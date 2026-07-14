-- name: ListProjectEnvironments :many
SELECT * FROM project_environment
WHERE project_id = $1
ORDER BY name ASC, created_at ASC;

-- name: GetProjectEnvironment :one
SELECT * FROM project_environment
WHERE id = $1;

-- name: GetProjectEnvironmentInWorkspace :one
SELECT * FROM project_environment
WHERE id = $1 AND workspace_id = $2;

-- name: CreateProjectEnvironment :one
INSERT INTO project_environment (
    project_id, workspace_id, name, description, config, secrets, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: UpdateProjectEnvironment :one
UPDATE project_environment
SET name        = $2,
    description = $3,
    config      = $4,
    secrets     = $5,
    updated_at  = now()
WHERE id = $1
RETURNING *;

-- name: DeleteProjectEnvironment :exec
DELETE FROM project_environment WHERE id = $1;

-- name: ListProjectEnvironmentDaemons :many
SELECT * FROM project_environment_daemon
WHERE environment_id = $1
ORDER BY created_at ASC, runtime_id ASC;

-- name: AddProjectEnvironmentDaemon :exec
INSERT INTO project_environment_daemon (environment_id, runtime_id)
VALUES ($1, $2)
ON CONFLICT (environment_id, runtime_id) DO NOTHING;

-- name: RemoveProjectEnvironmentDaemon :exec
DELETE FROM project_environment_daemon
WHERE environment_id = $1 AND runtime_id = $2;

-- name: SetProjectEnvironmentRuntimeAllowlist :exec
WITH deleted AS (
    DELETE FROM project_environment_daemon
    WHERE environment_id = sqlc.arg('environment_id')::uuid
)
INSERT INTO project_environment_daemon (environment_id, runtime_id)
SELECT sqlc.arg('environment_id')::uuid, runtime_id
FROM unnest(sqlc.arg('runtime_ids')::uuid[]) AS runtime_id;

-- name: ListProjectEnvironmentsForRuntimeDelivery :many
SELECT pe.*
FROM project_environment pe
JOIN project_environment_daemon ped ON ped.environment_id = pe.id
WHERE ped.runtime_id = $1
ORDER BY pe.project_id ASC, pe.name ASC, pe.created_at ASC;
