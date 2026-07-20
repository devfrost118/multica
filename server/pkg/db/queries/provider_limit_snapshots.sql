-- name: UpsertProviderLimitSnapshot :one
INSERT INTO provider_limit_snapshots (
    workspace_id,
    runtime_id,
    daemon_id,
    provider,
    account_key,
    account_label,
    checked_at,
    status,
    source_kind,
    source_confidence,
    source_freshness_seconds,
    buckets,
    error_note,
    content_hash
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (workspace_id, runtime_id, content_hash) DO UPDATE
SET content_hash = provider_limit_snapshots.content_hash
RETURNING *;

-- name: ListLatestProviderLimitSnapshots :many
SELECT DISTINCT ON (provider, account_key) *
FROM provider_limit_snapshots
WHERE workspace_id = $1
ORDER BY provider, account_key, checked_at DESC, created_at DESC;

-- name: ListLatestProviderLimitSnapshotsByDaemon :many
SELECT DISTINCT ON (daemon_id, provider, account_key) *
FROM provider_limit_snapshots
WHERE workspace_id = $1
ORDER BY daemon_id, provider, account_key, checked_at DESC, created_at DESC;

-- name: ListProviderLimitSnapshotHistory :many
SELECT *
FROM provider_limit_snapshots
WHERE workspace_id = $1
ORDER BY checked_at DESC, created_at DESC
LIMIT $2;

-- name: DeleteExpiredProviderLimitSnapshots :execrows
DELETE FROM provider_limit_snapshots snapshot
WHERE snapshot.checked_at < $1
  AND NOT EXISTS (
      SELECT 1
      FROM provider_limit_snapshots last_good
      WHERE last_good.workspace_id = snapshot.workspace_id
        AND last_good.runtime_id = snapshot.runtime_id
        AND last_good.provider = snapshot.provider
        AND last_good.account_key = snapshot.account_key
        AND last_good.status IN ('ok', 'partial')
        AND last_good.id = snapshot.id
        AND NOT EXISTS (
            SELECT 1
            FROM provider_limit_snapshots newer_good
            WHERE newer_good.workspace_id = snapshot.workspace_id
              AND newer_good.runtime_id = snapshot.runtime_id
              AND newer_good.provider = snapshot.provider
              AND newer_good.account_key = snapshot.account_key
              AND newer_good.status IN ('ok', 'partial')
              AND (newer_good.checked_at, newer_good.created_at, newer_good.id) > (last_good.checked_at, last_good.created_at, last_good.id)
        )
  );
