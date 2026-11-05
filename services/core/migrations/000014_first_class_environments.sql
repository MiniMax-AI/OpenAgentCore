-- +goose Up
-- Both views join environments.session_id; they are recreated at the end.
DROP VIEW runtime_device_authority;
DROP VIEW runtime_reset_retained_environments;

-- The Environment owns its Project, frozen configuration and, for a standalone Environment, its creation identity.
ALTER TABLE environments
    ADD COLUMN tenant_id uuid,
    ADD COLUMN configuration jsonb,
    ADD COLUMN session_owned boolean NOT NULL DEFAULT true,
    ADD COLUMN idempotency_key text,
    ADD COLUMN request_hash text,
    ADD COLUMN creator_kind text,
    ADD COLUMN creator_id text;
UPDATE environments e SET tenant_id = s.tenant_id, configuration = s.configuration->'environment'
FROM sessions s WHERE s.id = e.session_id;

-- A Session names its Environment; at most one live Session is attached to one.
ALTER TABLE sessions ADD COLUMN environment_id uuid REFERENCES environments(id);
UPDATE sessions s SET environment_id = e.id FROM environments e WHERE e.session_id = s.id;
UPDATE sessions SET configuration = configuration - 'environment';
CREATE UNIQUE INDEX sessions_attached_environment ON sessions (environment_id) WHERE deleted_at IS NULL;

-- Preparation input is keyed by Environment.
ALTER TABLE environment_setups DROP CONSTRAINT environment_setups_session_id_fkey;
UPDATE environment_setups f SET session_id = e.id FROM environments e WHERE e.session_id = f.session_id;
ALTER TABLE environment_setups RENAME COLUMN session_id TO environment_id;
ALTER TABLE environment_setups ADD FOREIGN KEY (environment_id) REFERENCES environments(id);
ALTER TABLE initial_environment_files DROP CONSTRAINT initial_environment_files_session_id_fkey;
UPDATE initial_environment_files f SET session_id = e.id FROM environments e WHERE e.session_id = f.session_id;
ALTER TABLE initial_environment_files RENAME COLUMN session_id TO environment_id;
ALTER TABLE initial_environment_files RENAME CONSTRAINT initial_environment_files_session_id_path_key TO initial_environment_files_environment_id_path_key;
ALTER TABLE initial_environment_files RENAME CONSTRAINT initial_environment_files_session_id_position_key TO initial_environment_files_environment_id_position_key;
ALTER TABLE initial_environment_files ADD FOREIGN KEY (environment_id) REFERENCES environments(id);

-- Waiting input belongs to the Session.
ALTER TABLE environment_input_reservations DROP CONSTRAINT environment_input_reservations_session_id_fkey,
    ADD FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE environments DROP COLUMN session_id;
ALTER TABLE environments ALTER COLUMN tenant_id SET NOT NULL, ALTER COLUMN configuration SET NOT NULL, ALTER COLUMN session_owned DROP DEFAULT,
    ADD CONSTRAINT environments_configuration_check CHECK (jsonb_typeof(configuration) = 'object' AND configuration->>'type' IN ('openai_hosted', 'self_hosted')),
    -- A standalone Environment is hosted and has a complete creation identity; a Session-owned one has none.
    ADD CONSTRAINT environments_creation_identity CHECK (
        session_owned = (idempotency_key IS NULL)
        AND (idempotency_key IS NULL) = (request_hash IS NULL)
        AND (idempotency_key IS NULL) = (creator_kind IS NULL)
        AND (idempotency_key IS NULL) = (creator_id IS NULL)
        AND (session_owned OR configuration->>'type' = 'openai_hosted')
        AND (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$')
        AND (creator_kind IS NULL OR creator_kind IN ('user', 'service_account'))
        AND (creator_id IS NULL OR creator_id <> '')),
    ADD CONSTRAINT environments_tenant_id_idempotency_key_key UNIQUE (tenant_id, idempotency_key);
CREATE INDEX environments_standalone_list ON environments (tenant_id, created_at, id) WHERE NOT session_owned;

-- A hosted device stays authorized while its Environment is not reclaimed.
CREATE VIEW runtime_device_authority AS
SELECT d.id, d.tenant_id, d.name, d.environment_id, COALESCE(c.token_sha256, d.credential_hash) AS credential_hash
FROM devices d
LEFT JOIN environments e ON e.id = d.environment_id AND e.tenant_id = d.tenant_id
LEFT JOIN sessions s ON s.environment_id = e.id AND s.deleted_at IS NULL
LEFT JOIN environment_executor_credentials c ON c.key_id = d.executor_key_id
WHERE d.revoked_at IS NULL
  AND (d.environment_id IS NULL OR (e.id IS NOT NULL AND (s.id IS NOT NULL OR NOT e.session_owned)))
  AND (d.executor_key_id IS NULL OR (c.revoked_at IS NULL AND c.tenant_id = s.tenant_id
       AND c.subject_kind = s.creator_kind AND c.subject_id = s.creator_id
       AND (c.environment_id IS NULL OR c.environment_id = e.id)
       AND e.configuration->>'type' = 'self_hosted' AND e.status NOT IN ('failed', 'expired')
       AND EXISTS (SELECT 1 FROM execution_project_scopes p WHERE p.tenant_id = c.tenant_id)));

-- session_id is the attached live Session, null when none is attached.
CREATE VIEW runtime_reset_retained_environments AS
SELECT e.id AS environment_id, s.id AS session_id, e.tenant_id, a.deployment_generation
FROM environments e
LEFT JOIN sessions s ON s.environment_id = e.id AND s.deleted_at IS NULL
JOIN environment_workspaces w ON w.environment_id = e.id AND w.state = 'ready'
JOIN LATERAL (
    SELECT allocation.state, allocation.provider_key, allocation.created_at, allocation.deployment_generation
    FROM runtime_allocations allocation
    WHERE allocation.environment_id = e.id
    ORDER BY allocation.created_at DESC, allocation.id DESC
    LIMIT 1) a ON true
CROSS JOIN runtime_deployment d
WHERE d.reset_clear IS NOT NULL AND d.reset_requested_at IS NOT NULL
  AND (s.id IS NOT NULL OR NOT e.session_owned)
  AND e.created_at <= d.reset_requested_at
  AND e.status NOT IN ('failed', 'expired') AND e.initialization = 'complete'
  AND e.configuration->>'type' = 'openai_hosted'
  AND a.state = 'released' AND a.provider_key = d.installation_id
  AND a.deployment_generation <= d.generation AND a.created_at <= d.reset_requested_at
  AND NOT EXISTS (SELECT 1 FROM runtime_placements p WHERE p.environment_id = e.id AND p.released_at IS NULL);
