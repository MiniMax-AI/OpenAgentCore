-- +goose Up
CREATE TABLE project_api_keys (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    prefix text NOT NULL,
    token_sha256 text NOT NULL UNIQUE CHECK (token_sha256 ~ '^[0-9a-f]{64}$'),
    binding_digest text NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    tenant_id uuid NOT NULL REFERENCES execution_project_scopes(tenant_id),
    organization_id text NOT NULL,
    project_id text NOT NULL,
    subject_kind text NOT NULL CHECK (subject_kind IN ('user', 'service_account')),
    subject_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
CREATE INDEX project_api_keys_owner ON project_api_keys
    (binding_digest, tenant_id, organization_id, project_id, subject_kind, subject_id, created_at DESC, id);

-- +goose Down
DROP TABLE project_api_keys;
