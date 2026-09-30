-- +goose Up
CREATE TABLE write_audit_operations (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    key_id text NOT NULL,
    key_name text NOT NULL,
    key_prefix text NOT NULL,
    key_kind text NOT NULL CHECK (key_kind IN ('static', 'issued', 'console')),
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    parent_id text NOT NULL DEFAULT '',
    request_id text NOT NULL,
    trace_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (tenant_id, request_id),
    UNIQUE (tenant_id, id)
);
CREATE INDEX write_audit_operations_history ON write_audit_operations (tenant_id, created_at DESC, id DESC);
CREATE INDEX write_audit_operations_key_history ON write_audit_operations (tenant_id, key_id, created_at DESC, id DESC);
CREATE INDEX write_audit_operations_resource_history ON write_audit_operations (tenant_id, resource_type, resource_id, created_at DESC, id DESC);
CREATE INDEX write_audit_operations_expiry ON write_audit_operations (created_at, id);

CREATE TABLE write_audit_owners (
    tenant_id uuid NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    parent_id text NOT NULL DEFAULT '',
    operation_id uuid NOT NULL,
    PRIMARY KEY (tenant_id, resource_type, resource_id),
    FOREIGN KEY (tenant_id, operation_id) REFERENCES write_audit_operations (tenant_id, id)
);
CREATE INDEX write_audit_owners_operation ON write_audit_owners (operation_id);

-- +goose Down
DROP TABLE write_audit_owners;
DROP TABLE write_audit_operations;
