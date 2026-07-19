// Backfill_project_environment_secrets migrates project environment secrets
// between legacy plaintext and the versioned encrypted envelope.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/projectenvsecrets"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

const projectEnvironmentSecretKeyEnv = "MULTICA_PROJECT_ENV_SECRET_KEY"

type config struct {
	execute     bool
	decrypt     bool
	rotate      bool
	oldKeyEnv   string
	databaseURL string
}

type summary struct {
	Scanned int
	Updated int
	Skipped int
	Legacy  int
	Sealed  int
}

func main() {
	if err := run(); err != nil {
		slog.Error("project environment secret backfill failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config{}
	flag.BoolVar(&cfg.execute, "execute", false, "apply changes; without this flag only report candidates")
	flag.BoolVar(&cfg.decrypt, "decrypt", false, "convert sealed envelopes to legacy plaintext (requires --execute)")
	flag.BoolVar(&cfg.rotate, "rotate", false, "re-encrypt sealed envelopes from --old-key-env to the current key")
	flag.StringVar(&cfg.oldKeyEnv, "old-key-env", "MULTICA_PROJECT_ENV_SECRET_OLD_KEY", "environment variable containing the previous base64 key for --rotate")
	flag.StringVar(&cfg.databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	flag.Parse()

	if cfg.decrypt && cfg.rotate {
		return errors.New("--decrypt and --rotate cannot be used together")
	}
	if (cfg.decrypt || cfg.rotate) && !cfg.execute {
		return errors.New("--decrypt and --rotate require --execute")
	}
	if cfg.databaseURL == "" {
		cfg.databaseURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	current, err := codecFromEnv(projectEnvironmentSecretKeyEnv)
	if err != nil {
		return err
	}
	source := current
	if cfg.rotate {
		source, err = codecFromEnv(cfg.oldKeyEnv)
		if err != nil {
			return fmt.Errorf("load rotation key: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	result, err := process(ctx, pool, cfg, source, current)
	if err != nil {
		return err
	}
	slog.Info("project environment secret backfill complete",
		"scanned", result.Scanned,
		"updated", result.Updated,
		"skipped", result.Skipped,
		"legacy", result.Legacy,
		"sealed", result.Sealed,
		"execute", cfg.execute,
		"decrypt", cfg.decrypt,
		"rotate", cfg.rotate,
	)
	return nil
}

func codecFromEnv(envVar string) (projectenvsecrets.Codec, error) {
	key, err := secretbox.LoadKey(envVar)
	if err != nil {
		return projectenvsecrets.Codec{}, err
	}
	box, err := secretbox.New(key)
	if err != nil {
		return projectenvsecrets.Codec{}, err
	}
	return projectenvsecrets.New(box), nil
}

func process(ctx context.Context, pool *pgxpool.Pool, cfg config, source, target projectenvsecrets.Codec) (summary, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return summary{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT id, secrets FROM project_environment ORDER BY id FOR UPDATE`)
	if err != nil {
		return summary{}, fmt.Errorf("list project environments: %w", err)
	}
	defer rows.Close()

	result := summary{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return summary{}, fmt.Errorf("scan project environment: %w", err)
		}
		result.Scanned++
		sealed := projectenvsecrets.IsSealed(raw)
		if sealed {
			result.Sealed++
		} else {
			result.Legacy++
		}

		replacement, update, err := replacementForRecord(raw, cfg, source, target)
		if err != nil {
			return summary{}, fmt.Errorf("process project environment %s: %w", id, err)
		}
		if !update {
			result.Skipped++
			continue
		}
		if !cfg.execute {
			result.Updated++
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE project_environment SET secrets = $1, updated_at = NOW() WHERE id = $2`, replacement, id); err != nil {
			return summary{}, fmt.Errorf("update project environment %s: %w", id, err)
		}
		result.Updated++
	}
	if err := rows.Err(); err != nil {
		return summary{}, fmt.Errorf("iterate project environments: %w", err)
	}
	if !cfg.execute {
		return result, tx.Rollback(ctx)
	}
	if err := tx.Commit(ctx); err != nil {
		return summary{}, fmt.Errorf("commit project environment backfill: %w", err)
	}
	return result, nil
}

// replacementForRecord computes one safe migration step without exposing the
// plaintext. Rotation first tries the current key, so a retry after a partial
// run skips rows already re-encrypted under that key.
func replacementForRecord(raw []byte, cfg config, source, target projectenvsecrets.Codec) ([]byte, bool, error) {
	sealed := projectenvsecrets.IsSealed(raw)
	if cfg.rotate && sealed {
		if _, err := target.Open(raw); err == nil {
			return nil, false, nil
		}
	}

	secrets, err := source.Open(raw)
	if err != nil {
		return nil, false, err
	}
	switch {
	case cfg.decrypt:
		if !sealed {
			return nil, false, nil
		}
		replacement, err := json.Marshal(secrets)
		return replacement, true, err
	case cfg.rotate:
		replacement, err := target.Seal(secrets)
		return replacement, true, err
	default:
		if sealed {
			return nil, false, nil
		}
		replacement, err := target.Seal(secrets)
		return replacement, true, err
	}
}
