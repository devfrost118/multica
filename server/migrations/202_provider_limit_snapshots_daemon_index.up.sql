-- Serves ListLatestProviderLimitSnapshotsByDaemon, which dedupes the "by
-- execution environment" overview by the physical daemon_id rather than
-- runtime_id: a single daemon fans its report out to every runtime it
-- currently services (FRO-189), so grouping on runtime_id previously showed
-- the same daemon's data once per runtime registration. Keep this as the
-- migration's only statement: PostgreSQL rejects CREATE INDEX CONCURRENTLY
-- inside a transaction or multi-command string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_provider_limit_snapshots_daemon_latest
    ON provider_limit_snapshots (workspace_id, daemon_id, provider, account_key, checked_at DESC, created_at DESC);
