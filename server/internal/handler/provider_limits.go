package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
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
	// providerLimitUnknownAccountKey is the account key an adapter reports when
	// it could not identify an account at all — no credential, no local session.
	providerLimitUnknownAccountKey = "unavailable"
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

	accepted := 0
	for _, snapshot := range request.Snapshots {
		var factoryCredential *db.ProviderCredential
		// A keyed Factory snapshot claims a specific workspace credential, so it
		// is only accepted when that credential exists. The unkeyed snapshot
		// claims no account at all — it is how the daemon reports that no
		// credential is configured yet — and must still be stored, otherwise the
		// Factory card can never appear and the user never reaches the Details
		// dialog that onboards the credential (FRO-206).
		if snapshot.Provider == factoryProvider && snapshot.AccountKey != providerLimitUnknownAccountKey {
			credential, found, err := h.factoryCredentialForAccount(r.Context(), runtime.WorkspaceID, snapshot.AccountKey)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to validate provider limits account")
				return
			}
			if !found {
				continue
			}
			factoryCredential = &credential
		}
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
			DaemonID:               runtime.DaemonID.String,
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
		accepted++
		if factoryCredential != nil {
			status := "unavailable"
			if snapshot.Status == "ok" || snapshot.Status == "partial" {
				status = "valid"
			} else if snapshot.ErrorNote == "credential_invalid" {
				status = "invalid"
			}
			if err := h.Queries.UpdateProviderCredentialValidation(r.Context(), db.UpdateProviderCredentialValidationParams{
				ID: factoryCredential.ID, WorkspaceID: runtime.WorkspaceID,
				LastValidatedAt:      pgtype.Timestamptz{Time: snapshot.CheckedAt.UTC(), Valid: true},
				LastValidationStatus: status, LastValidationNote: snapshot.ErrorNote,
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update provider credential validation")
				return
			}
		}
	}
	if _, err := h.Queries.DeleteExpiredProviderLimitSnapshots(r.Context(), pgtype.Timestamptz{Time: time.Now().UTC().AddDate(0, 0, -30), Valid: true}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retain provider limits")
		return
	}
	if h.ProviderLimitRefreshStore != nil && len(request.RefreshIDs) > 0 {
		h.ProviderLimitRefreshStore.Complete(runtimeID, request.RefreshIDs)
	}

	writeJSON(w, http.StatusOK, map[string]int{"accepted": accepted})
}

func (h *Handler) factoryCredentialForAccount(ctx context.Context, workspaceID pgtype.UUID, accountKey string) (db.ProviderCredential, bool, error) {
	rows, err := h.Queries.ListProviderCredentials(ctx, db.ListProviderCredentialsParams{WorkspaceID: workspaceID, Provider: factoryProvider})
	if err != nil {
		return db.ProviderCredential{}, false, err
	}
	for _, row := range rows {
		if providerCredentialAccountKey(uuidToString(row.ID)) == accountKey {
			return row, true, nil
		}
	}
	return db.ProviderCredential{}, false, nil
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
	RuntimeID         string                   `json:"runtime_id"`
	DaemonID          string                   `json:"daemon_id,omitempty"`
	Provider          string                   `json:"provider"`
	AccountKey        string                   `json:"account_key"`
	AccountLabel      string                   `json:"account_label,omitempty"`
	CheckedAt         time.Time                `json:"checked_at"`
	Status            string                   `json:"status"`
	Source            providerLimitSourceInput `json:"source"`
	Buckets           json.RawMessage          `json:"buckets"`
	ErrorNote         string                   `json:"error_note,omitempty"`
	Stale             bool                     `json:"stale"`
	LastSuccessfulAt  *time.Time               `json:"last_successful_at,omitempty"`
	LastAttemptedAt   time.Time                `json:"last_attempted_at"`
	LastAttemptStatus string                   `json:"last_attempt_status"`
	LastAttemptSource providerLimitSourceInput `json:"last_attempt_source"`
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
	lastGoodAccounts, err := h.Queries.ListLatestGoodProviderLimitSnapshots(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider limits")
		return
	}
	byDaemon, err := h.Queries.ListLatestProviderLimitSnapshotsByDaemon(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider limits")
		return
	}
	lastGoodByDaemon, err := h.Queries.ListLatestGoodProviderLimitSnapshotsByDaemon(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider limits")
		return
	}
	accountCandidates := append(
		append(make([]db.ProviderLimitSnapshot, 0, len(accounts)+len(lastGoodAccounts)), accounts...),
		lastGoodAccounts...,
	)
	daemonCandidates := append(
		append(make([]db.ProviderLimitSnapshot, 0, len(byDaemon)+len(lastGoodByDaemon)), byDaemon...),
		lastGoodByDaemon...,
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"accounts": providerLimitRows(accountCandidates),
		"daemons":  providerLimitRowsByDaemon(daemonCandidates),
	})
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
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": providerLimitHistoryRows(filtered)})
}

func providerLimitRows(rows []db.ProviderLimitSnapshot) []providerLimitSnapshotResponse {
	return reconcileProviderLimitRows(rows, false)
}

func providerLimitRowsByDaemon(rows []db.ProviderLimitSnapshot) []providerLimitSnapshotResponse {
	return reconcileProviderLimitRows(rows, true)
}

func providerLimitHistoryRows(rows []db.ProviderLimitSnapshot) []providerLimitSnapshotResponse {
	response := make([]providerLimitSnapshotResponse, 0, len(rows))
	now := time.Now().UTC()
	for _, row := range rows {
		checkedAt := row.CheckedAt.Time.UTC()
		freshness := row.SourceFreshnessSeconds
		if freshness <= 0 {
			freshness = 900
		}
		var lastSuccessfulAt *time.Time
		if row.Status == "ok" || row.Status == "partial" {
			value := checkedAt
			lastSuccessfulAt = &value
		}
		source := providerLimitSourceInput{
			Kind:             row.SourceKind,
			Confidence:       row.SourceConfidence,
			FreshnessSeconds: row.SourceFreshnessSeconds,
		}
		response = append(response, providerLimitSnapshotResponse{
			RuntimeID:         uuidToString(row.RuntimeID),
			DaemonID:          row.DaemonID,
			Provider:          row.Provider,
			AccountKey:        row.AccountKey,
			AccountLabel:      row.AccountLabel,
			CheckedAt:         checkedAt,
			Status:            row.Status,
			Source:            source,
			Buckets:           row.Buckets,
			ErrorNote:         row.ErrorNote,
			Stale:             now.After(checkedAt.Add(time.Duration(freshness) * time.Second)),
			LastSuccessfulAt:  lastSuccessfulAt,
			LastAttemptedAt:   checkedAt,
			LastAttemptStatus: row.Status,
			LastAttemptSource: source,
		})
	}
	return response
}

type providerLimitRowGroup struct {
	accountKey string
	rows       []db.ProviderLimitSnapshot
}

func reconcileProviderLimitRows(rows []db.ProviderLimitSnapshot, byDaemon bool) []providerLimitSnapshotResponse {
	knownAccounts := providerLimitKnownAccounts(rows, byDaemon)
	groups := providerLimitGroups(rows, knownAccounts, byDaemon)
	response := make([]providerLimitSnapshotResponse, 0, len(groups))
	now := time.Now().UTC()
	for _, group := range groups {
		response = append(response, providerLimitResponseForGroup(group, now))
	}
	sort.Slice(response, func(left, right int) bool {
		leftKey := response[left].DaemonID + ":" + response[left].Provider + ":" + response[left].AccountKey
		rightKey := response[right].DaemonID + ":" + response[right].Provider + ":" + response[right].AccountKey
		return leftKey < rightKey
	})
	return response
}

func providerLimitKnownAccounts(rows []db.ProviderLimitSnapshot, byDaemon bool) map[string]map[string]struct{} {
	knownAccounts := make(map[string]map[string]struct{})
	for _, row := range rows {
		if row.AccountKey == providerLimitUnknownAccountKey {
			continue
		}
		scopeKey := providerLimitScopeKey(row, byDaemon)
		if knownAccounts[scopeKey] == nil {
			knownAccounts[scopeKey] = make(map[string]struct{})
		}
		knownAccounts[scopeKey][row.AccountKey] = struct{}{}
	}
	return knownAccounts
}

func providerLimitGroups(
	rows []db.ProviderLimitSnapshot,
	knownAccounts map[string]map[string]struct{},
	byDaemon bool,
) map[string]providerLimitRowGroup {
	groups := make(map[string]providerLimitRowGroup)
	for _, row := range rows {
		scopeKey := providerLimitScopeKey(row, byDaemon)
		accountKey := providerLimitCanonicalAccountKey(row.AccountKey, knownAccounts[scopeKey])
		groupKey := scopeKey + ":" + accountKey
		group := groups[groupKey]
		group.accountKey = accountKey
		group.rows = append(group.rows, row)
		groups[groupKey] = group
	}
	return groups
}

func providerLimitCanonicalAccountKey(accountKey string, knownAccounts map[string]struct{}) string {
	if accountKey != providerLimitUnknownAccountKey || len(knownAccounts) != 1 {
		return accountKey
	}
	for knownAccountKey := range knownAccounts {
		return knownAccountKey
	}
	return accountKey
}

func providerLimitResponseForGroup(group providerLimitRowGroup, now time.Time) providerLimitSnapshotResponse {
	latestAttempt, lastGood := latestProviderLimitRows(group.rows)
	display := latestAttempt
	var lastSuccessfulAt *time.Time
	if lastGood != nil {
		display = *lastGood
		value := lastGood.CheckedAt.Time.UTC()
		lastSuccessfulAt = &value
	}
	checkedAt := display.CheckedAt.Time.UTC()
	freshness := display.SourceFreshnessSeconds
	if freshness <= 0 {
		freshness = 900
	}
	accountLabel := display.AccountLabel
	if accountLabel == "" {
		accountLabel = latestAttempt.AccountLabel
	}
	return providerLimitSnapshotResponse{
		RuntimeID:    uuidToString(latestAttempt.RuntimeID),
		DaemonID:     latestAttempt.DaemonID,
		Provider:     display.Provider,
		AccountKey:   group.accountKey,
		AccountLabel: accountLabel,
		CheckedAt:    checkedAt,
		Status:       display.Status,
		Source: providerLimitSourceInput{
			Kind:             display.SourceKind,
			Confidence:       display.SourceConfidence,
			FreshnessSeconds: display.SourceFreshnessSeconds,
		},
		Buckets:           display.Buckets,
		ErrorNote:         latestAttempt.ErrorNote,
		Stale:             now.After(checkedAt.Add(time.Duration(freshness) * time.Second)),
		LastSuccessfulAt:  lastSuccessfulAt,
		LastAttemptedAt:   latestAttempt.CheckedAt.Time.UTC(),
		LastAttemptStatus: latestAttempt.Status,
		LastAttemptSource: providerLimitSourceInput{
			Kind:             latestAttempt.SourceKind,
			Confidence:       latestAttempt.SourceConfidence,
			FreshnessSeconds: latestAttempt.SourceFreshnessSeconds,
		},
	}
}

func providerLimitScopeKey(row db.ProviderLimitSnapshot, byDaemon bool) string {
	if byDaemon {
		return row.DaemonID + ":" + row.Provider
	}
	return row.Provider
}

func latestProviderLimitRows(rows []db.ProviderLimitSnapshot) (db.ProviderLimitSnapshot, *db.ProviderLimitSnapshot) {
	latestAttempt := rows[0]
	var lastGood *db.ProviderLimitSnapshot
	for index := range rows {
		row := rows[index]
		if row.CheckedAt.Time.After(latestAttempt.CheckedAt.Time) {
			latestAttempt = row
		}
		if row.Status != "ok" && row.Status != "partial" {
			continue
		}
		if lastGood == nil || row.CheckedAt.Time.After(lastGood.CheckedAt.Time) {
			copied := row
			lastGood = &copied
		}
	}
	return latestAttempt, lastGood
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
	case "ok", "stale", "partial", "unavailable", "error":
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
