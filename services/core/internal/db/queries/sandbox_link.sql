-- name: GetSandboxServeAuthority :one
SELECT tenant_id, environment_id, kind, generation, credential_hash FROM sandbox_resources
WHERE id = $1 AND live;

-- name: GetAgentHostCredential :one
SELECT COALESCE(credential_hash, '')::text AS credential_hash, credential_revision FROM devices
WHERE id = $1 AND agent_host AND revoked_at IS NULL;

-- name: GetLinkAssignment :one
-- The assignment with its Runtime's Attach authority, the live Link resource
-- of its Session's Environment and that Environment's network access.
SELECT b.session_id, b.runtime_id, b.epoch, b.desired_state = 'bound' AS bound,
    (d.agent_host AND d.revoked_at IS NULL)::boolean AS agent_host, d.credential_revision,
    r.tenant_id AS resource_tenant_id, r.environment_id AS resource_environment_id, r.kind AS resource_kind,
    r.id AS resource_id, r.generation AS resource_generation,
    (COALESCE(s.configuration->'environment'->'network'->>'access', 'enabled') = 'enabled')::boolean AS network_enabled
FROM session_runtime_assignments b
JOIN sessions s ON s.id = b.session_id
JOIN devices d ON d.id = b.runtime_id
LEFT JOIN environments e ON e.session_id = b.session_id
LEFT JOIN sandbox_resources r ON r.environment_id = e.id AND r.live
WHERE b.assignment_id = $1;

-- name: RegisterAgentHost :one
-- The deployment's agent host, with no tenant, Environment or executor key.
-- A new credential advances the revision, which fences the Links the old one
-- authenticated; a revocation stays. No row means the ID belongs to a device
-- that is not an agent host.
INSERT INTO devices (id, name, credential_hash, agent_host)
VALUES ($1, 'agent-host', sqlc.arg(credential_hash), true)
ON CONFLICT (id) DO UPDATE SET credential_hash = EXCLUDED.credential_hash,
    credential_revision = devices.credential_revision + (devices.credential_hash IS DISTINCT FROM EXCLUDED.credential_hash)::int
WHERE devices.agent_host
RETURNING id;
