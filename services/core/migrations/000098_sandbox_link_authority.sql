-- +goose Up
-- Agent hosts are deployment identities. Existing assignments cannot be
-- transferred to them without losing native execution continuity.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM session_runtime_assignments b
    JOIN sessions s ON s.id = b.session_id
    WHERE b.desired_state = 'bound' AND s.deleted_at IS NULL
  ) THEN
    RAISE EXCEPTION 'Sessions have bound Runtime assignments; delete those Sessions, then upgrade';
  END IF;
END $$;
-- +goose StatementEnd

DROP VIEW runtime_device_authority;
-- These UUIDs identify historical native children and immutable write requests,
-- not current execution authority. Preserve them without registry foreign keys.
ALTER TABLE subagent_identities DROP CONSTRAINT subagent_identities_device_id_fkey;
ALTER TABLE environment_file_writes DROP CONSTRAINT environment_file_writes_device_id_fkey;
-- Allocations retain Provider cleanup ownership independently of a Runtime.
ALTER TABLE runtime_allocations
  DROP COLUMN device_id,
  ADD COLUMN serve_credential_hash text CHECK (serve_credential_hash ~ '^[0-9a-f]{64}$'),
  ADD COLUMN serve_generation bigint NOT NULL DEFAULT 1 CHECK (serve_generation > 0);
DELETE FROM session_runtime_assignments;
DELETE FROM devices;
ALTER TABLE devices
  DROP CONSTRAINT device_credential_source,
  DROP COLUMN tenant_id,
  DROP COLUMN environment_id,
  DROP COLUMN executor_key_id,
  DROP COLUMN archive_cancel_turn_id,
  ALTER COLUMN credential_hash SET NOT NULL,
  ADD COLUMN credential_revision bigint NOT NULL DEFAULT 1 CHECK (credential_revision > 0);

CREATE TABLE sandbox_enrollments (
  id uuid PRIMARY KEY,
  environment_id uuid NOT NULL UNIQUE REFERENCES environments(id),
  executor_key_id uuid NOT NULL REFERENCES environment_executor_credentials(key_id),
  generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Every Link resource with its Serve credential hash. A resource is live
-- while that credential may Serve it.
CREATE VIEW sandbox_resources AS
SELECT s.tenant_id, a.environment_id, 'allocation'::text AS kind, a.id, a.serve_generation AS generation,
  a.serve_credential_hash AS credential_hash,
  a.state IN ('creating', 'running') AND s.deleted_at IS NULL AS live
FROM runtime_allocations a
JOIN environments e ON e.id = a.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE a.serve_credential_hash IS NOT NULL
UNION ALL
SELECT s.tenant_id, n.environment_id, 'enrollment'::text, n.id, n.generation, c.token_sha256,
  c.revoked_at IS NULL AND c.tenant_id = s.tenant_id AND c.subject_kind = s.creator_kind AND c.subject_id = s.creator_id
    AND (c.environment_id IS NULL OR c.environment_id = e.id) AND s.deleted_at IS NULL
    AND e.status NOT IN ('failed', 'expired') AND s.configuration->'environment'->>'type' = 'self_hosted'
    AND EXISTS (SELECT 1 FROM execution_project_scopes p WHERE p.tenant_id = c.tenant_id)
FROM sandbox_enrollments n
JOIN environments e ON e.id = n.environment_id
JOIN sessions s ON s.id = e.session_id
JOIN environment_executor_credentials c ON c.key_id = n.executor_key_id;

-- +goose Down
-- Restoring the old device references cannot invent identities or discard
-- durable history, enrollment authority, uncertain writes or pending cleanup.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM subagent_identities)
    OR EXISTS (SELECT 1 FROM environment_file_writes)
    OR EXISTS (SELECT 1 FROM runtime_allocations)
    OR EXISTS (SELECT 1 FROM sandbox_enrollments)
    OR EXISTS (
      SELECT 1 FROM session_runtime_assignments b JOIN sessions s ON s.id = b.session_id
      WHERE s.deleted_at IS NULL OR b.desired_state <> 'released' OR b.applied_epoch <> b.epoch
    ) THEN
    RAISE EXCEPTION 'Cannot restore device constraints while Runtime history or cleanup is retained';
  END IF;
END $$;
-- +goose StatementEnd
DROP VIEW sandbox_resources;
DELETE FROM session_runtime_assignments;
DELETE FROM devices;
ALTER TABLE devices
  DROP COLUMN credential_revision,
  ADD COLUMN tenant_id uuid NOT NULL,
  ADD COLUMN environment_id uuid UNIQUE REFERENCES environments(id),
  ADD COLUMN executor_key_id uuid REFERENCES environment_executor_credentials(key_id),
  ADD COLUMN archive_cancel_turn_id uuid REFERENCES turns(id) ON DELETE SET NULL,
  ALTER COLUMN credential_hash DROP NOT NULL,
  ADD CONSTRAINT device_credential_source CHECK (
    (executor_key_id IS NULL AND credential_hash IS NOT NULL)
    OR (executor_key_id IS NOT NULL AND credential_hash IS NULL AND environment_id IS NOT NULL)
  );
CREATE INDEX devices_tenant_idx ON devices (tenant_id);
ALTER TABLE subagent_identities ADD CONSTRAINT subagent_identities_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices(id);
ALTER TABLE environment_file_writes ADD CONSTRAINT environment_file_writes_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices(id);
DROP TABLE sandbox_enrollments;
ALTER TABLE runtime_allocations
  DROP COLUMN serve_generation,
  DROP COLUMN serve_credential_hash,
  ADD COLUMN device_id uuid NOT NULL UNIQUE REFERENCES devices(id);

CREATE VIEW runtime_device_authority AS
SELECT d.id, d.tenant_id, d.name, d.environment_id,
    COALESCE(c.token_sha256, d.credential_hash) AS credential_hash
FROM devices d
LEFT JOIN environments e ON e.id = d.environment_id
LEFT JOIN sessions s ON s.id = e.session_id AND s.tenant_id = d.tenant_id
LEFT JOIN environment_executor_credentials c ON c.key_id = d.executor_key_id
WHERE d.revoked_at IS NULL
    AND (d.environment_id IS NULL OR (s.id IS NOT NULL AND s.deleted_at IS NULL))
    AND (d.executor_key_id IS NULL OR (
        c.revoked_at IS NULL AND c.tenant_id = s.tenant_id
        AND c.subject_kind = s.creator_kind AND c.subject_id = s.creator_id
        AND (c.environment_id IS NULL OR c.environment_id = e.id)
        AND s.configuration->'environment'->>'type' = 'self_hosted'
        AND e.status NOT IN ('failed', 'expired')
        AND EXISTS (SELECT 1 FROM execution_project_scopes p WHERE p.tenant_id = c.tenant_id)
    ));

