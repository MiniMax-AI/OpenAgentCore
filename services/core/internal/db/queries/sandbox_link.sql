-- name: GetSandboxServeAuthority :one
SELECT tenant_id, environment_id, kind, generation, credential_hash FROM sandbox_resources
WHERE id = $1 AND live;

-- name: ListLiveSandboxResources :many
-- Every live Link resource. Quiesced compute is between a quiesce and the
-- wake that resumes it.
SELECT r.tenant_id, r.environment_id, r.kind, r.id, r.generation,
    COALESCE(a.compute_phase NOT IN ('disabled', 'running'), false)::boolean AS quiesced
FROM sandbox_resources r
LEFT JOIN runtime_allocations a ON r.kind = 'allocation' AND a.id = r.id
WHERE r.live;

-- name: GetEnvironmentResource :one
-- The Environment's live Link resource. An Environment has at most one Link
-- resource: its allocation's or its enrollment's.
SELECT kind, id, generation, credential_hash FROM sandbox_resources
WHERE tenant_id = $1 AND environment_id = $2 AND live;

-- name: GetAgentHostCredential :one
SELECT credential_hash, credential_revision FROM devices
WHERE id = $1 AND revoked_at IS NULL;

-- name: GetLinkAssignment :one
-- The assignment with its Runtime's Attach authority, the live Link resource
-- of its Session's Environment and that Environment's frozen configuration and network access.
SELECT b.session_id, b.runtime_id, b.epoch, b.desired_state = 'bound' AS bound,
    (d.revoked_at IS NULL)::boolean AS agent_host, d.credential_revision,
    r.tenant_id AS resource_tenant_id, r.environment_id AS resource_environment_id, r.kind AS resource_kind,
    r.id AS resource_id, r.generation AS resource_generation,
    (s.configuration->'environment')::jsonb AS environment_configuration,
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
-- authenticated; a revocation stays.
INSERT INTO devices (id, name, credential_hash)
VALUES ($1, 'agent-host', sqlc.arg(credential_hash))
ON CONFLICT (id) DO UPDATE SET credential_hash = EXCLUDED.credential_hash,
    credential_revision = devices.credential_revision + (devices.credential_hash IS DISTINCT FROM EXCLUDED.credential_hash)::int
RETURNING id;
