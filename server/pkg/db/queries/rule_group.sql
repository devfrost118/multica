-- Rule Group CRUD

-- name: ListRuleGroupSummariesByWorkspace :many
-- List endpoint shape: group metadata plus rule/binding counts, but no rule
-- content (rule bodies can be large and the list never renders them).
SELECT
    rg.id, rg.workspace_id, rg.name, rg.description, rg.enabled,
    rg.source_type, rg.source_ref, rg.version, rg.created_by,
    rg.created_at, rg.updated_at,
    (SELECT count(*) FROM rule_group_rule r WHERE r.rule_group_id = rg.id) AS rule_count,
    (SELECT count(*) FROM rule_group_binding b WHERE b.rule_group_id = rg.id) AS binding_count
FROM rule_group rg
WHERE rg.workspace_id = $1
ORDER BY rg.name ASC;

-- name: GetRuleGroupInWorkspace :one
SELECT * FROM rule_group
WHERE id = $1 AND workspace_id = $2;

-- name: GetRuleGroupByName :one
-- Used by the builtin seeder to find a group by its stable display name within
-- a workspace (the (workspace_id, name) unique key) so seeding is idempotent.
SELECT * FROM rule_group
WHERE workspace_id = $1 AND name = $2;

-- name: CreateRuleGroup :one
INSERT INTO rule_group (
    workspace_id, name, description, enabled, source_type, source_ref, version, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateRuleGroup :one
UPDATE rule_group SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    enabled = COALESCE(sqlc.narg('enabled'), enabled),
    version = COALESCE(sqlc.narg('version'), version),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteRuleGroup :exec
-- workspace_id is a SQL-layer tenant guard; cascades to rules and bindings.
DELETE FROM rule_group WHERE id = $1 AND workspace_id = $2;

-- Rule CRUD (rules live inside a group)

-- name: ListRuleGroupRules :many
SELECT * FROM rule_group_rule
WHERE rule_group_id = $1
ORDER BY sort_order ASC, name ASC;

-- name: GetRuleGroupRuleInGroup :one
SELECT * FROM rule_group_rule
WHERE id = $1 AND rule_group_id = $2;

-- name: GetRuleGroupRuleByName :one
-- Used by the builtin seeder to find a rule by its name within a group (the
-- (rule_group_id, name) unique key) so re-seeding updates in place.
SELECT * FROM rule_group_rule
WHERE rule_group_id = $1 AND name = $2;

-- name: CreateRuleGroupRule :one
INSERT INTO rule_group_rule (
    workspace_id, rule_group_id, name, description, content,
    sort_order, enabled, file_name, tags, runtime_hints
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateRuleGroupRule :one
UPDATE rule_group_rule SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    content = COALESCE(sqlc.narg('content'), content),
    sort_order = COALESCE(sqlc.narg('sort_order'), sort_order),
    enabled = COALESCE(sqlc.narg('enabled'), enabled),
    file_name = COALESCE(sqlc.narg('file_name'), file_name),
    tags = COALESCE(sqlc.narg('tags'), tags),
    runtime_hints = COALESCE(sqlc.narg('runtime_hints'), runtime_hints),
    updated_at = now()
WHERE id = $1 AND rule_group_id = $2
RETURNING *;

-- name: DeleteRuleGroupRule :exec
DELETE FROM rule_group_rule WHERE id = $1 AND rule_group_id = $2;

-- Binding CRUD (assign a group to a workspace/project/squad/agent scope)

-- name: ListRuleGroupBindingsByWorkspace :many
SELECT rgb.*, rg.name AS rule_group_name
FROM rule_group_binding rgb
JOIN rule_group rg ON rg.id = rgb.rule_group_id
WHERE rgb.workspace_id = $1
ORDER BY rgb.sort_order ASC, rg.name ASC;

-- name: GetRuleGroupBindingInWorkspace :one
SELECT * FROM rule_group_binding
WHERE id = $1 AND workspace_id = $2;

-- name: CreateRuleGroupBinding :one
INSERT INTO rule_group_binding (
    workspace_id, rule_group_id, project_id, squad_id, agent_id,
    enabled, sort_order, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateRuleGroupBinding :one
-- Only enabled / sort_order are mutable. Re-targeting a binding to a different
-- scope is a delete + create, not an update.
UPDATE rule_group_binding SET
    enabled = COALESCE(sqlc.narg('enabled'), enabled),
    sort_order = COALESCE(sqlc.narg('sort_order'), sort_order),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteRuleGroupBinding :exec
DELETE FROM rule_group_binding WHERE id = $1 AND workspace_id = $2;

-- Effective rules

-- name: ListEffectiveRules :many
-- Assemble the effective rule set for a (project, squad, agent) combination
-- within a workspace. Workspace-scoped bindings always apply; project/squad/
-- agent bindings apply only when the matching id is supplied. Rows come back
-- pre-ordered by scope layer (workspace -> project -> squad -> agent), then by
-- binding sort_order, group name, rule sort_order, rule name, rule id.
SELECT
    rgb.id AS binding_id,
    rgb.sort_order AS binding_sort_order,
    rg.id AS rule_group_id,
    rg.name AS rule_group_name,
    r.id AS rule_id,
    r.name AS rule_name,
    r.description AS rule_description,
    r.content AS rule_content,
    r.sort_order AS rule_sort_order,
    r.file_name AS rule_file_name,
    r.runtime_hints AS rule_runtime_hints,
    (CASE
        WHEN rgb.project_id IS NOT NULL THEN 'project'
        WHEN rgb.squad_id IS NOT NULL THEN 'squad'
        WHEN rgb.agent_id IS NOT NULL THEN 'agent'
        ELSE 'workspace'
    END)::text AS scope_type
FROM rule_group_binding rgb
JOIN rule_group rg ON rg.id = rgb.rule_group_id
JOIN rule_group_rule r ON r.rule_group_id = rg.id
WHERE rgb.workspace_id = $1
    AND rgb.enabled = true
    AND rg.enabled = true
    AND r.enabled = true
    AND (
        (rgb.project_id IS NULL AND rgb.squad_id IS NULL AND rgb.agent_id IS NULL)
        OR rgb.project_id = sqlc.narg('project_id')
        OR rgb.squad_id = sqlc.narg('squad_id')
        OR rgb.agent_id = sqlc.narg('agent_id')
    )
ORDER BY
    (CASE
        WHEN rgb.project_id IS NULL AND rgb.squad_id IS NULL AND rgb.agent_id IS NULL THEN 0
        WHEN rgb.project_id IS NOT NULL THEN 1
        WHEN rgb.squad_id IS NOT NULL THEN 2
        ELSE 3
    END),
    rgb.sort_order ASC, rg.name ASC, r.sort_order ASC, r.name ASC, r.id ASC;
