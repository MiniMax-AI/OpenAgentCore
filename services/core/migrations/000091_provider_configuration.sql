-- +goose Up
-- Copy only public selectors/observations. Ciphertext and its installation/generation
-- associated data are unchanged; credentials are never decoded by SQL.
ALTER TABLE runtime_deployment RENAME COLUMN e2b_credential TO provider_credential;
ALTER TABLE runtime_deployment ADD COLUMN provider_config jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(provider_config)='object');
ALTER TABLE runtime_deployment ADD COLUMN provider_metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(provider_metadata)='object');
UPDATE runtime_deployment SET
provider_config=jsonb_build_object('template',e2b_template,'api_url',e2b_api_url,'domain',e2b_domain),
provider_metadata=jsonb_build_object('template_build',jsonb_build_object('status',e2b_template_build_status,'resources',
 jsonb_build_object('cpus',e2b_template_cpus,'memory_mib',e2b_template_memory_mib,'root_disk_mib',e2b_template_root_disk_mib)))
WHERE e2b_template<>'';
ALTER TABLE runtime_deployment DROP COLUMN e2b_template;
ALTER TABLE runtime_deployment DROP COLUMN e2b_api_url;
ALTER TABLE runtime_deployment DROP COLUMN e2b_domain;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template_build_status;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template_cpus;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template_memory_mib;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template_root_disk_mib;
ALTER TABLE runtime_deployment_generations ADD COLUMN provider_config jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(provider_config)='object');
ALTER TABLE runtime_deployment_generations ADD COLUMN provider_metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(provider_metadata)='object');
ALTER TABLE runtime_deployment_generations DISABLE TRIGGER immutable_sandbox_specification;
UPDATE runtime_deployment_generations SET
provider_config=jsonb_build_object('template',e2b_template,'api_url',e2b_api_url,'domain',e2b_domain),
provider_metadata=jsonb_build_object('template_build',jsonb_build_object('status',e2b_template_build_status,'resources',
 jsonb_build_object('cpus',e2b_template_cpus,'memory_mib',e2b_template_memory_mib,'root_disk_mib',e2b_template_root_disk_mib)))
WHERE e2b_template<>'';
ALTER TABLE runtime_deployment_generations ENABLE TRIGGER immutable_sandbox_specification;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_template;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_api_url;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_domain;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_template_build_status;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_template_cpus;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_template_memory_mib;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_template_root_disk_mib;

-- +goose Down
-- Reject a lossy downgrade; no unknown provider configuration may be discarded.
-- +goose StatementBegin
DO $$ BEGIN
IF EXISTS (SELECT 1 FROM runtime_deployment WHERE provider_kind NOT IN ('','docker','microsandbox','e2b'))
 OR EXISTS (SELECT 1 FROM runtime_deployment_generations WHERE provider_kind NOT IN ('docker','microsandbox','e2b')) THEN
 RAISE EXCEPTION 'Cannot downgrade provider configurations unknown to the previous schema';
END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
IF EXISTS (SELECT 1 FROM runtime_deployment WHERE
 (provider_kind<>'e2b' AND (provider_config<>'{}'::jsonb OR provider_metadata<>'{}'::jsonb)) OR
 (provider_kind='e2b' AND (provider_config - ARRAY['template','api_url','domain'] <> '{}'::jsonb OR
 provider_metadata - 'template_build' <> '{}'::jsonb OR
 COALESCE(provider_metadata->'template_build','{}'::jsonb) - ARRAY['status','resources'] <> '{}'::jsonb OR
 COALESCE(provider_metadata#>'{template_build,resources}','{}'::jsonb) - ARRAY['cpus','memory_mib','root_disk_mib'] <> '{}'::jsonb))) THEN
 RAISE EXCEPTION 'Cannot downgrade provider configuration without losing fields';
END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_deployment ADD COLUMN e2b_template text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment ADD COLUMN e2b_api_url text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment ADD COLUMN e2b_domain text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment ADD COLUMN e2b_template_build_status text;
ALTER TABLE runtime_deployment ADD COLUMN e2b_template_cpus integer;
ALTER TABLE runtime_deployment ADD COLUMN e2b_template_memory_mib integer;
ALTER TABLE runtime_deployment ADD COLUMN e2b_template_root_disk_mib integer;
UPDATE runtime_deployment SET
 e2b_template=COALESCE(provider_config->>'template',''),e2b_api_url=COALESCE(provider_config->>'api_url',''),e2b_domain=COALESCE(provider_config->>'domain',''),
 e2b_template_build_status=provider_metadata#>>'{template_build,status}',
 e2b_template_cpus=(provider_metadata#>>'{template_build,resources,cpus}')::integer,
 e2b_template_memory_mib=(provider_metadata#>>'{template_build,resources,memory_mib}')::integer,
 e2b_template_root_disk_mib=(provider_metadata#>>'{template_build,resources,root_disk_mib}')::integer;
ALTER TABLE runtime_deployment DROP COLUMN provider_config, DROP COLUMN provider_metadata;
-- +goose StatementBegin
DO $$ BEGIN
IF EXISTS (SELECT 1 FROM runtime_deployment_generations WHERE
 (provider_kind<>'e2b' AND (provider_config<>'{}'::jsonb OR provider_metadata<>'{}'::jsonb)) OR
 (provider_kind='e2b' AND (provider_config - ARRAY['template','api_url','domain'] <> '{}'::jsonb OR
 provider_metadata - 'template_build' <> '{}'::jsonb OR
 COALESCE(provider_metadata->'template_build','{}'::jsonb) - ARRAY['status','resources'] <> '{}'::jsonb OR
 COALESCE(provider_metadata#>'{template_build,resources}','{}'::jsonb) - ARRAY['cpus','memory_mib','root_disk_mib'] <> '{}'::jsonb))) THEN
 RAISE EXCEPTION 'Cannot downgrade provider configuration without losing fields';
END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_template text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_api_url text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_domain text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_template_build_status text;
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_template_cpus integer;
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_template_memory_mib integer;
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_template_root_disk_mib integer;
ALTER TABLE runtime_deployment_generations DISABLE TRIGGER immutable_sandbox_specification;
UPDATE runtime_deployment_generations SET
 e2b_template=COALESCE(provider_config->>'template',''),e2b_api_url=COALESCE(provider_config->>'api_url',''),e2b_domain=COALESCE(provider_config->>'domain',''),
 e2b_template_build_status=provider_metadata#>>'{template_build,status}',
 e2b_template_cpus=(provider_metadata#>>'{template_build,resources,cpus}')::integer,
 e2b_template_memory_mib=(provider_metadata#>>'{template_build,resources,memory_mib}')::integer,
 e2b_template_root_disk_mib=(provider_metadata#>>'{template_build,resources,root_disk_mib}')::integer;
ALTER TABLE runtime_deployment_generations ENABLE TRIGGER immutable_sandbox_specification;
ALTER TABLE runtime_deployment_generations DROP COLUMN provider_config, DROP COLUMN provider_metadata;
ALTER TABLE runtime_deployment RENAME COLUMN provider_credential TO e2b_credential;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_check CHECK (
    (provider_kind <> '' AND e2b_template <> '' AND e2b_credential IS NOT NULL) OR
    (e2b_template = '' AND e2b_credential IS NULL)
);
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_template_build_check CHECK (
    e2b_template <> '' OR (e2b_template_build_status IS NULL AND e2b_template_cpus IS NULL AND
        e2b_template_memory_mib IS NULL AND e2b_template_root_disk_mib IS NULL)
);
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_endpoint_check CHECK (
    (e2b_api_url = '' AND e2b_domain = '') OR
    (e2b_template <> '' AND e2b_api_url <> '' AND e2b_domain <> '')
);
ALTER TABLE runtime_deployment_generations ADD CONSTRAINT runtime_deployment_generation_e2b_endpoint_check CHECK (
    (e2b_api_url = '' AND e2b_domain = '') OR
    (e2b_template <> '' AND e2b_api_url <> '' AND e2b_domain <> '')
);

