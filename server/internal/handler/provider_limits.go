package handler

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	providerLimitsMaxRequestBytes = 256 << 10
	providerLimitsMaxSnapshots    = 32
	providerLimitsMaxBuckets      = 32
)

var (
	providerLimitsIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	providerLimitsAccountKey = regexp.MustCompile(`^(?:[a-f0-9]{8,64}|unavailable)$`)
	providerLimitsReason     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

type providerLimitsReportRequest struct {
	Snapshots  []providerLimitSnapshotInput `json:"snapshots"`
	RefreshIDs []string                     `json:"refresh_ids,omitempty"`
}

// ProviderLimitRefreshStore retains one pending request per runtime. The
// heartbeat only reads it; completion is piggybacked on the existing snapshot
// ingest so manual refresh never creates another daemon transport.
type ProviderLimitRefreshStore interface {
	Enqueue(runtimeID string) providerLimitRefreshRequest
	Pending(runtimeID string) *providerLimitRefreshRequest
	Complete(runtimeID string, ids []string)
}

type providerLimitRefreshRequest struct {
	ID string `json:"id"`
}

type inMemoryProviderLimitRefreshStore struct {
	mu      sync.Mutex
	pending map[string]providerLimitRefreshRequest
}

func NewInMemoryProviderLimitRefreshStore() *inMemoryProviderLimitRefreshStore {
	return &inMemoryProviderLimitRefreshStore{pending: make(map[string]providerLimitRefreshRequest)}
}

func (s *inMemoryProviderLimitRefreshStore) Enqueue(runtimeID string) providerLimitRefreshRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request, ok := s.pending[runtimeID]; ok {
		return request
	}
	request := providerLimitRefreshRequest{ID: randomID()}
	s.pending[runtimeID] = request
	return request
}

func (s *inMemoryProviderLimitRefreshStore) Pending(runtimeID string) *providerLimitRefreshRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	request, ok := s.pending[runtimeID]
	if !ok {
		return nil
	}
	return &request
}

func (s *inMemoryProviderLimitRefreshStore) Complete(runtimeID string, ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request, ok := s.pending[runtimeID]
	if !ok {
		return
	}
	for _, id := range ids {
		if id == request.ID {
			delete(s.pending, runtimeID)
			return
		}
	}
}

type providerLimitSnapshotInput struct {
	Provider     string                     `json:"provider"`
	AccountKey   string                     `json:"account_key"`
	AccountLabel string                     `json:"account_label"`
	CheckedAt    time.Time                  `json:"checked_at"`
	Status       string                     `json:"status"`
	Source       providerLimitSourceInput   `json:"source"`
	Buckets      []providerLimitBucketInput `json:"buckets"`
	ErrorNote    string                     `json:"error_note"`
}

type providerLimitSourceInput struct {
	Kind             string `json:"kind"`
	FreshnessSeconds int64  `json:"freshness_seconds"`
	Confidence       string `json:"confidence"`
}

type providerLimitBucketInput struct {
	ID             string     `json:"id"`
	Label          string     `json:"label"`
	Unit           string     `json:"unit"`
	LimitValue     *float64   `json:"limit_value"`
	UsedValue      *float64   `json:"used_value"`
	RemainingValue *float64   `json:"remaining_value"`
	ResetsAt       *time.Time `json:"resets_at"`
	Status         string     `json:"status"`
	Note           string     `json:"note"`
}

func (h *Handler) ReportProviderLimits(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}

	request, ok := decodeProviderLimitsReport(w, r)
	if !ok {
		return
	}
	if err := validateProviderLimitsReport(request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	for _, snapshot := range request.Snapshots {
		buckets, err := json.Marshal(snapshot.Buckets)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid provider limits payload")
			return
		}
		content, err := json.Marshal(snapshot)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid provider limits payload")
			return
		}
		hash := sha256.Sum256(content)
		if _, err := h.Queries.UpsertProviderLimitSnapshot(r.Context(), db.UpsertProviderLimitSnapshotParams{
			WorkspaceID:            runtime.WorkspaceID,
			RuntimeID:              runtime.ID,
			Provider:               snapshot.Provider,
			AccountKey:             snapshot.AccountKey,
			AccountLabel:           snapshot.AccountLabel,
			CheckedAt:              pgtype.Timestamptz{Time: snapshot.CheckedAt.UTC(), Valid: true},
			Status:                 snapshot.Status,
			SourceKind:             snapshot.Source.Kind,
			SourceConfidence:       snapshot.Source.Confidence,
			SourceFreshnessSeconds: snapshot.Source.FreshnessSeconds,
			Buckets:                buckets,
			ErrorNote:              snapshot.ErrorNote,
			ContentHash:            stringHash(hash),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store provider limits")
			return
		}
	}
	if _, err := h.Queries.DeleteExpiredProviderLimitSnapshots(r.Context(), pgtype.Timestamptz{Time: time.Now().UTC().AddDate(0, 0, -30), Valid: true}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retain provider limits")
		return
	}
	if h.ProviderLimitRefreshStore != nil && len(request.RefreshIDs) > 0 {
		h.ProviderLimitRefreshStore.Complete(runtimeID, request.RefreshIDs)
	}

	writeJSON(w, http.StatusOK, map[string]int{"accepted": len(request.Snapshots)})
}

// RequestProviderLimitsRefresh queues a manual collection for one daemon
// runtime. Repeated clicks reuse the same request until the collector's normal
// snapshot ingest confirms it, making the action reconnect-safe and deduped.
func (h *Handler) RequestProviderLimitsRefresh(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RuntimeID string `json:"runtime_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.RuntimeID == "" {
		writeError(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	runtimeUUID, ok := parseUUIDOrBadRequest(w, request.RuntimeID, "runtime_id")
	if !ok {
		return
	}
	runtime, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(runtime.WorkspaceID), "runtime not found"); !ok {
		return
	}
	if runtime.Status != "online" {
		writeError(w, http.StatusServiceUnavailable, "runtime is offline")
		return
	}
	if h.ProviderLimitRefreshStore == nil {
		writeError(w, http.StatusServiceUnavailable, "provider limit refresh is unavailable")
		return
	}
	writeJSON(w, http.StatusAccepted, h.ProviderLimitRefreshStore.Enqueue(request.RuntimeID))
}

type providerLimitSnapshotResponse struct {
	RuntimeID    string                   `json:"runtime_id"`
	Provider     string                   `json:"provider"`
	AccountKey   string                   `json:"account_key"`
	AccountLabel string                   `json:"account_label,omitempty"`
	CheckedAt    time.Time                `json:"checked_at"`
	Status       string                   `json:"status"`
	Source       providerLimitSourceInput `json:"source"`
	Buckets      json.RawMessage          `json:"buckets"`
	ErrorNote    string                   `json:"error_note,omitempty"`
	Stale        bool                     `json:"stale"`
}

func (h *Handler) GetProviderLimits(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}
	accounts, err := h.Queries.ListLatestProviderLimitSnapshots(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider limits")
		return
	}
	byDaemon, err := h.Queries.ListLatestProviderLimitSnapshotsByRuntime(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider limits")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": providerLimitRows(accounts), "daemons": providerLimitRows(byDaemon)})
}

func (h *Handler) GetProviderLimitHistory(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}
	rows, err := h.Queries.ListProviderLimitSnapshotHistory(r.Context(), db.ListProviderLimitSnapshotHistoryParams{WorkspaceID: workspaceUUID, Limit: 200})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider limit history")
		return
	}
	provider, accountKey := r.URL.Query().Get("provider"), r.URL.Query().Get("account_key")
	filtered := make([]db.ProviderLimitSnapshot, 0, len(rows))
	for _, row := range rows {
		if (provider == "" || row.Provider == provider) && (accountKey == "" || row.AccountKey == accountKey) {
			filtered = append(filtered, row)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": providerLimitRows(filtered)})
}

func providerLimitRows(rows []db.ProviderLimitSnapshot) []providerLimitSnapshotResponse {
	response := make([]providerLimitSnapshotResponse, 0, len(rows))
	now := time.Now().UTC()
	for _, row := range rows {
		checkedAt := row.CheckedAt.Time.UTC()
		freshness := row.SourceFreshnessSeconds
		if freshness <= 0 {
			freshness = 900
		}
		response = append(response, providerLimitSnapshotResponse{RuntimeID: uuidToString(row.RuntimeID), Provider: row.Provider, AccountKey: row.AccountKey, AccountLabel: row.AccountLabel, CheckedAt: checkedAt, Status: row.Status, Source: providerLimitSourceInput{Kind: row.SourceKind, Confidence: row.SourceConfidence, FreshnessSeconds: row.SourceFreshnessSeconds}, Buckets: row.Buckets, ErrorNote: row.ErrorNote, Stale: now.After(checkedAt.Add(time.Duration(freshness) * time.Second))})
	}
	return response
}

func stringHash(hash [sha256.Size]byte) string {
	const hex = "0123456789abcdef"
	encoded := make([]byte, sha256.Size*2)
	for index, value := range hash {
		encoded[index*2] = hex[value>>4]
		encoded[index*2+1] = hex[value&0x0f]
	}
	return string(encoded)
}

func decodeProviderLimitsReport(w http.ResponseWriter, r *http.Request) (providerLimitsReportRequest, bool) {
	decoder := json.NewDecoder(io.LimitReader(r.Body, providerLimitsMaxRequestBytes+1))
	decoder.DisallowUnknownFields()
	var request providerLimitsReportRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid provider limits request")
		return providerLimitsReportRequest{}, false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid provider limits request")
		return providerLimitsReportRequest{}, false
	}
	return request, true
}

func validateProviderLimitsReport(request providerLimitsReportRequest) error {
	if len(request.Snapshots) == 0 || len(request.Snapshots) > providerLimitsMaxSnapshots {
		return errInvalidProviderLimitsPayload
	}
	for _, snapshot := range request.Snapshots {
		if !providerLimitsIdentifier.MatchString(snapshot.Provider) || !providerLimitsAccountKey.MatchString(snapshot.AccountKey) || snapshot.CheckedAt.IsZero() || !providerLimitStatusValid(snapshot.Status) || !providerLimitSourceValid(snapshot.Source) || len(snapshot.Buckets) > providerLimitsMaxBuckets || !providerLimitsReasonOrEmpty(snapshot.ErrorNote) {
			return errInvalidProviderLimitsPayload
		}
		for _, bucket := range snapshot.Buckets {
			if !providerLimitsIdentifier.MatchString(bucket.ID) || strings.TrimSpace(bucket.Label) == "" || len(bucket.Label) > 64 || !providerLimitUnitValid(bucket.Unit) || !providerLimitStatusValid(bucket.Status) || !providerLimitsReasonOrEmpty(bucket.Note) {
				return errInvalidProviderLimitsPayload
			}
		}
	}
	return nil
}

var errInvalidProviderLimitsPayload = providerLimitsPayloadError{}

type providerLimitsPayloadError struct{}

func (providerLimitsPayloadError) Error() string { return "invalid provider limits payload" }

func providerLimitStatusValid(status string) bool {
	switch status {
	case "ok", "partial", "unavailable", "error":
		return true
	default:
		return false
	}
}

func providerLimitSourceValid(source providerLimitSourceInput) bool {
	if source.FreshnessSeconds < 0 {
		return false
	}
	switch source.Kind {
	case "official_api", "cli", "local_auth_state", "local_log":
	default:
		return false
	}
	switch source.Confidence {
	case "official", "observed", "estimated":
		return true
	default:
		return false
	}
}

func providerLimitUnitValid(unit string) bool {
	switch unit {
	case "percent", "tokens", "credits", "currency", "requests":
		return true
	default:
		return false
	}
}

func providerLimitsReasonOrEmpty(value string) bool {
	return value == "" || providerLimitsReason.MatchString(value)
}
