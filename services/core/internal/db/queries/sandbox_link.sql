-- name: GetSandboxServeAuthority :one
SELECT tenant_id, environment_id, generation, credential_hash FROM sandbox_resources
WHERE kind = sqlc.arg(kind) AND id = sqlc.arg(id) AND live;

-- name: GetAgentHostCredential :one
SELECT COALESCE(credential_hash, '')::text AS credential_hash, credential_revision FROM devices
WHERE id = $1 AND agent_host AND revoked_at IS NULL;

-- name: GetLinkAssignment :one
-- The assignment with its Runtime's Attach authority and the live Link
-- resource of its Session's Environment.
SELECT b.session_id, b.runtime_id, b.epoch, b.desired_state = 'bound' AS bound,
    d.agent_host AND d.revoked_at IS NULL AS agent_host, d.credential_revision,
    r.tenant_id AS resource_tenant_id, r.environment_id AS resource_environment_id, r.kind AS resource_kind,
    r.id AS resource_id, r.generation AS resource_generation
FROM session_runtime_assignments b
JOIN devices d ON d.id = b.runtime_id
LEFT JOIN environments e ON e.session_id = b.session_id
LEFT JOIN sandbox_resources r ON r.environment_id = e.id AND r.live
WHERE b.assignment_id = $1;
