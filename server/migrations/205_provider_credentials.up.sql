CREATE TABLE provider_credentials (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id           UUID NOT NULL,
    provider               TEXT NOT NULL,
    account_label          TEXT NOT NULL DEFAULT '',
    sealed_token           BYTEA NOT NULL,
    fingerprint            TEXT NOT NULL,
    last_validated_at      TIMESTAMPTZ,
    last_validation_status TEXT NOT NULL DEFAULT 'pending',
    last_validation_note   TEXT NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT provider_credentials_provider_check CHECK (provider IN ('factory')),
    CONSTRAINT provider_credentials_fingerprint_check CHECK (char_length(fingerprint) = 12),
    CONSTRAINT provider_credentials_validation_status_check
        CHECK (last_validation_status IN ('pending', 'valid', 'invalid', 'unavailable'))
);
