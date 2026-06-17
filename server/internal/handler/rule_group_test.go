package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- helpers ---

func ruleGroupSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// createRuleGroupViaAPI drives POST /api/rule-groups and registers cleanup
// (which cascades to the group's rules and bindings).
func createRuleGroupViaAPI(t *testing.T, name string) RuleGroupResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/rule-groups?workspace_id="+testWorkspaceID, map[string]any{
		"name": name,
	})
	testHandler.CreateRuleGroup(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateRuleGroup: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var group RuleGroupResponse
	if err := json.NewDecoder(w.Body).Decode(&group); err != nil {
		t.Fatalf("decode rule group: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM rule_group WHERE id = $1`, group.ID)
	})
	return group
}

// createRuleViaAPI drives POST /api/rule-groups/{id}/rules.
func createRuleViaAPI(t *testing.T, groupID string, body map[string]any) (*httptest.ResponseRecorder, RuleGroupRuleResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/rule-groups/"+groupID+"/rules", body)
	req = withURLParam(req, "id", groupID)
	testHandler.CreateRuleGroupRule(w, req)
	var rule RuleGroupRuleResponse
	if w.Code == http.StatusCreated {
		json.NewDecoder(w.Body).Decode(&rule)
	}
	return w, rule
}

func bindRuleGroupViaAPI(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/rule-group-bindings?workspace_id="+testWorkspaceID, body)
	testHandler.CreateRuleGroupBinding(w, req)
	return w
}

func createProjectInWorkspace(t *testing.T, workspaceID, title string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		workspaceID, title,
	).Scan(&id); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, id)
	})
	return id
}

func createSquadInWorkspace(t *testing.T, workspaceID, name, leaderAgentID string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO squad (workspace_id, name, leader_id, creator_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		workspaceID, name, leaderAgentID, testUserID,
	).Scan(&id); err != nil {
		t.Fatalf("insert squad: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, id)
	})
	return id
}

// --- tests ---

func TestRuleGroupCRUD(t *testing.T) {
	suffix := ruleGroupSuffix()
	group := createRuleGroupViaAPI(t, "Runtime Hygiene "+suffix)
	if group.SourceType != "manual" || !group.Enabled {
		t.Fatalf("expected manual+enabled group, got %+v", group)
	}

	// Detail with no rules yet.
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/rule-groups/"+group.ID, nil)
	req = withURLParam(req, "id", group.ID)
	testHandler.GetRuleGroup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetRuleGroup: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var detail RuleGroupWithRulesResponse
	json.NewDecoder(w.Body).Decode(&detail)
	if len(detail.Rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(detail.Rules))
	}

	// Add two markdown rules.
	for i, name := range []string{"Completion evidence", "No secrets in logs"} {
		recorder, rule := createRuleViaAPI(t, group.ID, map[string]any{
			"name":       name,
			"content":    "# " + name + "\nmarkdown body",
			"sort_order": i * 10,
		})
		if recorder.Code != http.StatusCreated {
			t.Fatalf("CreateRule %q: expected 201, got %d: %s", name, recorder.Code, recorder.Body.String())
		}
		if rule.Name != name {
			t.Fatalf("rule name = %q, want %q", rule.Name, name)
		}
	}

	// Detail now lists both rules in sort order; list endpoint counts them.
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/rule-groups/"+group.ID, nil)
	req = withURLParam(req, "id", group.ID)
	testHandler.GetRuleGroup(w, req)
	json.NewDecoder(w.Body).Decode(&detail)
	if len(detail.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(detail.Rules))
	}
	if detail.Rules[0].Name != "Completion evidence" {
		t.Fatalf("rules not in sort order: %+v", detail.Rules)
	}

	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/rule-groups?workspace_id="+testWorkspaceID, nil)
	testHandler.ListRuleGroups(w, req)
	var summaries []RuleGroupSummaryResponse
	json.NewDecoder(w.Body).Decode(&summaries)
	found := false
	for _, s := range summaries {
		if s.ID == group.ID {
			found = true
			if s.RuleCount != 2 {
				t.Fatalf("summary rule_count = %d, want 2", s.RuleCount)
			}
		}
	}
	if !found {
		t.Fatal("created group missing from list")
	}

	// Update name.
	w = httptest.NewRecorder()
	newName := "Renamed Hygiene " + suffix
	req = newRequest("PUT", "/api/rule-groups/"+group.ID, map[string]any{"name": newName})
	req = withURLParam(req, "id", group.ID)
	testHandler.UpdateRuleGroup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateRuleGroup: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated RuleGroupResponse
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Name != newName {
		t.Fatalf("update name = %q, want %q", updated.Name, newName)
	}

	// Delete cascades.
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/rule-groups/"+group.ID, nil)
	req = withURLParam(req, "id", group.ID)
	testHandler.DeleteRuleGroup(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteRuleGroup: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/rule-groups/"+group.ID, nil)
	req = withURLParam(req, "id", group.ID)
	testHandler.GetRuleGroup(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetRuleGroup after delete: expected 404, got %d", w.Code)
	}
}

func TestRuleGroupDuplicateNameRejected(t *testing.T) {
	name := "Duplicate Group " + ruleGroupSuffix()
	createRuleGroupViaAPI(t, name)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/rule-groups?workspace_id="+testWorkspaceID, map[string]any{"name": name})
	testHandler.CreateRuleGroup(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate group name: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRuleGroupRuleDuplicatesRejected(t *testing.T) {
	group := createRuleGroupViaAPI(t, "Rule Dup Group "+ruleGroupSuffix())

	first, _ := createRuleViaAPI(t, group.ID, map[string]any{
		"name": "shared-name", "content": "body", "file_name": "shared.md",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first rule: expected 201, got %d: %s", first.Code, first.Body.String())
	}

	dupName, _ := createRuleViaAPI(t, group.ID, map[string]any{
		"name": "shared-name", "content": "other body",
	})
	if dupName.Code != http.StatusConflict {
		t.Fatalf("duplicate rule name: expected 409, got %d: %s", dupName.Code, dupName.Body.String())
	}

	dupFile, _ := createRuleViaAPI(t, group.ID, map[string]any{
		"name": "different-name", "content": "body", "file_name": "shared.md",
	})
	if dupFile.Code != http.StatusConflict {
		t.Fatalf("duplicate file_name: expected 409, got %d: %s", dupFile.Code, dupFile.Body.String())
	}
}

func TestRuleGroupRuleRequiresContent(t *testing.T) {
	group := createRuleGroupViaAPI(t, "Empty Content Group "+ruleGroupSuffix())
	w, _ := createRuleViaAPI(t, group.ID, map[string]any{"name": "no-content", "content": ""})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty content: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRuleGroupBindingDuplicateRejected(t *testing.T) {
	group := createRuleGroupViaAPI(t, "Binding Dup Group "+ruleGroupSuffix())

	first := bindRuleGroupViaAPI(t, map[string]any{
		"rule_group_id": group.ID,
		"scope_type":    "workspace",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first binding: expected 201, got %d: %s", first.Code, first.Body.String())
	}

	dup := bindRuleGroupViaAPI(t, map[string]any{
		"rule_group_id": group.ID,
		"scope_type":    "workspace",
	})
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate binding: expected 409, got %d: %s", dup.Code, dup.Body.String())
	}
}

func TestRuleGroupBindingRejectsCrossWorkspaceScope(t *testing.T) {
	ctx := context.Background()
	group := createRuleGroupViaAPI(t, "Cross WS Group "+ruleGroupSuffix())

	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Rule cross-ws", "rule-cross-ws-"+ruleGroupSuffix(), "Foreign workspace", "RCW").Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})
	foreignProjectID := createProjectInWorkspace(t, otherWorkspaceID, "Foreign project")

	w := bindRuleGroupViaAPI(t, map[string]any{
		"rule_group_id": group.ID,
		"scope_type":    "project",
		"scope_id":      foreignProjectID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-workspace binding: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM rule_group_binding WHERE rule_group_id = $1`, group.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected binding still wrote a row (count=%d)", count)
	}
}

// TestRuleGroupBindingTriggerBlocksCrossWorkspace proves the DB trigger closes
// the boundary even when a write bypasses the handler validation.
func TestRuleGroupBindingTriggerBlocksCrossWorkspace(t *testing.T) {
	ctx := context.Background()
	group := createRuleGroupViaAPI(t, "Trigger Group "+ruleGroupSuffix())

	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Rule trigger ws", "rule-trigger-ws-"+ruleGroupSuffix(), "Foreign workspace", "RTW").Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})
	foreignProjectID := createProjectInWorkspace(t, otherWorkspaceID, "Foreign trigger project")

	_, err := testPool.Exec(ctx, `
		INSERT INTO rule_group_binding (workspace_id, rule_group_id, project_id)
		VALUES ($1, $2, $3)
	`, testWorkspaceID, group.ID, foreignProjectID)
	if err == nil {
		t.Fatal("expected trigger to reject cross-workspace binding insert, got nil error")
	}
}

func TestEffectiveRulesLayerOrdering(t *testing.T) {
	suffix := ruleGroupSuffix()
	agentID := createHandlerTestAgent(t, "Effective Agent "+suffix, []byte("[]"))
	projectID := createProjectInWorkspace(t, testWorkspaceID, "Effective Project "+suffix)
	squadID := createSquadInWorkspace(t, testWorkspaceID, "Effective Squad "+suffix, agentID)

	// One group per scope, each with a single rule named after its scope.
	scopes := []struct {
		scopeType string
		scopeID   string
	}{
		{"workspace", ""},
		{"project", projectID},
		{"squad", squadID},
		{"agent", agentID},
	}
	for _, sc := range scopes {
		group := createRuleGroupViaAPI(t, "EFF "+sc.scopeType+" "+suffix)
		recorder, _ := createRuleViaAPI(t, group.ID, map[string]any{
			"name":    "rule-" + sc.scopeType,
			"content": "body for " + sc.scopeType,
		})
		if recorder.Code != http.StatusCreated {
			t.Fatalf("rule for %s: %d %s", sc.scopeType, recorder.Code, recorder.Body.String())
		}
		body := map[string]any{"rule_group_id": group.ID, "scope_type": sc.scopeType}
		if sc.scopeID != "" {
			body["scope_id"] = sc.scopeID
		}
		bw := bindRuleGroupViaAPI(t, body)
		if bw.Code != http.StatusCreated {
			t.Fatalf("binding for %s: %d %s", sc.scopeType, bw.Code, bw.Body.String())
		}
	}

	w := httptest.NewRecorder()
	url := fmt.Sprintf("/api/rules/effective?workspace_id=%s&project_id=%s&squad_id=%s&agent_id=%s",
		testWorkspaceID, projectID, squadID, agentID)
	req := newRequest("GET", url, nil)
	testHandler.GetEffectiveRules(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetEffectiveRules: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp EffectiveRulesResponse
	json.NewDecoder(w.Body).Decode(&resp)

	want := []string{"workspace", "project", "squad", "agent"}
	var got []string
	for _, rule := range resp.Rules {
		if rule.Name == "rule-workspace" || rule.Name == "rule-project" ||
			rule.Name == "rule-squad" || rule.Name == "rule-agent" {
			got = append(got, rule.ScopeType)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d effective rules, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("effective rule order = %v, want %v", got, want)
		}
	}
}

func TestEffectiveRulesExcludesDisabled(t *testing.T) {
	suffix := ruleGroupSuffix()

	// Enabled group, enabled rule -> included.
	live := createRuleGroupViaAPI(t, "EFF live "+suffix)
	if r, _ := createRuleViaAPI(t, live.ID, map[string]any{"name": "live-rule", "content": "x"}); r.Code != http.StatusCreated {
		t.Fatalf("live rule: %d %s", r.Code, r.Body.String())
	}
	if bw := bindRuleGroupViaAPI(t, map[string]any{"rule_group_id": live.ID, "scope_type": "workspace"}); bw.Code != http.StatusCreated {
		t.Fatalf("live binding: %d %s", bw.Code, bw.Body.String())
	}

	// Disabled group -> excluded entirely.
	disabledGroup := createRuleGroupViaAPI(t, "EFF disabled-group "+suffix)
	if r, _ := createRuleViaAPI(t, disabledGroup.ID, map[string]any{"name": "hidden-by-group", "content": "x"}); r.Code != http.StatusCreated {
		t.Fatalf("hidden rule: %d %s", r.Code, r.Body.String())
	}
	if bw := bindRuleGroupViaAPI(t, map[string]any{"rule_group_id": disabledGroup.ID, "scope_type": "workspace"}); bw.Code != http.StatusCreated {
		t.Fatalf("disabled-group binding: %d %s", bw.Code, bw.Body.String())
	}
	dw := httptest.NewRecorder()
	dreq := newRequest("PUT", "/api/rule-groups/"+disabledGroup.ID, map[string]any{"enabled": false})
	dreq = withURLParam(dreq, "id", disabledGroup.ID)
	testHandler.UpdateRuleGroup(dw, dreq)
	if dw.Code != http.StatusOK {
		t.Fatalf("disable group: %d %s", dw.Code, dw.Body.String())
	}

	// Enabled group with a disabled rule -> that rule excluded.
	mixed := createRuleGroupViaAPI(t, "EFF mixed "+suffix)
	_, disabledRule := createRuleViaAPI(t, mixed.ID, map[string]any{"name": "hidden-by-rule", "content": "x", "enabled": false})
	if bw := bindRuleGroupViaAPI(t, map[string]any{"rule_group_id": mixed.ID, "scope_type": "workspace"}); bw.Code != http.StatusCreated {
		t.Fatalf("mixed binding: %d %s", bw.Code, bw.Body.String())
	}
	_ = disabledRule

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/rules/effective?workspace_id="+testWorkspaceID, nil)
	testHandler.GetEffectiveRules(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetEffectiveRules: %d %s", w.Code, w.Body.String())
	}
	var resp EffectiveRulesResponse
	json.NewDecoder(w.Body).Decode(&resp)

	names := map[string]bool{}
	for _, rule := range resp.Rules {
		names[rule.Name] = true
	}
	if !names["live-rule"] {
		t.Fatal("expected live-rule in effective output")
	}
	if names["hidden-by-group"] {
		t.Fatal("rule from disabled group must not appear")
	}
	if names["hidden-by-rule"] {
		t.Fatal("disabled rule must not appear")
	}
}

func TestRuleGroupMutationsRejectAgentActor(t *testing.T) {
	// A task-token actor (X-Actor-Source=task_token) must be blocked from
	// creating rule groups even though its token authenticates as the owner.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/rule-groups?workspace_id="+testWorkspaceID, map[string]any{
		"name": "Agent attempt " + ruleGroupSuffix(),
	})
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", "3a589638-8c13-454d-b07d-0ef091cb1819")
	testHandler.CreateRuleGroup(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("agent actor create: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
