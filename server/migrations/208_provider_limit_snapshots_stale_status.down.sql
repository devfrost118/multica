ALTER TABLE provider_limit_snapshots
    DROP CONSTRAINT provider_limit_snapshots_status_check,
    ADD CONSTRAINT provider_limit_snapshots_status_check
        CHECK (status IN ('ok', 'partial', 'unavailable', 'error'));