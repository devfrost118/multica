CREATE INDEX CONCURRENTLY idx_provider_credentials_workspace_provider ON provider_credentials (workspace_id, provider, created_at);
