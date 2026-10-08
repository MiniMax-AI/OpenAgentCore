-- +goose Up
-- Core derives the idle suspension policy from the Provider's checkpoint
-- declaration, so the deployment no longer stores it.
ALTER TABLE runtime_deployment
    DROP CONSTRAINT runtime_deployment_setup_check,
    DROP COLUMN idle_seconds,
    DROP COLUMN retention_seconds,
    ADD CONSTRAINT runtime_deployment_setup_check CHECK (
        installation_id IS NULL OR
        (provider_kind = '' AND mode = '' AND generation >= 0) OR
        (provider_kind <> '' AND mode IN ('nodes','direct') AND generation > 0)
    );

-- +goose Down
ALTER TABLE runtime_deployment
    DROP CONSTRAINT runtime_deployment_setup_check,
    ADD COLUMN idle_seconds bigint NOT NULL DEFAULT 0 CHECK (idle_seconds >= 0),
    ADD COLUMN retention_seconds bigint NOT NULL DEFAULT 0 CHECK (retention_seconds >= 0);
UPDATE runtime_deployment SET idle_seconds = 300, retention_seconds = 86400 WHERE provider_kind = 'microsandbox';
ALTER TABLE runtime_deployment
    ADD CONSTRAINT runtime_deployment_setup_check CHECK (
        installation_id IS NULL OR
        (provider_kind = '' AND mode = '' AND generation >= 0 AND idle_seconds = 0 AND retention_seconds = 0) OR
        (provider_kind <> '' AND mode IN ('nodes','direct') AND generation > 0 AND
            ((idle_seconds = 0 AND retention_seconds = 0) OR (idle_seconds > 0 AND retention_seconds > 0)))
    );
