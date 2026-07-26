-- name: ListProviderCredentials :many
SELECT *
FROM provider_credentials
WHERE workspace_id = $1 AND provider = $2
ORDER BY created_at, id;

-- name: GetProviderCredentialInWorkspace :one
SELECT *
FROM provider_credentials
WHERE id = $1 AND workspace_id = $2;

-- name: CreateProviderCredential :one
INSERT INTO provider_credentials (
    workspace_id, provider, account_label, sealed_token, fingerprint
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: ReplaceProviderCredentialToken :one
UPDATE provider_credentials
SET sealed_token = $3,
    fingerprint = $4,
    last_validated_at = NULL,
    last_validation_status = 'pending',
    last_validation_note = '',
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: UpdateProviderCredentialValidation :exec
UPDATE provider_credentials
SET last_validated_at = $3,
    last_validation_status = $4,
    last_validation_note = $5,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2;

-- name: DeleteProviderCredential :execrows
DELETE FROM provider_credentials
WHERE id = $1 AND workspace_id = $2;

-- name: DeleteProviderLimitSnapshotsForAccount :execrows
DELETE FROM provider_limit_snapshots
WHERE workspace_id = $1 AND provider = $2 AND account_key = $3;
