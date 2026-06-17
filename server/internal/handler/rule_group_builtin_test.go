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

func TestBuiltinRuleGroupProtectedFromMutation(t *testing.T) {
	ctx := context.Background()
	// Seed into the shared test workspace so the owner (testUserID) passes the
	// mutation authorization check; clean up the builtin rows afterwards.
	if err := builtinrulegroups.EnsureBuiltinRuleGroups(ctx, testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM rule_group WHERE workspace_id = $1 AND source_type = 'builtin'`, testWorkspaceID)
	})

	var coreID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM rule_group WHERE workspace_id = $1 AND name = '1C Core'`, testWorkspaceID,
	).Scan(&coreID); err != nil {
		t.Fatalf("lookup 1C Core: %v", err)
	}

	// Delete is forbidden.
	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/rule-groups/"+coreID, nil)
	req = withURLParam(req, "id", coreID)
	testHandler.DeleteRuleGroup(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("DeleteRuleGroup builtin: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// Rename is forbidden.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/rule-groups/"+coreID, map[string]any{"name": "Hacked"})
	req = withURLParam(req, "id", coreID)
	testHandler.UpdateRuleGroup(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("rename builtin: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// Enable/disable is allowed.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/rule-groups/"+coreID, map[string]any{"enabled": false})
	req = withURLParam(req, "id", coreID)
	testHandler.UpdateRuleGroup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("toggle builtin enabled: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
