package db

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProjectEnvironmentDeliveryRequiresRuntimeAllowlist(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping live-Postgres sqlc test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	queries := New(pool)
	workspaceID := testUUID("11111111-1111-4111-8111-111111111111")
	projectID := testUUID("22222222-2222-4222-8222-222222222222")
	runtimeID := testUUID("33333333-3333-4333-8333-333333333333")
	otherRuntimeID := testUUID("44444444-4444-4444-8444-444444444444")

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})

	_, err = pool.Exec(ctx, `
		INSERT INTO workspace (id, name, slug)
		VALUES ($1, 'project env sqlc test', 'project-env-sqlc-test')
		ON CONFLICT (id) DO NOTHING
	`, workspaceID)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO project (id, workspace_id, title, status)
		VALUES ($1, $2, 'Project env sqlc test', 'planned')
	`, projectID, workspaceID)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO agent_runtime (id, workspace_id, daemon_id, name, runtime_mode, provider)
		VALUES
			($1, $2, 'project-env-sqlc-test-a', 'Runtime A', 'local', 'codex'),
			($3, $2, 'project-env-sqlc-test-b', 'Runtime B', 'local', 'codex')
	`, runtimeID, workspaceID, otherRuntimeID)
	if err != nil {
		t.Fatalf("seed runtimes: %v", err)
	}

	env, err := queries.CreateProjectEnvironment(ctx, CreateProjectEnvironmentParams{
		ProjectID:   projectID,
		WorkspaceID: workspaceID,
		Name:        "staging",
		Description: pgtype.Text{String: "Staging environment", Valid: true},
		Config:      []byte(`{"url":"https://staging.example.test"}`),
		Secrets:     []byte(`{"TOKEN":"secret"}`),
		CreatedBy:   pgtype.UUID{},
	})
	if err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	withoutAllowlist, err := queries.ListProjectEnvironmentsForRuntimeDelivery(ctx, runtimeID)
	if err != nil {
		t.Fatalf("list delivery before allowlist: %v", err)
	}
	if len(withoutAllowlist) != 0 {
		t.Fatalf("delivery without allowlist returned %d environments", len(withoutAllowlist))
	}

	if err := queries.SetProjectEnvironmentRuntimeAllowlist(ctx, SetProjectEnvironmentRuntimeAllowlistParams{
		EnvironmentID: env.ID,
		RuntimeIds:    []pgtype.UUID{runtimeID},
	}); err != nil {
		t.Fatalf("set allowlist: %v", err)
	}

	forOtherRuntime, err := queries.ListProjectEnvironmentsForRuntimeDelivery(ctx, otherRuntimeID)
	if err != nil {
		t.Fatalf("list delivery for other runtime: %v", err)
	}
	if len(forOtherRuntime) != 0 {
		t.Fatalf("delivery for non-allowlisted runtime returned %d environments", len(forOtherRuntime))
	}

	forRuntime, err := queries.ListProjectEnvironmentsForRuntimeDelivery(ctx, runtimeID)
	if err != nil {
		t.Fatalf("list delivery for allowlisted runtime: %v", err)
	}
	if len(forRuntime) != 1 {
		t.Fatalf("delivery returned %d environments, want 1", len(forRuntime))
	}
	if forRuntime[0].ID != env.ID {
		t.Fatalf("delivery returned environment %v, want %v", forRuntime[0].ID, env.ID)
	}
}

func testUUID(value string) pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		panic(err)
	}
	return id
}
