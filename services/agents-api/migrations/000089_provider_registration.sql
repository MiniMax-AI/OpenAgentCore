-- +goose Up
-- Provider registration owns supported kinds, modes and native configuration.
-- Persistence enforces shape and ownership without enumerating implementations.
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_provider_kind_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_provider_kind_check
    CHECK (provider_kind = '' OR provider_kind ~ '^[a-z][a-z0-9_-]{0,63}$');
ALTER TABLE runtime_deployment_generations DROP CONSTRAINT runtime_deployment_generations_provider_kind_check;
ALTER TABLE runtime_deployment_generations ADD CONSTRAINT runtime_deployment_generations_provider_kind_check
    CHECK (provider_kind ~ '^[a-z][a-z0-9_-]{0,63}$');
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind = '' AND mode = '' AND generation >= 0 AND idle_seconds = 0 AND retention_seconds = 0) OR
        (provider_kind <> '' AND mode IN ('nodes','direct') AND generation > 0 AND
            ((idle_seconds = 0 AND retention_seconds = 0) OR (idle_seconds > 0 AND retention_seconds > 0)))
    ))
);
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_e2b_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_check CHECK (
    (provider_kind <> '' AND e2b_template <> '' AND e2b_credential IS NOT NULL) OR
    (e2b_template = '' AND e2b_credential IS NULL)
);
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_e2b_template_build_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_template_build_check CHECK (
    e2b_template <> '' OR (e2b_template_build_status IS NULL AND e2b_template_cpus IS NULL AND
        e2b_template_memory_mib IS NULL AND e2b_template_root_disk_mib IS NULL)
);
ALTER TABLE runtime_deployment DROP CONSTRAINT IF EXISTS runtime_deployment_e2b_endpoint_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_endpoint_check CHECK (
    (e2b_api_url = '' AND e2b_domain = '') OR
    (e2b_template <> '' AND e2b_api_url <> '' AND e2b_domain <> '')
);
ALTER TABLE runtime_deployment_generations DROP CONSTRAINT runtime_deployment_generation_e2b_endpoint_check;
ALTER TABLE runtime_deployment_generations ADD CONSTRAINT runtime_deployment_generation_e2b_endpoint_check CHECK (
    (e2b_api_url = '' AND e2b_domain = '') OR
    (e2b_template <> '' AND e2b_api_url <> '' AND e2b_domain <> '')
);

-- +goose Down
-- Restore the previous representation of official endpoints. Custom endpoints
-- remain explicit so older downgrade guards still protect their ownership.
UPDATE runtime_deployment SET e2b_api_url = '', e2b_domain = ''
WHERE e2b_api_url = 'https://api.e2b.app' AND e2b_domain = 'e2b.app';
-- This transaction changes representation only, not the endpoint or resource owner.
ALTER TABLE runtime_deployment_generations DISABLE TRIGGER immutable_sandbox_specification;
UPDATE runtime_deployment_generations SET e2b_api_url = '', e2b_domain = ''
WHERE e2b_api_url = 'https://api.e2b.app' AND e2b_domain = 'e2b.app';
ALTER TABLE runtime_deployment_generations ENABLE TRIGGER immutable_sandbox_specification;
-- Narrowing registration cannot silently invalidate retained resource ownership.
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_provider_kind_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_provider_kind_check CHECK (provider_kind IN ('','docker','microsandbox','e2b'));
ALTER TABLE runtime_deployment_generations DROP CONSTRAINT runtime_deployment_generations_provider_kind_check;
ALTER TABLE runtime_deployment_generations ADD CONSTRAINT runtime_deployment_generations_provider_kind_check CHECK (provider_kind IN ('docker','microsandbox','e2b'));
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind='' AND mode='' AND generation>=0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='docker' AND mode='nodes' AND generation>0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='microsandbox' AND mode='nodes' AND generation>0 AND idle_seconds>0 AND retention_seconds>0) OR
        (provider_kind='e2b' AND mode='direct' AND generation>0 AND idle_seconds=0 AND retention_seconds=0)
    ))
);
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_e2b_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_check CHECK (
    (provider_kind='e2b' AND e2b_template<>'' AND e2b_credential IS NOT NULL) OR
    (provider_kind<>'e2b' AND e2b_template='' AND e2b_credential IS NULL)
);
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_e2b_template_build_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_template_build_check CHECK (
    provider_kind = 'e2b' OR (e2b_template_build_status IS NULL AND e2b_template_cpus IS NULL AND
        e2b_template_memory_mib IS NULL AND e2b_template_root_disk_mib IS NULL)
);
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_e2b_endpoint_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_endpoint_check CHECK (
    (provider_kind = 'e2b' AND ((e2b_api_url = '' AND e2b_domain = '') OR (e2b_api_url <> '' AND e2b_domain <> ''))) OR
    (provider_kind <> 'e2b' AND e2b_api_url = '' AND e2b_domain = '')
);
ALTER TABLE runtime_deployment_generations DROP CONSTRAINT runtime_deployment_generation_e2b_endpoint_check;
ALTER TABLE runtime_deployment_generations ADD CONSTRAINT runtime_deployment_generation_e2b_endpoint_check CHECK (
    (provider_kind = 'e2b' AND ((e2b_api_url = '' AND e2b_domain = '') OR (e2b_api_url <> '' AND e2b_domain <> ''))) OR
    (provider_kind <> 'e2b' AND e2b_api_url = '' AND e2b_domain = '')
);
