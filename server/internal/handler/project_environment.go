package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	projectEnvironmentActivityCreated  = "project_environment_created"
	projectEnvironmentActivityUpdated  = "project_environment_updated"
	projectEnvironmentActivityDeleted  = "project_environment_deleted"
	projectEnvironmentActivityRevealed = "project_environment_revealed"
)

type ProjectEnvironmentResponse struct {
	ID                string            `json:"id"`
	ProjectID         string            `json:"project_id"`
	WorkspaceID       string            `json:"workspace_id"`
	Name              string            `json:"name"`
	Description       *string           `json:"description"`
	Config            json.RawMessage   `json:"config"`
	Secrets           map[string]string `json:"secrets"`
	AllowedRuntimeIDs []string          `json:"allowed_runtime_ids"`
	CreatedBy         *string           `json:"created_by"`
	CreatedAt         string            `json:"created_at"`
	UpdatedAt         string            `json:"updated_at"`
}

type ProjectEnvironmentRevealResponse struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"project_id"`
	WorkspaceID string            `json:"workspace_id"`
	Name        string            `json:"name"`
	Secrets     map[string]string `json:"secrets"`
}

type projectEnvironmentRequest struct {
	Name              string            `json:"name"`
	Description       *string           `json:"description"`
	Config            json.RawMessage   `json:"config"`
	Secrets           map[string]string `json:"secrets"`
	AllowedRuntimeIDs []string          `json:"allowed_runtime_ids"`
}

func (h *Handler) authorizeProjectEnvironmentManagement(w http.ResponseWriter, r *http.Request) (db.Project, db.Member, bool) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Project{}, db.Member{}, false
	}

	workspaceID := uuidToString(project.WorkspaceID)
	actorType, _ := h.resolveActor(r, requestUserID(r), workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not access project environment management endpoints")
		return db.Project{}, db.Member{}, false
	}

	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "project not found", "owner", "admin")
	if !ok {
		return db.Project{}, db.Member{}, false
	}

	return project, member, true
}

func (h *Handler) loadProjectEnvironmentForManagement(w http.ResponseWriter, r *http.Request, project db.Project) (db.ProjectEnvironment, bool) {
	envUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "envId"), "environment id")
	if !ok {
		return db.ProjectEnvironment{}, false
	}
	env, err := h.Queries.GetProjectEnvironmentInWorkspace(r.Context(), db.GetProjectEnvironmentInWorkspaceParams{
		ID:          envUUID,
		WorkspaceID: project.WorkspaceID,
	})
	if err != nil || uuidToString(env.ProjectID) != uuidToString(project.ID) {
		writeError(w, http.StatusNotFound, "project environment not found")
		return db.ProjectEnvironment{}, false
	}
	return env, true
}

func (h *Handler) ListProjectEnvironments(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.authorizeProjectEnvironmentManagement(w, r)
	if !ok {
		return
	}

	envs, err := h.Queries.ListProjectEnvironments(r.Context(), project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project environments")
		return
	}
	resp := make([]ProjectEnvironmentResponse, len(envs))
	for i, env := range envs {
		item, err := h.projectEnvironmentToResponse(r.Context(), env)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list project environments")
			return
		}
		resp[i] = item
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": resp, "total": len(resp)})
}

func (h *Handler) CreateProjectEnvironment(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.authorizeProjectEnvironmentManagement(w, r)
	if !ok {
		return
	}

	req, ok := decodeProjectEnvironmentRequest(w, r)
	if !ok {
		return
	}
	config, ok := normalizeJSONObject(w, req.Config, "config")
	if !ok {
		return
	}
	secrets := normalizeSecretMap(req.Secrets)
	secretBytes, err := json.Marshal(secrets)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode project environment")
		return
	}
	runtimeIDs, ok := h.parseProjectEnvironmentRuntimeIDs(w, r, project.WorkspaceID, req.AllowedRuntimeIDs)
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("project environment create: begin tx failed", append(logger.RequestAttrs(r), "error", err, "project_id", uuidToString(project.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create project environment")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	env, err := qtx.CreateProjectEnvironment(r.Context(), db.CreateProjectEnvironmentParams{
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
		Name:        req.Name,
		Description: textFromPtr(req.Description),
		Config:      config,
		Secrets:     secretBytes,
		CreatedBy:   member.UserID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "project environment name already exists")
			return
		}
		slog.Warn("create project environment failed", append(logger.RequestAttrs(r), "error", err, "project_id", uuidToString(project.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create project environment")
		return
	}
	if err := qtx.SetProjectEnvironmentRuntimeAllowlist(r.Context(), db.SetProjectEnvironmentRuntimeAllowlistParams{
		EnvironmentID: env.ID,
		RuntimeIds:    runtimeIDs,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update project environment allowlist")
		return
	}
	if !h.auditProjectEnvironmentMutation(w, r, qtx, project.WorkspaceID, member.UserID, projectEnvironmentActivityCreated, map[string]any{
		"environment_id": uuidToString(env.ID),
		"project_id":     uuidToString(project.ID),
		"name":           env.Name,
		"secret_keys":    sortedKeys(secrets),
		"runtime_ids":    uuidStrings(runtimeIDs),
	}) {
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create project environment")
		return
	}

	resp, err := h.projectEnvironmentToResponse(r.Context(), env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create project environment")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) UpdateProjectEnvironment(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.authorizeProjectEnvironmentManagement(w, r)
	if !ok {
		return
	}
	existing, ok := h.loadProjectEnvironmentForManagement(w, r, project)
	if !ok {
		return
	}

	req, ok := decodeProjectEnvironmentRequest(w, r)
	if !ok {
		return
	}
	config, ok := normalizeJSONObject(w, req.Config, "config")
	if !ok {
		return
	}
	mergedSecrets, audit := mergeAgentEnv(unmarshalProjectEnvironmentSecrets(existing), normalizeSecretMap(req.Secrets))
	secretBytes, err := json.Marshal(mergedSecrets)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode project environment")
		return
	}
	runtimeIDs, ok := h.parseProjectEnvironmentRuntimeIDs(w, r, project.WorkspaceID, req.AllowedRuntimeIDs)
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("project environment update: begin tx failed", append(logger.RequestAttrs(r), "error", err, "environment_id", uuidToString(existing.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to update project environment")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	updated, err := qtx.UpdateProjectEnvironment(r.Context(), db.UpdateProjectEnvironmentParams{
		ID:          existing.ID,
		Name:        req.Name,
		Description: textFromPtr(req.Description),
		Config:      config,
		Secrets:     secretBytes,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "project environment name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update project environment")
		return
	}
	if err := qtx.SetProjectEnvironmentRuntimeAllowlist(r.Context(), db.SetProjectEnvironmentRuntimeAllowlistParams{
		EnvironmentID: updated.ID,
		RuntimeIds:    runtimeIDs,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update project environment allowlist")
		return
	}
	if !h.auditProjectEnvironmentMutation(w, r, qtx, project.WorkspaceID, member.UserID, projectEnvironmentActivityUpdated, map[string]any{
		"environment_id": uuidToString(updated.ID),
		"project_id":     uuidToString(project.ID),
		"name":           updated.Name,
		"added_keys":     audit.added,
		"removed_keys":   audit.removed,
		"changed_keys":   audit.changed,
		"preserved_keys": audit.preserved,
		"runtime_ids":    uuidStrings(runtimeIDs),
	}) {
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update project environment")
		return
	}

	resp, err := h.projectEnvironmentToResponse(r.Context(), updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update project environment")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) DeleteProjectEnvironment(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.authorizeProjectEnvironmentManagement(w, r)
	if !ok {
		return
	}
	env, ok := h.loadProjectEnvironmentForManagement(w, r, project)
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete project environment")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	if err := qtx.DeleteProjectEnvironment(r.Context(), env.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete project environment")
		return
	}
	if !h.auditProjectEnvironmentMutation(w, r, qtx, project.WorkspaceID, member.UserID, projectEnvironmentActivityDeleted, map[string]any{
		"environment_id": uuidToString(env.ID),
		"project_id":     uuidToString(project.ID),
		"name":           env.Name,
		"secret_keys":    sortedKeys(unmarshalProjectEnvironmentSecrets(env)),
	}) {
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete project environment")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetProjectEnvironmentReveal(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.authorizeProjectEnvironmentManagement(w, r)
	if !ok {
		return
	}
	env, ok := h.loadProjectEnvironmentForManagement(w, r, project)
	if !ok {
		return
	}

	secrets := unmarshalProjectEnvironmentSecrets(env)
	keys := sortedKeys(secrets)
	details, _ := json.Marshal(map[string]any{
		"environment_id": uuidToString(env.ID),
		"project_id":     uuidToString(project.ID),
		"name":           env.Name,
		"revealed_keys":  keys,
		"key_count":      len(keys),
	})
	if _, err := h.Queries.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: project.WorkspaceID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     member.UserID,
		Action:      projectEnvironmentActivityRevealed,
		Details:     details,
	}); err != nil {
		slog.Error("project_environment_revealed audit write failed; refusing to serve plaintext", append(logger.RequestAttrs(r), "error", err, "environment_id", uuidToString(env.ID))...)
		writeError(w, http.StatusInternalServerError, "audit log write failed; refusing to serve environment secrets without a recorded reveal")
		return
	}

	writeJSON(w, http.StatusOK, ProjectEnvironmentRevealResponse{
		ID:          uuidToString(env.ID),
		ProjectID:   uuidToString(env.ProjectID),
		WorkspaceID: uuidToString(env.WorkspaceID),
		Name:        env.Name,
		Secrets:     secrets,
	})
}

func decodeProjectEnvironmentRequest(w http.ResponseWriter, r *http.Request) (projectEnvironmentRequest, bool) {
	var req projectEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return projectEnvironmentRequest{}, false
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return projectEnvironmentRequest{}, false
	}
	if req.Description != nil {
		trimmed := strings.TrimSpace(*req.Description)
		if trimmed == "" {
			req.Description = nil
		} else {
			req.Description = &trimmed
		}
	}
	return req, true
}

func normalizeJSONObject(w http.ResponseWriter, raw json.RawMessage, field string) ([]byte, bool) {
	if len(raw) == 0 {
		return []byte(`{}`), true
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		writeError(w, http.StatusBadRequest, field+" must be a JSON object")
		return nil, false
	}
	out, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode "+field)
		return nil, false
	}
	return out, true
}

func normalizeSecretMap(secrets map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range secrets {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		out[trimmed] = value
	}
	return out
}

func (h *Handler) parseProjectEnvironmentRuntimeIDs(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, raw []string) ([]pgtype.UUID, bool) {
	seen := map[string]bool{}
	out := make([]pgtype.UUID, 0, len(raw))
	for i, runtimeID := range raw {
		trimmed := strings.TrimSpace(runtimeID)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "allowed_runtime_ids["+strconv.Itoa(i)+"] is required")
			return nil, false
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		runtimeUUID, ok := parseUUIDOrBadRequest(w, trimmed, "allowed_runtime_ids["+strconv.Itoa(i)+"]")
		if !ok {
			return nil, false
		}
		if _, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
			ID:          runtimeUUID,
			WorkspaceID: workspaceID,
		}); err != nil {
			writeError(w, http.StatusBadRequest, "allowed_runtime_ids must reference runtimes in this workspace")
			return nil, false
		}
		out = append(out, runtimeUUID)
	}
	return out, true
}

func (h *Handler) projectEnvironmentToResponse(ctx context.Context, env db.ProjectEnvironment) (ProjectEnvironmentResponse, error) {
	secrets := maskedSecretMap(unmarshalProjectEnvironmentSecrets(env))
	runtimeRows, err := h.Queries.ListProjectEnvironmentDaemons(ctx, env.ID)
	if err != nil {
		return ProjectEnvironmentResponse{}, err
	}
	runtimeIDs := make([]string, 0, len(runtimeRows))
	for _, row := range runtimeRows {
		runtimeIDs = append(runtimeIDs, uuidToString(row.RuntimeID))
	}
	sort.Strings(runtimeIDs)

	config := json.RawMessage(env.Config)
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	return ProjectEnvironmentResponse{
		ID:                uuidToString(env.ID),
		ProjectID:         uuidToString(env.ProjectID),
		WorkspaceID:       uuidToString(env.WorkspaceID),
		Name:              env.Name,
		Description:       textToPtr(env.Description),
		Config:            config,
		Secrets:           secrets,
		AllowedRuntimeIDs: runtimeIDs,
		CreatedBy:         uuidToPtr(env.CreatedBy),
		CreatedAt:         timestampToString(env.CreatedAt),
		UpdatedAt:         timestampToString(env.UpdatedAt),
	}, nil
}

func maskedSecretMap(secrets map[string]string) map[string]string {
	out := make(map[string]string, len(secrets))
	for key := range secrets {
		out[key] = envSentinel
	}
	return out
}

func unmarshalProjectEnvironmentSecrets(env db.ProjectEnvironment) map[string]string {
	out := map[string]string{}
	if len(env.Secrets) == 0 {
		return out
	}
	if err := json.Unmarshal(env.Secrets, &out); err != nil {
		slog.Warn("failed to unmarshal project environment secrets", "environment_id", uuidToString(env.ID), "error", err)
		return map[string]string{}
	}
	if out == nil {
		return map[string]string{}
	}
	return out
}

func (h *Handler) auditProjectEnvironmentMutation(w http.ResponseWriter, r *http.Request, qtx *db.Queries, workspaceID, actorID pgtype.UUID, action string, detailsMap map[string]any) bool {
	details, _ := json.Marshal(detailsMap)
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: workspaceID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     actorID,
		Action:      action,
		Details:     details,
	}); err != nil {
		slog.Error("project environment audit write failed; rolling back mutation", append(logger.RequestAttrs(r), "error", err, "action", action)...)
		writeError(w, http.StatusInternalServerError, "audit log write failed; project environment mutation rolled back")
		return false
	}
	return true
}

func textFromPtr(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func uuidStrings(ids []pgtype.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, uuidToString(id))
	}
	sort.Strings(out)
	return out
}
