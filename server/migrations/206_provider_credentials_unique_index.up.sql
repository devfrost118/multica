CREATE UNIQUE INDEX CONCURRENTLY idx_provider_credentials_workspace_provider_id ON provider_credentials (workspace_id, provider, id);
