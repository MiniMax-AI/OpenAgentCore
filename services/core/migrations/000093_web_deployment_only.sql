-- +goose Up
-- Web setup is the only writer of the installation, so a recorded installation
-- is Web-managed, no deployment names a process-configured local node, every
-- node has the Core address it enrolled with, and only a sandbox reset pauses
-- admission.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE local_node_id IS NOT NULL OR (installation_id IS NOT NULL AND NOT web_managed)
        OR admission_paused <> (reset_clear IS NOT NULL))
        OR EXISTS (SELECT 1 FROM runtime_nodes WHERE removed_at IS NULL AND core_url = '') THEN
        RAISE EXCEPTION 'Cannot upgrade: the sandbox deployment was configured outside Web setup, or a live node has no Core address. Reset the sandbox in Web, then upgrade';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_deployment
    DROP CONSTRAINT runtime_deployment_identity_check,
    DROP CONSTRAINT runtime_deployment_setup_check,
    DROP CONSTRAINT runtime_deployment_reset_check,
    DROP CONSTRAINT runtime_deployment_reset_admission_check,
    DROP COLUMN web_managed,
    DROP COLUMN local_node_id,
    DROP COLUMN admission_paused,
    ADD CONSTRAINT runtime_deployment_identity_check CHECK (
        (installation_id IS NULL AND backend_fingerprint = '') OR
        (installation_id IS NOT NULL AND backend_fingerprint ~ '^[0-9a-f]{64}$') OR
        (installation_id IS NOT NULL AND provider_kind = '' AND backend_fingerprint = '')
    ),
    ADD CONSTRAINT runtime_deployment_setup_check CHECK (
        installation_id IS NULL OR
        (provider_kind = '' AND mode = '' AND generation >= 0 AND idle_seconds = 0 AND retention_seconds = 0) OR
        (provider_kind <> '' AND mode IN ('nodes','direct') AND generation > 0 AND
            ((idle_seconds = 0 AND retention_seconds = 0) OR (idle_seconds > 0 AND retention_seconds > 0)))
    ),
    ADD CONSTRAINT runtime_deployment_reset_check CHECK (
        (reset_clear IS NULL AND reset_requested_at IS NULL AND reset_deadline_at IS NULL
            AND reset_forced_at IS NULL AND reset_audit IS NULL)
        OR (reset_clear IS NOT NULL AND installation_id IS NOT NULL AND provider_kind <> ''
            AND reset_requested_at IS NOT NULL AND reset_audit IS NOT NULL
            AND jsonb_typeof(reset_audit) = 'object'
            AND ((reset_clear = 'auto' AND reset_deadline_at IS NOT NULL AND reset_forced_at IS NULL)
                OR (reset_clear = 'force' AND reset_forced_at IS NOT NULL)))
    );

-- +goose Down
ALTER TABLE runtime_deployment
    DROP CONSTRAINT runtime_deployment_identity_check,
    DROP CONSTRAINT runtime_deployment_setup_check,
    DROP CONSTRAINT runtime_deployment_reset_check,
    ADD COLUMN web_managed boolean NOT NULL DEFAULT false,
    ADD COLUMN local_node_id uuid,
    ADD COLUMN admission_paused boolean NOT NULL DEFAULT false;
UPDATE runtime_deployment SET web_managed = installation_id IS NOT NULL, admission_paused = reset_clear IS NOT NULL;
ALTER TABLE runtime_deployment
    ADD CONSTRAINT runtime_deployment_identity_check CHECK (
        (installation_id IS NULL AND backend_fingerprint = '') OR
        (installation_id IS NOT NULL AND backend_fingerprint ~ '^[0-9a-f]{64}$') OR
        (web_managed AND installation_id IS NOT NULL AND provider_kind = '' AND backend_fingerprint = '')
    ),
    ADD CONSTRAINT runtime_deployment_setup_check CHECK (
        NOT web_managed OR (local_node_id IS NULL AND (
            (provider_kind = '' AND mode = '' AND generation >= 0 AND idle_seconds = 0 AND retention_seconds = 0) OR
            (provider_kind <> '' AND mode IN ('nodes','direct') AND generation > 0 AND
                ((idle_seconds = 0 AND retention_seconds = 0) OR (idle_seconds > 0 AND retention_seconds > 0)))
        ))
    ),
    ADD CONSTRAINT runtime_deployment_reset_check CHECK (
        (reset_clear IS NULL AND reset_requested_at IS NULL AND reset_deadline_at IS NULL
            AND reset_forced_at IS NULL AND reset_audit IS NULL)
        OR (reset_clear IS NOT NULL AND web_managed AND provider_kind <> ''
            AND reset_requested_at IS NOT NULL AND reset_audit IS NOT NULL
            AND jsonb_typeof(reset_audit) = 'object'
            AND ((reset_clear = 'auto' AND reset_deadline_at IS NOT NULL AND reset_forced_at IS NULL)
                OR (reset_clear = 'force' AND reset_forced_at IS NOT NULL)))
    ),
    ADD CONSTRAINT runtime_deployment_reset_admission_check CHECK (
        NOT web_managed OR admission_paused = (reset_clear IS NOT NULL)
    );
