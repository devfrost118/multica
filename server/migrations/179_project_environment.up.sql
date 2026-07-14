CREATE TABLE project_environment (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   UUID NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT,
    config       JSONB NOT NULL DEFAULT '{}',
    secrets      JSONB NOT NULL DEFAULT '{}',
    created_by   UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE TABLE project_environment_daemon (
    environment_id UUID NOT NULL REFERENCES project_environment(id) ON DELETE CASCADE,
    runtime_id     UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, runtime_id)
);

CREATE INDEX idx_project_environment_project
    ON project_environment(project_id, name);

CREATE INDEX idx_project_environment_workspace
    ON project_environment(workspace_id);

CREATE INDEX idx_project_environment_daemon_runtime
    ON project_environment_daemon(runtime_id, environment_id);
