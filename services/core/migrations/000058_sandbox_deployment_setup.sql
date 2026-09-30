-- +goose Up
ALTER TABLE runtime_deployment ADD COLUMN web_managed boolean NOT NULL DEFAULT false;
ALTER TABLE runtime_deployment ADD COLUMN core_url text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment ADD COLUMN idle_seconds bigint NOT NULL DEFAULT 0 CHECK (idle_seconds >= 0);
ALTER TABLE runtime_deployment ADD COLUMN retention_seconds bigint NOT NULL DEFAULT 0 CHECK (retention_seconds >= 0);
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_identity_check CHECK (
    (installation_id IS NULL AND backend_fingerprint = '') OR
    (installation_id IS NOT NULL AND backend_fingerprint ~ '^[0-9a-f]{64}$') OR
    (web_managed AND installation_id IS NOT NULL AND provider_kind = '' AND backend_fingerprint = '')
);
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind = '' AND core_url = '' AND idle_seconds = 0 AND retention_seconds = 0) OR
        (provider_kind = 'docker' AND core_url <> '' AND idle_seconds = 0 AND retention_seconds = 0) OR
        (provider_kind = 'microsandbox' AND core_url <> '' AND idle_seconds > 0 AND retention_seconds > 0)
    ))
);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE web_managed) THEN
        RAISE EXCEPTION 'Cannot discard Web-managed sandbox deployment setup';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_identity_check;
ALTER TABLE runtime_deployment DROP COLUMN retention_seconds;
ALTER TABLE runtime_deployment DROP COLUMN idle_seconds;
ALTER TABLE runtime_deployment DROP COLUMN core_url;
ALTER TABLE runtime_deployment DROP COLUMN web_managed;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_check CHECK (
    (installation_id IS NULL AND backend_fingerprint = '') OR
    (installation_id IS NOT NULL AND backend_fingerprint ~ '^[0-9a-f]{64}$')
);
