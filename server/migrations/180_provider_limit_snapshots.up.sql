CREATE TABLE provider_limit_snapshots (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id             UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    runtime_id               UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
    provider                 TEXT NOT NULL,
    account_key              TEXT NOT NULL,
    account_label            TEXT NOT NULL DEFAULT '',
    checked_at               TIMESTAMPTZ NOT NULL,
    status                   TEXT NOT NULL,
    source_kind              TEXT NOT NULL,
    source_confidence        TEXT NOT NULL,
    source_freshness_seconds BIGINT NOT NULL DEFAULT 0,
    buckets                  JSONB NOT NULL DEFAULT '[]'::jsonb,
    error_note               TEXT NOT NULL DEFAULT '',
    content_hash             TEXT NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT provider_limit_snapshots_status_check
        CHECK (status IN ('ok', 'partial', 'unavailable', 'error')),
    CONSTRAINT provider_limit_snapshots_source_kind_check
        CHECK (source_kind IN ('official_api', 'cli', 'local_auth_state', 'local_log')),
    CONSTRAINT provider_limit_snapshots_source_confidence_check
        CHECK (source_confidence IN ('official', 'observed', 'estimated')),
    CONSTRAINT provider_limit_snapshots_source_freshness_check
        CHECK (source_freshness_seconds >= 0),
    CONSTRAINT provider_limit_snapshots_content_hash_check
        CHECK (char_length(content_hash) = 64),
    UNIQUE (workspace_id, runtime_id, content_hash)
);

CREATE INDEX idx_provider_limit_snapshots_workspace_latest
    ON provider_limit_snapshots (workspace_id, provider, account_key, checked_at DESC, created_at DESC);

CREATE INDEX idx_provider_limit_snapshots_runtime_latest
    ON provider_limit_snapshots (workspace_id, runtime_id, checked_at DESC, created_at DESC);
