package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Rule Groups: workspace-scoped collections of markdown rules that can be bound
// to a workspace, project, squad, or agent. The "effective rules" endpoint
// assembles the rules that apply to a given (project, squad, agent) combination
// by layering bindings in a fixed order. This file is the data-model + API layer
// only; runtime injection of effective rules is a later task in the epic.

// --- Response structs ---

type RuleGroupResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Enabled     bool    `json:"enabled"`
	SourceType  string  `json:"source_type"`
	SourceRef   any     `json:"source_ref"`
	Version     *string `json:"version"`
	CreatedBy   *string `json:"created_by"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type RuleGroupSummaryResponse struct {
	RuleGroupResponse
	RuleCount    int64 `json:"rule_count"`
	BindingCount int64 `json:"binding_count"`
}

type RuleGroupRuleResponse struct {
	ID           string   `json:"id"`
	WorkspaceID  string   `json:"workspace_id"`
	RuleGroupID  string   `json:"rule_group_id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Content      string   `json:"content"`
	SortOrder    int32    `json:"sort_order"`
	Enabled      bool     `json:"enabled"`
	FileName     *string  `json:"file_name"`
	Tags         []string `json:"tags"`
	RuntimeHints any      `json:"runtime_hints"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type RuleGroupWithRulesResponse struct {
	RuleGroupResponse
	Rules []RuleGroupRuleResponse `json:"rules"`
}

type RuleGroupBindingResponse struct {
	ID            string  `json:"id"`
	WorkspaceID   string  `json:"workspace_id"`
	RuleGroupID   string  `json:"rule_group_id"`
	RuleGroupName string  `json:"rule_group_name,omitempty"`
	ScopeType     string  `json:"scope_type"`
	ScopeID       *string `json:"scope_id"`
	Enabled       bool    `json:"enabled"`
	SortOrder     int32   `json:"sort_order"`
	CreatedBy     *string `json:"created_by"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type EffectiveRulesResponse struct {
	WorkspaceID string               `json:"workspace_id"`
	Inputs      EffectiveRulesInputs `json:"inputs"`
	Layers      []EffectiveRuleLayer `json:"layers"`
	Rules       []EffectiveRule      `json:"rules"`
}

type EffectiveRulesInputs struct {
	ProjectID *string `json:"project_id"`
	SquadID   *string `json:"squad_id"`
	AgentID   *string `json:"agent_id"`
}

type EffectiveRuleLayer struct {
	ScopeType string                    `json:"scope_type"`
	ScopeID   *string                   `json:"scope_id"`
	Groups    []EffectiveRuleLayerGroup `json:"groups"`
}

type EffectiveRuleLayerGroup struct {
	BindingID   string `json:"binding_id"`
	RuleGroupID string `json:"rule_group_id"`
	Name        string `json:"name"`
	RuleCount   int    `json:"rule_count"`
}

type EffectiveRule struct {
	ID            string  `json:"id"`
	RuleGroupID   string  `json:"rule_group_id"`
	RuleGroupName string  `json:"rule_group_name"`
	ScopeType     string  `json:"scope_type"`
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	Content       string  `json:"content"`
	SortOrder     int32   `json:"sort_order"`
	FileName      *string `json:"file_name"`
	RuntimeHints  any     `json:"runtime_hints"`
}

// --- Request structs ---

type CreateRuleGroupRequest struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Enabled     *bool   `json:"enabled"`
	Version     *string `json:"version"`
}

type UpdateRuleGroupRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
	Version     *string `json:"version"`
}

type CreateRuleGroupRuleRequest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Content      string   `json:"content"`
	SortOrder    int32    `json:"sort_order"`
	Enabled      *bool    `json:"enabled"`
	FileName     *string  `json:"file_name"`
	Tags         []string `json:"tags"`
	RuntimeHints any      `json:"runtime_hints"`
}

type UpdateRuleGroupRuleRequest struct {
	Name         *string  `json:"name"`
	Description  *string  `json:"description"`
	Content      *string  `json:"content"`
	SortOrder    *int32   `json:"sort_order"`
	Enabled      *bool    `json:"enabled"`
	FileName     *string  `json:"file_name"`
	Tags         []string `json:"tags"`
	RuntimeHints any      `json:"runtime_hints"`
}

type CreateRuleGroupBindingRequest struct {
	RuleGroupID string  `json:"rule_group_id"`
	ScopeType   string  `json:"scope_type"`
	ScopeID     *string `json:"scope_id"`
	Enabled     *bool   `json:"enabled"`
	SortOrder   int32   `json:"sort_order"`
}

type UpdateRuleGroupBindingRequest struct {
	Enabled   *bool  `json:"enabled"`
	SortOrder *int32 `json:"sort_order"`
}

// --- Helpers ---

// decodeJSONObject decodes a JSONB blob, defaulting to {} when missing or
// unparseable so the API surface always returns a JSON object.
func decodeJSONObject(raw []byte) any {
	var v any
	if raw != nil {
		_ = json.Unmarshal(raw, &v)
	}
	if v == nil {
		return map[string]any{}
	}
	return v
}

func ruleGroupToResponse(g db.RuleGroup) RuleGroupResponse {
	return RuleGroupResponse{
		ID:          uuidToString(g.ID),
		WorkspaceID: uuidToString(g.WorkspaceID),
		Name:        g.Name,
		Description: g.Description,
		Enabled:     g.Enabled,
		SourceType:  g.SourceType,
		SourceRef:   decodeJSONObject(g.SourceRef),
		Version:     textToPtr(g.Version),
		CreatedBy:   uuidToPtr(g.CreatedBy),
		CreatedAt:   timestampToString(g.CreatedAt),
		UpdatedAt:   timestampToString(g.UpdatedAt),
	}
}

func ruleGroupRuleToResponse(rule db.RuleGroupRule) RuleGroupRuleResponse {
	return RuleGroupRuleResponse{
		ID:           uuidToString(rule.ID),
		WorkspaceID:  uuidToString(rule.WorkspaceID),
		RuleGroupID:  uuidToString(rule.RuleGroupID),
		Name:         rule.Name,
		Description:  rule.Description,
		Content:      rule.Content,
		SortOrder:    rule.SortOrder,
		Enabled:      rule.Enabled,
		FileName:     textToPtr(rule.FileName),
		Tags:         rule.Tags,
		RuntimeHints: decodeJSONObject(rule.RuntimeHints),
		CreatedAt:    timestampToString(rule.CreatedAt),
		UpdatedAt:    timestampToString(rule.UpdatedAt),
	}
}

// bindingScope derives the (scope_type, scope_id) pair from the three nullable
// target columns. Zero targets set = workspace scope.
func bindingScope(projectID, squadID, agentID pgtype.UUID) (string, *string) {
	switch {
	case projectID.Valid:
		return "project", uuidToPtr(projectID)
	case squadID.Valid:
		return "squad", uuidToPtr(squadID)
	case agentID.Valid:
		return "agent", uuidToPtr(agentID)
	default:
		return "workspace", nil
	}
}

func ruleGroupBindingToResponse(b db.RuleGroupBinding, groupName string) RuleGroupBindingResponse {
	scopeType, scopeID := bindingScope(b.ProjectID, b.SquadID, b.AgentID)
	return RuleGroupBindingResponse{
		ID:            uuidToString(b.ID),
		WorkspaceID:   uuidToString(b.WorkspaceID),
		RuleGroupID:   uuidToString(b.RuleGroupID),
		RuleGroupName: groupName,
		ScopeType:     scopeType,
		ScopeID:       scopeID,
		Enabled:       b.Enabled,
		SortOrder:     b.SortOrder,
		CreatedBy:     uuidToPtr(b.CreatedBy),
		CreatedAt:     timestampToString(b.CreatedAt),
		UpdatedAt:     timestampToString(b.UpdatedAt),
	}
}

// loadRuleGroupForUser resolves a rule group by id within the request's
// workspace, writing a 400/404 and returning ok=false on failure.
func (h *Handler) loadRuleGroupForUser(w http.ResponseWriter, r *http.Request, id string) (db.RuleGroup, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return db.RuleGroup{}, false
	}
	groupUUID, ok := parseUUIDOrBadRequest(w, id, "rule group id")
	if !ok {
		return db.RuleGroup{}, false
	}
	group, err := h.Queries.GetRuleGroupInWorkspace(r.Context(), db.GetRuleGroupInWorkspaceParams{
		ID:          groupUUID,
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "rule group not found")
		return db.RuleGroup{}, false
	}
	return group, true
}

// authorizeRuleGroupMutation gates every create/update/delete on rule groups,
// rules, and bindings. Rule management is a human-owner concern: a running
// agent (task token) must not be able to rewrite the very rules that govern it,
// even though its task token authenticates as the owning human. Mirrors the
// agent-env env-management gate.
func (h *Handler) authorizeRuleGroupMutation(w http.ResponseWriter, r *http.Request, workspaceID string) (db.Member, bool) {
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return db.Member{}, false
	}
	if actorType, _ := h.resolveActor(r, requestUserID(r), workspaceID); actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not manage rule groups")
		return db.Member{}, false
	}
	return h.requireWorkspaceRole(w, r, workspaceID, "rule group not found", "owner", "admin")
}

// ruleGroupSourceBuiltin is the source_type of platform-seeded rule groups.
const ruleGroupSourceBuiltin = "builtin"

// rejectBuiltinMutation blocks structural changes to a builtin rule group.
// Builtin groups are seeded from the embedded catalog and managed by the
// platform, so their identity and content must not drift via the API: rename,
// delete, and add/remove/edit of their rules are rejected. Only enable/disable
// (group and rule) and bindings stay mutable; content changes go through
// re-seeding the catalog. Returns true (after writing a 403) when blocked.
func rejectBuiltinMutation(w http.ResponseWriter, g db.RuleGroup, action string) bool {
	if g.SourceType == ruleGroupSourceBuiltin {
		writeError(w, http.StatusForbidden, "builtin rule groups are managed by the platform; "+action+" is not allowed")
		return true
	}
	return false
}

// validateScopeTarget checks that a scope target id (project/squad/agent)
// exists in the workspace. Returns ok=false and writes a 400 when it does not.
func (h *Handler) validateScopeTarget(w http.ResponseWriter, r *http.Request, kind string, id, workspaceID pgtype.UUID) bool {
	var err error
	switch kind {
	case "project":
		_, err = h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	case "squad":
		_, err = h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	case "agent":
		_, err = h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, kind+" not found in this workspace")
		return false
	}
	return true
}

// --- Rule Group CRUD ---

func (h *Handler) ListRuleGroups(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	groups, err := h.Queries.ListRuleGroupSummariesByWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list rule groups")
		return
	}
	resp := make([]RuleGroupSummaryResponse, len(groups))
	for i, g := range groups {
		resp[i] = RuleGroupSummaryResponse{
			RuleGroupResponse: RuleGroupResponse{
				ID:          uuidToString(g.ID),
				WorkspaceID: uuidToString(g.WorkspaceID),
				Name:        g.Name,
				Description: g.Description,
				Enabled:     g.Enabled,
				SourceType:  g.SourceType,
				SourceRef:   decodeJSONObject(g.SourceRef),
				Version:     textToPtr(g.Version),
				CreatedBy:   uuidToPtr(g.CreatedBy),
				CreatedAt:   timestampToString(g.CreatedAt),
				UpdatedAt:   timestampToString(g.UpdatedAt),
			},
			RuleCount:    g.RuleCount,
			BindingCount: g.BindingCount,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetRuleGroup(w http.ResponseWriter, r *http.Request) {
	group, ok := h.loadRuleGroupForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rules, err := h.Queries.ListRuleGroupRules(r.Context(), group.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list rules")
		return
	}
	ruleResps := make([]RuleGroupRuleResponse, len(rules))
	for i, rule := range rules {
		ruleResps[i] = ruleGroupRuleToResponse(rule)
	}
	writeJSON(w, http.StatusOK, RuleGroupWithRulesResponse{
		RuleGroupResponse: ruleGroupToResponse(group),
		Rules:             ruleResps,
	})
}

func (h *Handler) CreateRuleGroup(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.authorizeRuleGroupMutation(w, r, workspaceID)
	if !ok {
		return
	}
	var req CreateRuleGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	params := db.CreateRuleGroupParams{
		WorkspaceID: parseUUID(workspaceID),
		Name:        sanitizeNullBytes(req.Name),
		Description: sanitizeNullBytes(req.Description),
		Enabled:     req.Enabled == nil || *req.Enabled,
		SourceType:  "manual",
		SourceRef:   []byte("{}"),
		CreatedBy:   member.UserID,
	}
	if req.Version != nil {
		params.Version = pgtype.Text{String: *req.Version, Valid: true}
	}
	group, err := h.Queries.CreateRuleGroup(r.Context(), params)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a rule group with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create rule group")
		return
	}
	writeJSON(w, http.StatusCreated, ruleGroupToResponse(group))
}

func (h *Handler) UpdateRuleGroup(w http.ResponseWriter, r *http.Request) {
	group, ok := h.loadRuleGroupForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.authorizeRuleGroupMutation(w, r, uuidToString(group.WorkspaceID)); !ok {
		return
	}
	var req UpdateRuleGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if group.SourceType == ruleGroupSourceBuiltin && (req.Name != nil || req.Description != nil || req.Version != nil) {
		writeError(w, http.StatusForbidden, "builtin rule groups are managed by the platform; only enable/disable is editable")
		return
	}
	params := db.UpdateRuleGroupParams{ID: group.ID, WorkspaceID: group.WorkspaceID}
	if req.Name != nil {
		params.Name = pgtype.Text{String: sanitizeNullBytes(*req.Name), Valid: true}
	}
	if req.Description != nil {
		params.Description = pgtype.Text{String: sanitizeNullBytes(*req.Description), Valid: true}
	}
	if req.Enabled != nil {
		params.Enabled = pgtype.Bool{Bool: *req.Enabled, Valid: true}
	}
	if req.Version != nil {
		params.Version = pgtype.Text{String: *req.Version, Valid: true}
	}
	updated, err := h.Queries.UpdateRuleGroup(r.Context(), params)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a rule group with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update rule group")
		return
	}
	writeJSON(w, http.StatusOK, ruleGroupToResponse(updated))
}

func (h *Handler) DeleteRuleGroup(w http.ResponseWriter, r *http.Request) {
	group, ok := h.loadRuleGroupForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.authorizeRuleGroupMutation(w, r, uuidToString(group.WorkspaceID)); !ok {
		return
	}
	if rejectBuiltinMutation(w, group, "delete") {
		return
	}
	if err := h.Queries.DeleteRuleGroup(r.Context(), db.DeleteRuleGroupParams{
		ID:          group.ID,
		WorkspaceID: group.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete rule group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Rule CRUD ---

func (h *Handler) ListRuleGroupRules(w http.ResponseWriter, r *http.Request) {
	group, ok := h.loadRuleGroupForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rules, err := h.Queries.ListRuleGroupRules(r.Context(), group.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list rules")
		return
	}
	resp := make([]RuleGroupRuleResponse, len(rules))
	for i, rule := range rules {
		resp[i] = ruleGroupRuleToResponse(rule)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CreateRuleGroupRule(w http.ResponseWriter, r *http.Request) {
	group, ok := h.loadRuleGroupForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.authorizeRuleGroupMutation(w, r, uuidToString(group.WorkspaceID)); !ok {
		return
	}
	if rejectBuiltinMutation(w, group, "adding rules") {
		return
	}
	var req CreateRuleGroupRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	params := db.CreateRuleGroupRuleParams{
		WorkspaceID:  group.WorkspaceID,
		RuleGroupID:  group.ID,
		Name:         sanitizeNullBytes(req.Name),
		Description:  sanitizeNullBytes(req.Description),
		Content:      sanitizeNullBytes(req.Content),
		SortOrder:    req.SortOrder,
		Enabled:      req.Enabled == nil || *req.Enabled,
		Tags:         normalizeTags(req.Tags),
		RuntimeHints: marshalJSONObject(req.RuntimeHints),
	}
	if req.FileName != nil && *req.FileName != "" {
		params.FileName = pgtype.Text{String: sanitizeNullBytes(*req.FileName), Valid: true}
	}
	rule, err := h.Queries.CreateRuleGroupRule(r.Context(), params)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a rule with this name or file_name already exists in this group")
			return
		}
		if isCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "name and content must not be empty")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create rule")
		return
	}
	writeJSON(w, http.StatusCreated, ruleGroupRuleToResponse(rule))
}

func (h *Handler) UpdateRuleGroupRule(w http.ResponseWriter, r *http.Request) {
	group, ok := h.loadRuleGroupForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.authorizeRuleGroupMutation(w, r, uuidToString(group.WorkspaceID)); !ok {
		return
	}
	ruleUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "ruleId"), "rule id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetRuleGroupRuleInGroup(r.Context(), db.GetRuleGroupRuleInGroupParams{
		ID:          ruleUUID,
		RuleGroupID: group.ID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	var req UpdateRuleGroupRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if group.SourceType == ruleGroupSourceBuiltin &&
		(req.Name != nil || req.Description != nil || req.Content != nil || req.SortOrder != nil ||
			req.FileName != nil || req.Tags != nil || req.RuntimeHints != nil) {
		writeError(w, http.StatusForbidden, "builtin rule groups are managed by the platform; only enable/disable is editable")
		return
	}
	params := db.UpdateRuleGroupRuleParams{ID: ruleUUID, RuleGroupID: group.ID}
	if req.Name != nil {
		params.Name = pgtype.Text{String: sanitizeNullBytes(*req.Name), Valid: true}
	}
	if req.Description != nil {
		params.Description = pgtype.Text{String: sanitizeNullBytes(*req.Description), Valid: true}
	}
	if req.Content != nil {
		params.Content = pgtype.Text{String: sanitizeNullBytes(*req.Content), Valid: true}
	}
	if req.SortOrder != nil {
		params.SortOrder = pgtype.Int4{Int32: *req.SortOrder, Valid: true}
	}
	if req.Enabled != nil {
		params.Enabled = pgtype.Bool{Bool: *req.Enabled, Valid: true}
	}
	if req.FileName != nil {
		params.FileName = pgtype.Text{String: sanitizeNullBytes(*req.FileName), Valid: true}
	}
	if req.Tags != nil {
		params.Tags = normalizeTags(req.Tags)
	}
	if req.RuntimeHints != nil {
		params.RuntimeHints = marshalJSONObject(req.RuntimeHints)
	}
	rule, err := h.Queries.UpdateRuleGroupRule(r.Context(), params)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a rule with this name or file_name already exists in this group")
			return
		}
		if isCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "name and content must not be empty")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update rule")
		return
	}
	writeJSON(w, http.StatusOK, ruleGroupRuleToResponse(rule))
}

func (h *Handler) DeleteRuleGroupRule(w http.ResponseWriter, r *http.Request) {
	group, ok := h.loadRuleGroupForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.authorizeRuleGroupMutation(w, r, uuidToString(group.WorkspaceID)); !ok {
		return
	}
	if rejectBuiltinMutation(w, group, "deleting rules") {
		return
	}
	ruleUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "ruleId"), "rule id")
	if !ok {
		return
	}
	rule, err := h.Queries.GetRuleGroupRuleInGroup(r.Context(), db.GetRuleGroupRuleInGroupParams{
		ID:          ruleUUID,
		RuleGroupID: group.ID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err := h.Queries.DeleteRuleGroupRule(r.Context(), db.DeleteRuleGroupRuleParams{
		ID:          rule.ID,
		RuleGroupID: group.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete rule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Binding CRUD ---

func (h *Handler) ListRuleGroupBindings(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	bindings, err := h.Queries.ListRuleGroupBindingsByWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list bindings")
		return
	}

	// Optional scope filter: ?scope_type=project&scope_id=<uuid>.
	scopeType := r.URL.Query().Get("scope_type")
	scopeID := r.URL.Query().Get("scope_id")

	resp := make([]RuleGroupBindingResponse, 0, len(bindings))
	for _, b := range bindings {
		br := ruleGroupBindingToResponse(db.RuleGroupBinding{
			ID:          b.ID,
			WorkspaceID: b.WorkspaceID,
			RuleGroupID: b.RuleGroupID,
			ProjectID:   b.ProjectID,
			SquadID:     b.SquadID,
			AgentID:     b.AgentID,
			Enabled:     b.Enabled,
			SortOrder:   b.SortOrder,
			CreatedBy:   b.CreatedBy,
			CreatedAt:   b.CreatedAt,
			UpdatedAt:   b.UpdatedAt,
		}, b.RuleGroupName)
		if scopeType != "" && br.ScopeType != scopeType {
			continue
		}
		if scopeID != "" && (br.ScopeID == nil || *br.ScopeID != scopeID) {
			continue
		}
		resp = append(resp, br)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CreateRuleGroupBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.authorizeRuleGroupMutation(w, r, workspaceID)
	if !ok {
		return
	}
	workspaceUUID := parseUUID(workspaceID)

	var req CreateRuleGroupBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	groupUUID, ok := parseUUIDOrBadRequest(w, req.RuleGroupID, "rule_group_id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetRuleGroupInWorkspace(r.Context(), db.GetRuleGroupInWorkspaceParams{
		ID:          groupUUID,
		WorkspaceID: workspaceUUID,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "rule group not found in this workspace")
		return
	}

	params := db.CreateRuleGroupBindingParams{
		WorkspaceID: workspaceUUID,
		RuleGroupID: groupUUID,
		Enabled:     req.Enabled == nil || *req.Enabled,
		SortOrder:   req.SortOrder,
		CreatedBy:   member.UserID,
	}

	switch req.ScopeType {
	case "workspace":
		if req.ScopeID != nil && *req.ScopeID != "" {
			writeError(w, http.StatusBadRequest, "scope_id must be empty for workspace scope")
			return
		}
	case "project", "squad", "agent":
		if req.ScopeID == nil || *req.ScopeID == "" {
			writeError(w, http.StatusBadRequest, "scope_id is required for "+req.ScopeType+" scope")
			return
		}
		scopeUUID, ok := parseUUIDOrBadRequest(w, *req.ScopeID, "scope_id")
		if !ok {
			return
		}
		if !h.validateScopeTarget(w, r, req.ScopeType, scopeUUID, workspaceUUID) {
			return
		}
		switch req.ScopeType {
		case "project":
			params.ProjectID = scopeUUID
		case "squad":
			params.SquadID = scopeUUID
		case "agent":
			params.AgentID = scopeUUID
		}
	default:
		writeError(w, http.StatusBadRequest, "scope_type must be one of workspace, project, squad, agent")
		return
	}

	binding, err := h.Queries.CreateRuleGroupBinding(r.Context(), params)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "this rule group is already bound to this scope")
			return
		}
		if isCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "scope target is not in this workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create binding")
		return
	}
	writeJSON(w, http.StatusCreated, ruleGroupBindingToResponse(binding, ""))
}

func (h *Handler) UpdateRuleGroupBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.authorizeRuleGroupMutation(w, r, workspaceID); !ok {
		return
	}
	bindingUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "binding id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetRuleGroupBindingInWorkspace(r.Context(), db.GetRuleGroupBindingInWorkspaceParams{
		ID:          bindingUUID,
		WorkspaceID: parseUUID(workspaceID),
	}); err != nil {
		writeError(w, http.StatusNotFound, "binding not found")
		return
	}
	var req UpdateRuleGroupBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	params := db.UpdateRuleGroupBindingParams{ID: bindingUUID, WorkspaceID: parseUUID(workspaceID)}
	if req.Enabled != nil {
		params.Enabled = pgtype.Bool{Bool: *req.Enabled, Valid: true}
	}
	if req.SortOrder != nil {
		params.SortOrder = pgtype.Int4{Int32: *req.SortOrder, Valid: true}
	}
	binding, err := h.Queries.UpdateRuleGroupBinding(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update binding")
		return
	}
	writeJSON(w, http.StatusOK, ruleGroupBindingToResponse(binding, ""))
}

func (h *Handler) DeleteRuleGroupBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.authorizeRuleGroupMutation(w, r, workspaceID); !ok {
		return
	}
	bindingUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "binding id")
	if !ok {
		return
	}
	binding, err := h.Queries.GetRuleGroupBindingInWorkspace(r.Context(), db.GetRuleGroupBindingInWorkspaceParams{
		ID:          bindingUUID,
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "binding not found")
		return
	}
	if err := h.Queries.DeleteRuleGroupBinding(r.Context(), db.DeleteRuleGroupBindingParams{
		ID:          binding.ID,
		WorkspaceID: binding.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete binding")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Effective rules ---

func (h *Handler) GetEffectiveRules(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	workspaceUUID := parseUUID(workspaceID)

	projectID, ok := h.optionalScopeID(w, r, r.URL.Query().Get("project_id"), "project", workspaceUUID)
	if !ok {
		return
	}
	squadID, ok := h.optionalScopeID(w, r, r.URL.Query().Get("squad_id"), "squad", workspaceUUID)
	if !ok {
		return
	}
	agentID, ok := h.optionalScopeID(w, r, r.URL.Query().Get("agent_id"), "agent", workspaceUUID)
	if !ok {
		return
	}

	rows, err := h.Queries.ListEffectiveRules(r.Context(), db.ListEffectiveRulesParams{
		WorkspaceID: workspaceUUID,
		ProjectID:   projectID,
		SquadID:     squadID,
		AgentID:     agentID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to assemble effective rules")
		return
	}

	resp := EffectiveRulesResponse{
		WorkspaceID: workspaceID,
		Inputs: EffectiveRulesInputs{
			ProjectID: uuidToPtr(projectID),
			SquadID:   uuidToPtr(squadID),
			AgentID:   uuidToPtr(agentID),
		},
		Layers: []EffectiveRuleLayer{},
		Rules:  []EffectiveRule{},
	}

	scopeIDForType := map[string]*string{
		"workspace": nil,
		"project":   uuidToPtr(projectID),
		"squad":     uuidToPtr(squadID),
		"agent":     uuidToPtr(agentID),
	}

	// Rows arrive ordered by scope layer, then binding/group/rule order, so we
	// can build the flattened rule list and the per-scope layer/group view in a
	// single pass.
	layerIndex := map[string]int{}
	groupIndex := map[string]int{} // key: scope_type + "/" + binding_id
	for _, row := range rows {
		resp.Rules = append(resp.Rules, EffectiveRule{
			ID:            uuidToString(row.RuleID),
			RuleGroupID:   uuidToString(row.RuleGroupID),
			RuleGroupName: row.RuleGroupName,
			ScopeType:     row.ScopeType,
			Name:          row.RuleName,
			Description:   row.RuleDescription,
			Content:       row.RuleContent,
			SortOrder:     row.RuleSortOrder,
			FileName:      textToPtr(row.RuleFileName),
			RuntimeHints:  decodeJSONObject(row.RuleRuntimeHints),
		})

		li, ok := layerIndex[row.ScopeType]
		if !ok {
			li = len(resp.Layers)
			layerIndex[row.ScopeType] = li
			resp.Layers = append(resp.Layers, EffectiveRuleLayer{
				ScopeType: row.ScopeType,
				ScopeID:   scopeIDForType[row.ScopeType],
				Groups:    []EffectiveRuleLayerGroup{},
			})
		}
		gKey := row.ScopeType + "/" + uuidToString(row.BindingID)
		gi, ok := groupIndex[gKey]
		if !ok {
			gi = len(resp.Layers[li].Groups)
			groupIndex[gKey] = gi
			resp.Layers[li].Groups = append(resp.Layers[li].Groups, EffectiveRuleLayerGroup{
				BindingID:   uuidToString(row.BindingID),
				RuleGroupID: uuidToString(row.RuleGroupID),
				Name:        row.RuleGroupName,
				RuleCount:   0,
			})
		}
		resp.Layers[li].Groups[gi].RuleCount++
	}

	writeJSON(w, http.StatusOK, resp)
}

// optionalScopeID parses an optional scope id query param and, when present,
// validates it belongs to the workspace. Returns (invalid UUID, true) when the
// param is absent. Returns ok=false (after writing a 400) on a malformed or
// foreign id.
func (h *Handler) optionalScopeID(w http.ResponseWriter, r *http.Request, raw, kind string, workspaceUUID pgtype.UUID) (pgtype.UUID, bool) {
	if raw == "" {
		return pgtype.UUID{}, true
	}
	id, ok := parseUUIDOrBadRequest(w, raw, kind+"_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	if !h.validateScopeTarget(w, r, kind, id, workspaceUUID) {
		return pgtype.UUID{}, false
	}
	return id, true
}

// normalizeTags ensures a non-nil slice so the NOT NULL text[] column never
// receives a SQL NULL on insert.
func normalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// marshalJSONObject serializes an arbitrary JSON value for a JSONB column,
// defaulting to an empty object when absent or unserializable.
func marshalJSONObject(v any) []byte {
	if v == nil {
		return []byte("{}")
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}
