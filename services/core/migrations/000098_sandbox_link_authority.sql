-- +goose Up
-- Link authority. An allocation's Serve credential serves only its own
-- resource at serve_generation. A self_hosted enrollment's resource is served
-- with its executor credential while that key is authorized for the
-- Environment. Only a marked agent host may Attach; credential_revision fences
-- links that a rotated credential authenticated. An agent host may belong to
-- no tenant, such as the deployment's own; every other device belongs to one.
ALTER TABLE runtime_allocations
  ADD COLUMN serve_credential_hash text CHECK (serve_credential_hash ~ '^[0-9a-f]{64}$'),
  ADD COLUMN serve_generation bigint NOT NULL DEFAULT 1 CHECK (serve_generation > 0);

CREATE TABLE sandbox_enrollments (
  id uuid PRIMARY KEY,
  environment_id uuid NOT NULL UNIQUE REFERENCES environments(id),
  executor_key_id uuid NOT NULL REFERENCES environment_executor_credentials(key_id),
  generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE devices
  ADD COLUMN agent_host boolean NOT NULL DEFAULT false,
  ADD COLUMN credential_revision bigint NOT NULL DEFAULT 1 CHECK (credential_revision > 0),
  ALTER COLUMN tenant_id DROP NOT NULL,
  ADD CONSTRAINT devices_agent_host CHECK (CASE WHEN agent_host THEN environment_id IS NULL AND executor_key_id IS NULL ELSE tenant_id IS NOT NULL END);

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

-- Sessions run on an agent host. A Session bound to an in-sandbox or
-- user-managed Runtime cannot move, so the upgrade waits until those Sessions
-- are deleted.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM session_runtime_assignments b
    JOIN sessions s ON s.id = b.session_id
    JOIN devices d ON d.id = b.runtime_id
    WHERE b.desired_state = 'bound' AND s.deleted_at IS NULL AND NOT d.agent_host
  ) THEN
    RAISE EXCEPTION 'Sessions are bound to a Runtime that is not an agent host; delete those Sessions, then upgrade';
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP VIEW sandbox_resources;
DELETE FROM devices WHERE tenant_id IS NULL;
ALTER TABLE devices
  DROP CONSTRAINT devices_agent_host,
  ALTER COLUMN tenant_id SET NOT NULL,
  DROP COLUMN credential_revision,
  DROP COLUMN agent_host;
DROP TABLE sandbox_enrollments;
ALTER TABLE runtime_allocations
  DROP COLUMN serve_generation,
  DROP COLUMN serve_credential_hash;
