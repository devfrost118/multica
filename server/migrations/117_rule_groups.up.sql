-- Rule Groups: workspace-scoped collections of markdown rules that can be
-- bound to a workspace, project, squad, or agent. The "effective rules" for an
-- agent/project/squad combination are assembled by layering bindings in a
-- fixed order (workspace -> project -> squad -> agent). This migration covers
-- the data model only; runtime injection of effective rules is a later task in
-- the "Rule Groups for agents" epic (FRO-61).

CREATE TABLE rule_group (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    -- 'github' is reserved for a future import/sync source; this task only
    -- creates 'manual' groups. The CHECK keeps the enum honest at the DB level.
    source_type TEXT NOT NULL DEFAULT 'manual'
        CHECK (source_type IN ('manual', 'builtin', 'github')),
    source_ref JSONB NOT NULL DEFAULT '{}',
    version TEXT,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, name),
    -- Composite key so child tables can FK (id, workspace_id) and inherit the
    -- workspace boundary at the schema level rather than trusting the handler.
    UNIQUE(id, workspace_id)
);

CREATE TABLE rule_group_rule (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    rule_group_id UUID NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT true,
    file_name TEXT,
    tags TEXT[] NOT NULL DEFAULT '{}',
    runtime_hints JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite FK pins each rule to its group AND its group's workspace.
    FOREIGN KEY (rule_group_id, workspace_id)
        REFERENCES rule_group(id, workspace_id) ON DELETE CASCADE,
    UNIQUE(rule_group_id, name),
    CHECK (btrim(name) <> ''),
    CHECK (btrim(content) <> '')
);

-- A file_name, when present, must be unique within its group.
CREATE UNIQUE INDEX uq_rule_group_rule_file_name
    ON rule_group_rule(rule_group_id, file_name)
    WHERE file_name IS NOT NULL;

CREATE TABLE rule_group_binding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    rule_group_id UUID NOT NULL,
    project_id UUID REFERENCES project(id) ON DELETE CASCADE,
    squad_id UUID REFERENCES squad(id) ON DELETE CASCADE,
    agent_id UUID REFERENCES agent(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT true,
    sort_order INT NOT NULL DEFAULT 0,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (rule_group_id, workspace_id)
        REFERENCES rule_group(id, workspace_id) ON DELETE CASCADE,
    -- At most one scope target may be set. Zero set = workspace scope.
    CHECK (num_nonnulls(project_id, squad_id, agent_id) <= 1)
);

-- Lookup indexes.
CREATE INDEX idx_rule_group_workspace ON rule_group(workspace_id, enabled, name);
CREATE INDEX idx_rule_group_rule_group ON rule_group_rule(rule_group_id, enabled, sort_order);
CREATE INDEX idx_rule_group_binding_workspace ON rule_group_binding(workspace_id, enabled);
CREATE INDEX idx_rule_group_binding_project ON rule_group_binding(project_id) WHERE project_id IS NOT NULL;
CREATE INDEX idx_rule_group_binding_squad ON rule_group_binding(squad_id) WHERE squad_id IS NOT NULL;
CREATE INDEX idx_rule_group_binding_agent ON rule_group_binding(agent_id) WHERE agent_id IS NOT NULL;

-- A rule group may be bound to each scope target only once. Partial unique
-- indexes (one per scope kind) enforce "no duplicate binding" without a
-- nullable composite key, which Postgres would treat as always-distinct.
CREATE UNIQUE INDEX uq_rule_group_binding_workspace
    ON rule_group_binding(workspace_id, rule_group_id)
    WHERE project_id IS NULL AND squad_id IS NULL AND agent_id IS NULL;
CREATE UNIQUE INDEX uq_rule_group_binding_project
    ON rule_group_binding(rule_group_id, project_id)
    WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX uq_rule_group_binding_squad
    ON rule_group_binding(rule_group_id, squad_id)
    WHERE squad_id IS NOT NULL;
CREATE UNIQUE INDEX uq_rule_group_binding_agent
    ON rule_group_binding(rule_group_id, agent_id)
    WHERE agent_id IS NOT NULL;

-- Workspace-boundary guard for the scope target. The composite FK already pins
-- the rule_group to the binding's workspace, but project_id/squad_id/agent_id
-- have only plain FKs to their own tables, so a binding could otherwise point
-- at a scope target in a different workspace. Handlers validate this and return
-- a clean 400; this trigger blocks any path that bypasses the API.
CREATE OR REPLACE FUNCTION validate_rule_group_binding_workspace()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.project_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM project WHERE id = NEW.project_id AND workspace_id = NEW.workspace_id
    ) THEN
        RAISE EXCEPTION 'project % is not in workspace %', NEW.project_id, NEW.workspace_id
            USING ERRCODE = '23514';
    END IF;
    IF NEW.squad_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM squad WHERE id = NEW.squad_id AND workspace_id = NEW.workspace_id
    ) THEN
        RAISE EXCEPTION 'squad % is not in workspace %', NEW.squad_id, NEW.workspace_id
            USING ERRCODE = '23514';
    END IF;
    IF NEW.agent_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM agent WHERE id = NEW.agent_id AND workspace_id = NEW.workspace_id
    ) THEN
        RAISE EXCEPTION 'agent % is not in workspace %', NEW.agent_id, NEW.workspace_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_validate_rule_group_binding_workspace
    BEFORE INSERT OR UPDATE ON rule_group_binding
    FOR EACH ROW EXECUTE FUNCTION validate_rule_group_binding_workspace();
