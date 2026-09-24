-- +goose Up
CREATE TABLE admin_asset_copies (
    target_tenant_id uuid NOT NULL,
    idempotency_key text NOT NULL,
    request_hash bytea NOT NULL,
    result jsonb NOT NULL,
    audit_id uuid NOT NULL REFERENCES admin_audit_log(id),
    PRIMARY KEY (target_tenant_id, idempotency_key)
);

CREATE TABLE admin_resource_owners (
    tenant_id uuid NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    parent_id text NOT NULL DEFAULT '',
    audit_id uuid NOT NULL REFERENCES admin_audit_log(id),
    PRIMARY KEY (tenant_id, resource_type, resource_id)
);
CREATE INDEX admin_resource_owners_audit ON admin_resource_owners(audit_id);

-- +goose Down
DROP TABLE admin_resource_owners;
DROP TABLE admin_asset_copies;
