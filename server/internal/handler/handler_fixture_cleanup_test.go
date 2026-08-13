package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Isolated fixture IDs for teardown regression tests. Must not collide with the
// shared TestMain fixture (handlerTestEmail / handlerTestWorkspaceSlug).
const (
	teardownFlakeEmail = "handler-teardown-flake@multica.ai"
	teardownFlakeSlug  = "handler-teardown-flake"
)

// seedOrphanedSquadLeaderFixture creates a workspace+user+runtime+agent+squad
// graph that historically blocked cleanupHandlerTestFixture:
// squad.leader_id → agent ON DELETE RESTRICT prevents workspace cascade from
// removing the agent, so the subsequent user delete hits agent_owner_id_fkey.
// Deliberately does NOT register t.Cleanup — simulates panic / partial fail.
func seedOrphanedSquadLeaderFixture(t *testing.T, pool *pgxpool.Pool, email, slug string) (userID, workspaceID, agentID, squadID string) {
	t.Helper()
	ctx := context.Background()

	if err := cleanupHandlerTestFixtureIdent(ctx, pool, email, slug); err != nil {
		t.Fatalf("pre-clean isolated fixture: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Handler Teardown Fixture User", email).Scan(&userID); err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Teardown Fixture WS", slug, "isolated teardown fixture", "TDF").Scan(&workspaceID); err != nil {
		t.Fatalf("insert fixture workspace: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("insert fixture member: %v", err)
	}

	var runtimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, NULL, $2, 'cloud', $3, 'online', $4, '{}'::jsonb, $5, now())
		RETURNING id
	`, workspaceID, "Teardown Fixture Runtime", "teardown_fixture", "teardown fixture runtime", userID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert fixture runtime: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, permission_mode, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 'public_to', 1, $4)
		RETURNING id
	`, workspaceID, "Teardown Fixture Agent", runtimeID, userID).Scan(&agentID); err != nil {
		t.Fatalf("insert fixture agent: %v", err)
	}

	// archived_by is a non-cascade FK to user — must not block user delete.
	if _, err := pool.Exec(ctx, `
		UPDATE agent SET archived_at = now(), archived_by = $1 WHERE id = $2
	`, userID, agentID); err != nil {
		t.Fatalf("set agent.archived_by: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, '', $3, $4)
		RETURNING id
	`, workspaceID, fmt.Sprintf("teardown-fixture-squad-%d", time.Now().UnixNano()), agentID, userID).Scan(&squadID); err != nil {
		t.Fatalf("insert fixture squad: %v", err)
	}

	return userID, workspaceID, agentID, squadID
}

func countByID(t *testing.T, pool *pgxpool.Pool, table, id string) int {
	t.Helper()
	var n int
	// table names are package-controlled literals only.
	q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE id = $1`, table)
	if err := pool.QueryRow(context.Background(), q, id).Scan(&n); err != nil {
		t.Fatalf("count %s %s: %v", table, id, err)
	}
	return n
}

func countUserByEmail(t *testing.T, pool *pgxpool.Pool, email string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM "user" WHERE email = $1`, email,
	).Scan(&n); err != nil {
		t.Fatalf("count user %s: %v", email, err)
	}
	return n
}

func countWorkspaceBySlug(t *testing.T, pool *pgxpool.Pool, slug string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM workspace WHERE slug = $1`, slug,
	).Scan(&n); err != nil {
		t.Fatalf("count workspace %s: %v", slug, err)
	}
	return n
}

// TestCleanupHandlerTestFixture_OrphanedSquadLeader reproduces the CI flake:
// a leftover squad.leader_id RESTRICT row blocks workspace→agent cascade, then
// user delete fails on agent_owner_id_fkey. Cleanup must succeed without
// per-test t.Cleanup and without weakening FKs.
func TestCleanupHandlerTestFixture_OrphanedSquadLeader(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}

	userID, workspaceID, agentID, squadID := seedOrphanedSquadLeaderFixture(
		t, testPool, teardownFlakeEmail, teardownFlakeSlug,
	)

	if err := cleanupHandlerTestFixtureIdent(context.Background(), testPool, teardownFlakeEmail, teardownFlakeSlug); err != nil {
		t.Fatalf("cleanupHandlerTestFixtureIdent: %v", err)
	}

	if got := countUserByEmail(t, testPool, teardownFlakeEmail); got != 0 {
		t.Fatalf("fixture user still present after cleanup (id=%s)", userID)
	}
	if got := countWorkspaceBySlug(t, testPool, teardownFlakeSlug); got != 0 {
		t.Fatalf("fixture workspace still present after cleanup (id=%s)", workspaceID)
	}
	if got := countByID(t, testPool, "agent", agentID); got != 0 {
		t.Fatalf("fixture agent still present after cleanup (id=%s)", agentID)
	}
	if got := countByID(t, testPool, "squad", squadID); got != 0 {
		t.Fatalf("fixture squad still present after cleanup (id=%s)", squadID)
	}

	// Idempotent second pass must also succeed.
	if err := cleanupHandlerTestFixtureIdent(context.Background(), testPool, teardownFlakeEmail, teardownFlakeSlug); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}
}

// TestCleanupHandlerTestFixture_Paths covers acceptance #4: success, failed
// setup (partial rows), and failed test-body (orphaned deps) cleanup paths.
func TestCleanupHandlerTestFixture_Paths(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	t.Run("success_full_graph", func(t *testing.T) {
		email := "handler-teardown-success@multica.ai"
		slug := "handler-teardown-success"
		_, _, _, _ = seedOrphanedSquadLeaderFixture(t, testPool, email, slug)
		if err := cleanupHandlerTestFixtureIdent(ctx, testPool, email, slug); err != nil {
			t.Fatalf("success path cleanup: %v", err)
		}
		if countUserByEmail(t, testPool, email) != 0 || countWorkspaceBySlug(t, testPool, slug) != 0 {
			t.Fatal("success path left fixture rows")
		}
	})

	t.Run("failed_setup_user_only", func(t *testing.T) {
		email := "handler-teardown-setup-fail@multica.ai"
		slug := "handler-teardown-setup-fail"
		_ = cleanupHandlerTestFixtureIdent(ctx, testPool, email, slug)

		var userID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
		`, "Partial Setup User", email).Scan(&userID); err != nil {
			t.Fatalf("insert partial user: %v", err)
		}
		// Simulate setup abort before workspace insert — cleanup must still
		// remove the orphan user.
		if err := cleanupHandlerTestFixtureIdent(ctx, testPool, email, slug); err != nil {
			t.Fatalf("failed-setup cleanup: %v", err)
		}
		if countUserByEmail(t, testPool, email) != 0 {
			t.Fatalf("partial setup user still present (id=%s)", userID)
		}
	})

	t.Run("failed_test_body_orphaned_squad", func(t *testing.T) {
		email := "handler-teardown-body-fail@multica.ai"
		slug := "handler-teardown-body-fail"
		userID, workspaceID, agentID, squadID := seedOrphanedSquadLeaderFixture(t, testPool, email, slug)
		// No t.Cleanup — body "failed" mid-flight leaving squad+agent.
		if err := cleanupHandlerTestFixtureIdent(ctx, testPool, email, slug); err != nil {
			t.Fatalf("failed-body cleanup: %v", err)
		}
		if countUserByEmail(t, testPool, email) != 0 {
			t.Fatalf("user remained after failed-body cleanup (id=%s)", userID)
		}
		if countWorkspaceBySlug(t, testPool, slug) != 0 {
			t.Fatalf("workspace remained after failed-body cleanup (id=%s)", workspaceID)
		}
		if countByID(t, testPool, "agent", agentID) != 0 || countByID(t, testPool, "squad", squadID) != 0 {
			t.Fatal("orphaned agent/squad remained after failed-body cleanup")
		}
	})
}

// TestCleanupHandlerTestFixture_ErrorIncludesFixtureIDs covers acceptance #5:
// teardown failures must name the fixture email/slug, never secrets.
func TestCleanupHandlerTestFixture_ErrorIncludesFixtureIDs(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cleanupHandlerTestFixtureIdent(ctx, testPool, "fixture-id@multica.ai", "fixture-id-slug")
	if err == nil {
		t.Fatal("expected cleanup error from cancelled context")
	}
	msg := err.Error()
	if !strings.Contains(msg, "fixture-id@multica.ai") || !strings.Contains(msg, "fixture-id-slug") {
		t.Fatalf("teardown error missing fixture ids: %q", msg)
	}
	if !strings.Contains(msg, "step=") {
		t.Fatalf("teardown error missing step: %q", msg)
	}
	for _, secretish := range []string{"password", "token", "DATABASE_URL"} {
		if strings.Contains(msg, secretish) {
			t.Fatalf("teardown error must not mention %q: %q", secretish, msg)
		}
	}
}
