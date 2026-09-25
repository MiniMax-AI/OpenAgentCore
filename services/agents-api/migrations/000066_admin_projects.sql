-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM project_api_keys) THEN
        RAISE EXCEPTION 'Legacy inherited API keys prevent Project catalog installation; use a clean private installation. No existing data has been removed.';
    END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE projects (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    tenant_id uuid NOT NULL UNIQUE REFERENCES execution_project_scopes(tenant_id),
    subject_kind text NOT NULL CHECK (subject_kind = 'service_account'),
    subject_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,
    UNIQUE (id, tenant_id)
);
DROP INDEX project_api_keys_owner;
ALTER TABLE project_api_keys DROP COLUMN binding_digest,
    DROP COLUMN tenant_id, DROP COLUMN organization_id, DROP COLUMN project_id,
    DROP COLUMN subject_kind, DROP COLUMN subject_id;
ALTER TABLE project_api_keys ADD COLUMN project_id uuid NOT NULL REFERENCES projects(id);
CREATE INDEX project_api_keys_project ON project_api_keys(project_id,id);
CREATE TABLE admin_audit_log (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    project_id uuid NOT NULL,
    admin_credential_id text NOT NULL,
    actor_label text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    result_ids jsonb NOT NULL DEFAULT '[]',
    request_id text NOT NULL,
    trace_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (project_id,tenant_id) REFERENCES projects(id,tenant_id),
    UNIQUE (tenant_id, request_id, action, resource_type, resource_id)
);
CREATE INDEX admin_audit_log_history ON admin_audit_log (project_id, created_at DESC, id DESC);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'Project catalog downgrade is unsupported; restore a complete database backup instead.';
END $$;
-- +goose StatementEnd
