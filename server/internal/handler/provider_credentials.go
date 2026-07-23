package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/providercredentials"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const factoryProvider = "factory"

type ProviderCredentialRefreshStore interface {
	Enqueue(runtimeID string)
	Pending(runtimeID string) bool
	Complete(runtimeID string)
}

type inMemoryProviderCredentialRefreshStore struct {
	mu      sync.Mutex
	pending map[string]struct{}
}

func NewInMemoryProviderCredentialRefreshStore() *inMemoryProviderCredentialRefreshStore {
	return &inMemoryProviderCredentialRefreshStore{pending: make(map[string]struct{})}
}

func (s *inMemoryProviderCredentialRefreshStore) Enqueue(runtimeID string) {
	s.mu.Lock()
	next := make(map[string]struct{}, len(s.pending)+1)
	for key := range s.pending {
		next[key] = struct{}{}
	}
	next[runtimeID] = struct{}{}
	s.pending = next
	s.mu.Unlock()
}

func (s *inMemoryProviderCredentialRefreshStore) Pending(runtimeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pending[runtimeID]
	return ok
}

func (s *inMemoryProviderCredentialRefreshStore) Complete(runtimeID string) {
	s.mu.Lock()
	next := make(map[string]struct{}, len(s.pending))
	for key := range s.pending {
		if key != runtimeID {
			next[key] = struct{}{}
		}
	}
	s.pending = next
	s.mu.Unlock()
}

type providerCredentialResponse struct {
	ID                   string  `json:"id"`
	Provider             string  `json:"provider"`
	AccountKey           string  `json:"account_key"`
	AccountLabel         string  `json:"account_label,omitempty"`
	Fingerprint          string  `json:"fingerprint"`
	LastValidatedAt      *string `json:"last_validated_at,omitempty"`
	LastValidationStatus string  `json:"last_validation_status"`
	LastValidationNote   string  `json:"last_validation_note,omitempty"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

type providerCredentialRequest struct {
	Provider     string `json:"provider"`
	Token        string `json:"token"`
	AccountLabel string `json:"account_label,omitempty"`
}

type daemonProviderCredential struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	AccountLabel string `json:"account_label,omitempty"`
	Token        string `json:"token"`
}

func (h *Handler) authorizeProviderCredentialManagement(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	actorType, _ := h.resolveActor(r, requestUserID(r), workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not manage provider credentials")
		return pgtype.UUID{}, false
	}
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return pgtype.UUID{}, false
	}
	return workspaceUUID, true
}

func (h *Handler) ListProviderCredentials(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorizeProviderCredentialManagement(w, r)
	if !ok {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	if provider == "" {
		provider = factoryProvider
	}
	if provider != factoryProvider {
		writeError(w, http.StatusUnprocessableEntity, "unsupported provider")
		return
	}
	rows, err := h.Queries.ListProviderCredentials(r.Context(), db.ListProviderCredentialsParams{WorkspaceID: workspaceID, Provider: provider})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider credentials")
		return
	}
	response := make([]providerCredentialResponse, len(rows))
	for index, row := range rows {
		response[index] = providerCredentialToResponse(row)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) CreateProviderCredential(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorizeProviderCredentialManagement(w, r)
	if !ok {
		return
	}
	request, ok := decodeProviderCredentialRequest(w, r, true)
	if !ok {
		return
	}
	sealed, err := h.ProviderCredentials.Seal(request.Token)
	if errors.Is(err, providercredentials.ErrKeyUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "provider credential encryption is not configured")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to protect provider credential")
		return
	}
	row, err := h.Queries.CreateProviderCredential(r.Context(), db.CreateProviderCredentialParams{
		WorkspaceID: workspaceID, Provider: factoryProvider, AccountLabel: request.AccountLabel,
		SealedToken: sealed, Fingerprint: providerCredentialFingerprint(request.Token),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store provider credential")
		return
	}
	h.notifyProviderCredentialsChanged(r.Context(), workspaceID)
	writeJSON(w, http.StatusCreated, providerCredentialToResponse(row))
}

func (h *Handler) ReplaceProviderCredential(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorizeProviderCredentialManagement(w, r)
	if !ok {
		return
	}
	credentialID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "credentialId"), "credential id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetProviderCredentialInWorkspace(r.Context(), db.GetProviderCredentialInWorkspaceParams{ID: credentialID, WorkspaceID: workspaceID}); err != nil {
		writeError(w, http.StatusNotFound, "provider credential not found")
		return
	}
	request, ok := decodeProviderCredentialRequest(w, r, false)
	if !ok {
		return
	}
	sealed, err := h.ProviderCredentials.Seal(request.Token)
	if errors.Is(err, providercredentials.ErrKeyUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "provider credential encryption is not configured")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to protect provider credential")
		return
	}
	row, err := h.Queries.ReplaceProviderCredentialToken(r.Context(), db.ReplaceProviderCredentialTokenParams{
		ID: credentialID, WorkspaceID: workspaceID, SealedToken: sealed, Fingerprint: providerCredentialFingerprint(request.Token),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to replace provider credential")
		return
	}
	h.notifyProviderCredentialsChanged(r.Context(), workspaceID)
	writeJSON(w, http.StatusOK, providerCredentialToResponse(row))
}

func (h *Handler) DeleteProviderCredential(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorizeProviderCredentialManagement(w, r)
	if !ok {
		return
	}
	credentialID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "credentialId"), "credential id")
	if !ok {
		return
	}
	row, err := h.Queries.GetProviderCredentialInWorkspace(r.Context(), db.GetProviderCredentialInWorkspaceParams{ID: credentialID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "provider credential not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider credential")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete provider credential")
		return
	}
	defer tx.Rollback(r.Context())
	queries := h.Queries.WithTx(tx)
	if _, err := queries.DeleteProviderLimitSnapshotsForAccount(r.Context(), db.DeleteProviderLimitSnapshotsForAccountParams{
		WorkspaceID: workspaceID, Provider: row.Provider, AccountKey: providerCredentialAccountKey(uuidToString(row.ID)),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete provider credential")
		return
	}
	if affected, err := queries.DeleteProviderCredential(r.Context(), db.DeleteProviderCredentialParams{ID: credentialID, WorkspaceID: workspaceID}); err != nil || affected != 1 {
		writeError(w, http.StatusInternalServerError, "failed to delete provider credential")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete provider credential")
		return
	}
	h.notifyProviderCredentialsChanged(r.Context(), workspaceID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetDaemonProviderCredentials(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	rows, err := h.Queries.ListProviderCredentials(r.Context(), db.ListProviderCredentialsParams{WorkspaceID: runtime.WorkspaceID, Provider: factoryProvider})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider credentials")
		return
	}
	response := make([]daemonProviderCredential, 0, len(rows))
	for _, row := range rows {
		token, err := h.ProviderCredentials.Open(row.SealedToken)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "provider credential encryption is unavailable")
			return
		}
		response = append(response, daemonProviderCredential{ID: uuidToString(row.ID), Provider: row.Provider, AccountLabel: row.AccountLabel, Token: token})
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": response})
	if h.ProviderCredentialRefreshStore != nil {
		h.ProviderCredentialRefreshStore.Complete(runtimeID)
	}
}

func decodeProviderCredentialRequest(w http.ResponseWriter, r *http.Request, requireProvider bool) (providerCredentialRequest, bool) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	var request providerCredentialRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid provider credential request")
		return providerCredentialRequest{}, false
	}
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	request.Token = strings.TrimSpace(request.Token)
	request.AccountLabel = strings.TrimSpace(request.AccountLabel)
	if requireProvider && request.Provider != factoryProvider {
		writeError(w, http.StatusUnprocessableEntity, "provider must be factory")
		return providerCredentialRequest{}, false
	}
	if request.Token == "" || len(request.Token) < 8 || len(request.Token) > 4096 || strings.ContainsAny(request.Token, "\r\n\x00") {
		writeError(w, http.StatusUnprocessableEntity, "token is invalid")
		return providerCredentialRequest{}, false
	}
	if len(request.AccountLabel) > 80 {
		writeError(w, http.StatusUnprocessableEntity, "account_label is too long")
		return providerCredentialRequest{}, false
	}
	return request, true
}

func providerCredentialToResponse(row db.ProviderCredential) providerCredentialResponse {
	return providerCredentialResponse{
		ID: uuidToString(row.ID), Provider: row.Provider, AccountKey: providerCredentialAccountKey(uuidToString(row.ID)), AccountLabel: row.AccountLabel, Fingerprint: row.Fingerprint,
		LastValidatedAt: timestampToPtr(row.LastValidatedAt), LastValidationStatus: row.LastValidationStatus,
		LastValidationNote: row.LastValidationNote, CreatedAt: timestampToString(row.CreatedAt), UpdatedAt: timestampToString(row.UpdatedAt),
	}
}

func providerCredentialFingerprint(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])[:12]
}

func providerCredentialAccountKey(id string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(id)))
	return hex.EncodeToString(hash[:])[:16]
}

func (h *Handler) notifyProviderCredentialsChanged(ctx context.Context, workspaceID pgtype.UUID) {
	if h.ProviderCredentialRefreshStore == nil {
		return
	}
	runtimes, err := h.Queries.ListAgentRuntimes(ctx, workspaceID)
	if err != nil {
		return
	}
	for _, runtime := range runtimes {
		h.ProviderCredentialRefreshStore.Enqueue(uuidToString(runtime.ID))
	}
}
