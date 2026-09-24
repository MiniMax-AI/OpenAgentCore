-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM project_api_keys) THEN
        RAISE EXCEPTION 'Legacy inherited API keys prevent independent key spaces; use a clean private installation or explicitly retire legacy key records after preserving their assets. No data has been removed.';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX project_api_keys_owner;
ALTER TABLE project_api_keys DROP COLUMN binding_digest;
ALTER TABLE project_api_keys ADD CONSTRAINT project_api_keys_tenant_unique UNIQUE (tenant_id);
CREATE TABLE admin_audit_log (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES execution_project_scopes(tenant_id),
    admin_credential_id text NOT NULL,
    actor_label text NOT NULL,
    action text NOT NULL,
    target_key_id text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    result_ids jsonb NOT NULL DEFAULT '[]',
    request_id text NOT NULL,
    trace_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (tenant_id, request_id, action, resource_type, resource_id)
);
CREATE INDEX admin_audit_log_history ON admin_audit_log (tenant_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE admin_audit_log;
ALTER TABLE project_api_keys DROP CONSTRAINT project_api_keys_tenant_unique;
ALTER TABLE project_api_keys ADD COLUMN binding_digest text;
