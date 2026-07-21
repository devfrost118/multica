package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	builtinrulegroups "github.com/multica-ai/multica/server/internal/service/builtin_rule_groups"
)

// createTempWorkspace inserts an isolated workspace so a seed run cannot disturb
// the shared test fixture, and registers cascade cleanup.
func createTempWorkspace(t *testing.T) string {
	t.Helper()
	suffix := ruleGroupSuffix()
	var id string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO workspace (name, slug, issue_prefix) VALUES ($1, $2, $3) RETURNING id`,
		"Builtin Seed WS "+suffix, "builtin-seed-"+suffix, "BSW",
	).Scan(&id); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, id)
	})
	return id
}

func TestEnsureBuiltinRuleGroupsIdempotent(t *testing.T) {
	ctx := context.Background()
	wsID := createTempWorkspace(t)
	wsUUID := parseUUID(wsID)

	// Two seed runs must leave exactly the catalog's worth of builtin rows.
	for i := 0; i < 2; i++ {
		if err := builtinrulegroups.EnsureBuiltinRuleGroups(ctx, testHandler.Queries, wsUUID); err != nil {
			t.Fatalf("seed run %d: %v", i, err)
		}
	}

	catalog := builtinrulegroups.Catalog()
	wantGroups := len(catalog)
	wantRules := 0
	for _, g := range catalog {
		wantRules += len(g.Rules)
	}

	var gotGroups int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM rule_group WHERE workspace_id = $1 AND source_type = 'builtin'`,
		wsID,
	).Scan(&gotGroups); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if gotGroups != wantGroups {
		t.Fatalf("builtin group count = %d, want %d (seeding not idempotent)", gotGroups, wantGroups)
	}

	var gotRules int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM rule_group_rule r
		 JOIN rule_group g ON g.id = r.rule_group_id
		 WHERE g.workspace_id = $1 AND g.source_type = 'builtin'`,
		wsID,
	).Scan(&gotRules); err != nil {
		t.Fatalf("count rules: %v", err)
	}
	if gotRules != wantRules {
		t.Fatalf("builtin rule count = %d, want %d (seeding not idempotent)", gotRules, wantRules)
	}

	// The starter set required by the AC must be present and bindable.
	for _, name := range []string{"1C Core", "1C Developer", "1C Architect", "1C Reviewer"} {
		var n int
		if err := testPool.QueryRow(ctx,
			`SELECT count(*) FROM rule_group WHERE workspace_id = $1 AND name = $2 AND source_type = 'builtin'`,
			wsID, name,
		).Scan(&n); err != nil {
			t.Fatalf("lookup %q: %v", name, err)
		}
		if n != 1 {
			t.Errorf("builtin group %q: found %d, want 1", name, n)
		}
	}
}

func TestEnsureBuiltinRuleGroupsDoesNotClobberManual(t *testing.T) {
	ctx := context.Background()
	wsID := createTempWorkspace(t)
	wsUUID := parseUUID(wsID)

	// A user-authored group that happens to share a builtin name must survive
	// seeding untouched (source_type and description preserved).
	var manualID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO rule_group (workspace_id, name, description, source_type)
		 VALUES ($1, '1C Core', 'mine', 'manual') RETURNING id`,
		wsID,
	).Scan(&manualID); err != nil {
		t.Fatalf("insert manual group: %v", err)
	}

	if err := builtinrulegroups.EnsureBuiltinRuleGroups(ctx, testHandler.Queries, wsUUID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var sourceType, description string
	if err := testPool.QueryRow(ctx,
		`SELECT source_type, description FROM rule_group WHERE id = $1`, manualID,
	).Scan(&sourceType, &description); err != nil {
		t.Fatalf("reload manual group: %v", err)
	}
	if sourceType != "manual" || description != "mine" {
		t.Fatalf("manual group clobbered: source_type=%q description=%q", sourceType, description)
	}
}

// lookupRuleGroupID resolves a seeded group's id by its catalog name within the
// shared test workspace.
func lookupRuleGroupID(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM rule_group WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, name,
	).Scan(&id); err != nil {
		t.Fatalf("lookup rule group %q: %v", name, err)
	}
	return id
}

func assertRuleGroupSourceType(t *testing.T, ctx context.Context, id, want string) {
	t.Helper()
	var got string
	if err := testPool.QueryRow(ctx, `SELECT source_type FROM rule_group WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("load source_type for %s: %v", id, err)
	}
	if got != want {
		t.Fatalf("source_type = %q, want %q", got, want)
	}
}

// cleanupSeededGroups removes the seeded groups by their stable catalog names.
// Matching by name (not source_type) is required because a group adopted during
// the test is now source_type='manual'; renaming also frees the original name so
// a fresh builtin copy can reappear after a re-seed.
func cleanupSeededGroups(t *testing.T, ctx context.Context, extraNames ...string) {
	t.Cleanup(func() {
		for _, g := range builtinrulegroups.Catalog() {
			testPool.Exec(ctx, `DELETE FROM rule_group WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, g.Name)
		}
		for _, name := range extraNames {
			testPool.Exec(ctx, `DELETE FROM rule_group WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, name)
		}
	})
}

// TestBuiltinRuleGroupAdoptedOnEdit is the inverse of the old "protected from
// mutation" test: a structural edit of a builtin group is no longer rejected.
// Instead the group is adopted (source_type builtin -> manual) so the edit
// survives the next catalog backfill. A pure enable/disable toggle still leaves
// the group builtin.
func TestBuiltinRuleGroupAdoptedOnEdit(t *testing.T) {
	ctx := context.Background()
	// Seed into the shared test workspace so the owner (testUserID) passes the
	// mutation authorization check.
	if err := builtinrulegroups.EnsureBuiltinRuleGroups(ctx, testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cleanupSeededGroups(t, ctx, "Adopted Core")

	coreID := lookupRuleGroupID(t, ctx, "1C Core")

	// Enable/disable is a plain toggle: allowed, and the group stays builtin.
	dw := httptest.NewRecorder()
	dreq := newRequest("PUT", "/api/rule-groups/"+coreID, map[string]any{"enabled": false})
	dreq = withURLParam(dreq, "id", coreID)
	testHandler.UpdateRuleGroup(dw, dreq)
	if dw.Code != http.StatusOK {
		t.Fatalf("toggle builtin enabled: expected 200, got %d: %s", dw.Code, dw.Body.String())
	}
	assertRuleGroupSourceType(t, ctx, coreID, "builtin")

	// A structural edit (rename) now succeeds and adopts the group to manual.
	rw := httptest.NewRecorder()
	rreq := newRequest("PUT", "/api/rule-groups/"+coreID, map[string]any{"name": "Adopted Core"})
	rreq = withURLParam(rreq, "id", coreID)
	testHandler.UpdateRuleGroup(rw, rreq)
	if rw.Code != http.StatusOK {
		t.Fatalf("rename builtin: expected 200, got %d: %s", rw.Code, rw.Body.String())
	}
	assertRuleGroupSourceType(t, ctx, coreID, "manual")

	// Re-seeding must not revert the adopted group: the seeder skips manual rows.
	if err := builtinrulegroups.EnsureBuiltinRuleGroups(ctx, testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	assertRuleGroupSourceType(t, ctx, coreID, "manual")
	var name string
	if err := testPool.QueryRow(ctx, `SELECT name FROM rule_group WHERE id = $1`, coreID).Scan(&name); err != nil {
		t.Fatalf("reload adopted group: %v", err)
	}
	if name != "Adopted Core" {
		t.Fatalf("adopted group reverted by reseed: name=%q, want %q", name, "Adopted Core")
	}

	// Deleting a builtin group is no longer blocked.
	reviewerID := lookupRuleGroupID(t, ctx, "1C Reviewer")
	delW := httptest.NewRecorder()
	delReq := newRequest("DELETE", "/api/rule-groups/"+reviewerID, nil)
	delReq = withURLParam(delReq, "id", reviewerID)
	testHandler.DeleteRuleGroup(delW, delReq)
	if delW.Code != http.StatusNoContent {
		t.Fatalf("delete builtin group: expected 204, got %d: %s", delW.Code, delW.Body.String())
	}
}

// TestBuiltinRuleGroupRuleMutationsAdopt proves the user can create / edit /
// delete rules inside builtin groups (no 403), that each structural mutation
// adopts the owning group to manual, and that none of the changes are reverted
// by a subsequent catalog re-seed.
func TestBuiltinRuleGroupRuleMutationsAdopt(t *testing.T) {
	ctx := context.Background()
	if err := builtinrulegroups.EnsureBuiltinRuleGroups(ctx, testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cleanupSeededGroups(t, ctx)

	// --- Edit a rule's content: 200, group adopts to manual. ---
	devID := lookupRuleGroupID(t, ctx, "1C Developer")
	var ruleID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM rule_group_rule WHERE rule_group_id = $1 ORDER BY sort_order ASC LIMIT 1`, devID,
	).Scan(&ruleID); err != nil {
		t.Fatalf("lookup developer rule: %v", err)
	}
	uw := httptest.NewRecorder()
	ureq := newRequest("PUT", "/api/rule-groups/"+devID+"/rules/"+ruleID, map[string]any{"content": "User-customized content"})
	ureq = withURLParams(ureq, "id", devID, "ruleId", ruleID)
	testHandler.UpdateRuleGroupRule(uw, ureq)
	if uw.Code != http.StatusOK {
		t.Fatalf("edit builtin rule: expected 200, got %d: %s", uw.Code, uw.Body.String())
	}
	assertRuleGroupSourceType(t, ctx, devID, "manual")

	// --- Add a rule to a builtin group: 201, group adopts to manual. ---
	architectID := lookupRuleGroupID(t, ctx, "1C Architect")
	cw := httptest.NewRecorder()
	creq := newRequest("POST", "/api/rule-groups/"+architectID+"/rules", map[string]any{
		"name": "user-added-rule", "content": "fresh body", "sort_order": 999,
	})
	creq = withURLParam(creq, "id", architectID)
	testHandler.CreateRuleGroupRule(cw, creq)
	if cw.Code != http.StatusCreated {
		t.Fatalf("add rule to builtin group: expected 201, got %d: %s", cw.Code, cw.Body.String())
	}
	assertRuleGroupSourceType(t, ctx, architectID, "manual")

	// --- Delete a rule from a builtin group: 204, group adopts to manual. ---
	reviewerID := lookupRuleGroupID(t, ctx, "1C Reviewer")
	var reviewerRuleID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM rule_group_rule WHERE rule_group_id = $1 ORDER BY sort_order ASC LIMIT 1`, reviewerID,
	).Scan(&reviewerRuleID); err != nil {
		t.Fatalf("lookup reviewer rule: %v", err)
	}
	dw := httptest.NewRecorder()
	dreq := newRequest("DELETE", "/api/rule-groups/"+reviewerID+"/rules/"+reviewerRuleID, nil)
	dreq = withURLParams(dreq, "id", reviewerID, "ruleId", reviewerRuleID)
	testHandler.DeleteRuleGroupRule(dw, dreq)
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete builtin rule: expected 204, got %d: %s", dw.Code, dw.Body.String())
	}
	assertRuleGroupSourceType(t, ctx, reviewerID, "manual")

	// --- Re-seed: none of the user's structural changes are reverted. ---
	if err := builtinrulegroups.EnsureBuiltinRuleGroups(ctx, testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	var content string
	if err := testPool.QueryRow(ctx, `SELECT content FROM rule_group_rule WHERE id = $1`, ruleID).Scan(&content); err != nil {
		t.Fatalf("reload edited rule: %v", err)
	}
	if content != "User-customized content" {
		t.Fatalf("rule edit reverted by reseed: content=%q", content)
	}
	var addedCount int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM rule_group_rule WHERE rule_group_id = $1 AND name = 'user-added-rule'`, architectID,
	).Scan(&addedCount); err != nil {
		t.Fatalf("count added rule: %v", err)
	}
	if addedCount != 1 {
		t.Fatalf("user-added rule dropped by reseed: count=%d", addedCount)
	}
	var deletedCount int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM rule_group_rule WHERE id = $1`, reviewerRuleID,
	).Scan(&deletedCount); err != nil {
		t.Fatalf("count deleted rule: %v", err)
	}
	if deletedCount != 0 {
		t.Fatalf("deleted builtin rule re-created by reseed: count=%d", deletedCount)
	}
}
