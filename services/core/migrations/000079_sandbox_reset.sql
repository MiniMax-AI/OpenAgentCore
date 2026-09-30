-- +goose Up
ALTER TABLE runtime_deployment RENAME COLUMN maintenance TO admission_paused;
-- Old Web-managed maintenance is retired; ordinary upgrades resume admission.
UPDATE runtime_deployment SET admission_paused = false WHERE web_managed;
ALTER TABLE runtime_deployment
    ADD COLUMN reset_clear text,
    ADD COLUMN reset_requested_at timestamptz,
    ADD COLUMN reset_deadline_at timestamptz,
    ADD COLUMN reset_forced_at timestamptz,
    ADD COLUMN reset_audit jsonb,
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

ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind='' AND mode='' AND generation>=0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='docker' AND mode='nodes' AND generation>0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='microsandbox' AND mode='nodes' AND generation>0 AND idle_seconds>0 AND retention_seconds>0) OR
        (provider_kind='e2b' AND mode='direct' AND generation>0 AND idle_seconds=0 AND retention_seconds=0)
    ))
);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE reset_clear IS NOT NULL) THEN
        RAISE EXCEPTION 'Cannot downgrade while a sandbox reset is active';
    END IF;
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE web_managed AND provider_kind = '' AND generation > 0) THEN
        RAISE EXCEPTION 'Configure a sandbox provider before downgrading a completed reset';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_deployment
    DROP CONSTRAINT runtime_deployment_reset_admission_check,
    DROP CONSTRAINT runtime_deployment_reset_check,
    DROP COLUMN reset_clear,
    DROP COLUMN reset_requested_at,
    DROP COLUMN reset_deadline_at,
    DROP COLUMN reset_forced_at,
    DROP COLUMN reset_audit;
ALTER TABLE runtime_deployment RENAME COLUMN admission_paused TO maintenance;
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind='' AND mode='' AND generation=0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='docker' AND mode='nodes' AND generation>0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='microsandbox' AND mode='nodes' AND generation>0 AND idle_seconds>0 AND retention_seconds>0) OR
        (provider_kind='e2b' AND mode='direct' AND generation>0 AND idle_seconds=0 AND retention_seconds=0)
    ))
);
