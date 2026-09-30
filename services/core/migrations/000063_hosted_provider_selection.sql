-- +goose Up
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_provider_kind_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_provider_kind_check CHECK (provider_kind IN ('','docker','microsandbox','e2b'));
ALTER TABLE runtime_deployment ADD COLUMN generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0);
ALTER TABLE runtime_deployment ADD COLUMN mode text NOT NULL DEFAULT '' CHECK (mode IN ('','nodes','direct'));
ALTER TABLE runtime_deployment ADD COLUMN e2b_template text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment ADD COLUMN e2b_credential bytea;
UPDATE runtime_deployment SET generation=1,mode='nodes' WHERE provider_kind<>'';
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind='' AND core_url='' AND mode='' AND generation=0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='docker' AND core_url<>'' AND mode='nodes' AND generation>0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='microsandbox' AND core_url<>'' AND mode='nodes' AND generation>0 AND idle_seconds>0 AND retention_seconds>0) OR
        (provider_kind='e2b' AND core_url<>'' AND mode='direct' AND generation>0 AND idle_seconds=0 AND retention_seconds=0)
    ))
);
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_check CHECK (
    (provider_kind='e2b' AND e2b_template<>'' AND e2b_credential IS NOT NULL) OR
    (provider_kind<>'e2b' AND e2b_template='' AND e2b_credential IS NULL)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE provider_kind='e2b' OR generation>1) THEN
        RAISE EXCEPTION 'Cannot discard hosted provider configuration generations';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_e2b_check;
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment DROP COLUMN e2b_credential;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template;
ALTER TABLE runtime_deployment DROP COLUMN mode;
ALTER TABLE runtime_deployment DROP COLUMN generation;
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_provider_kind_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_provider_kind_check CHECK (provider_kind IN ('','docker','microsandbox'));
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind='' AND core_url='' AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='docker' AND core_url<>'' AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='microsandbox' AND core_url<>'' AND idle_seconds>0 AND retention_seconds>0)
    ))
);
