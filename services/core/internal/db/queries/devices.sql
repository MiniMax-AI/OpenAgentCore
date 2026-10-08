-- name: CreateDevice :one
INSERT INTO devices (id, tenant_id, name, credential_hash)
VALUES ($1, $2, $3, $4) RETURNING id;

-- name: GetAgentHost :one
-- An agent host that may run the tenant's Sessions: the deployment's own or
-- one of the tenant's.
SELECT id, name FROM devices
WHERE id = $2 AND agent_host AND revoked_at IS NULL AND (tenant_id IS NULL OR tenant_id = $1);

-- name: GetDeviceCredential :one
SELECT d.id, d.name, d.credential_hash, COALESCE(a.node_id::text, '')::text AS runtime_node_id, COALESCE(a.id::text, '')::text AS runtime_allocation_id
FROM runtime_device_authority d
LEFT JOIN runtime_allocations a ON a.device_id = d.id
WHERE d.id = $1;

-- name: RevokeDevice :execrows
UPDATE devices SET revoked_at = COALESCE(revoked_at, clock_timestamp()), archive_cancel_turn_id = NULL
WHERE tenant_id = $1 AND id = $2;

-- name: SettleRevokedRuntimeReleases :exec
-- No Runtime is left to act on a revoked device's releases, so revocation
-- settles them. It runs after the revocation in the same transaction: the
-- revocation holds the device row, which a release locks before it reads the
-- device's authority, so every release is either seen here or sees the
-- revocation.
UPDATE session_runtime_assignments SET applied_epoch = epoch
WHERE runtime_id = $1 AND desired_state = 'released';

-- name: TouchDevice :execrows
UPDATE devices SET last_seen_at = clock_timestamp()
WHERE devices.id = $1 AND EXISTS (SELECT 1 FROM runtime_device_authority a WHERE a.id = devices.id);

-- name: BindSessionDevice :one
-- Binds the Session to an agent host. A Session with an Environment binds
-- only while its Environment has a live Link resource, through which the
-- agent host reaches the sandbox.
INSERT INTO session_runtime_assignments (session_id, runtime_id)
SELECT s.id, d.id FROM sessions s JOIN devices d ON d.agent_host AND (d.tenant_id IS NULL OR d.tenant_id = s.tenant_id)
WHERE s.tenant_id = $1 AND s.id = $2 AND d.id = $3 AND d.revoked_at IS NULL
AND (NOT EXISTS (SELECT 1 FROM environments e WHERE e.session_id = s.id) OR EXISTS (
    SELECT 1 FROM environments e JOIN sandbox_resources r ON r.environment_id = e.id
    WHERE e.session_id = s.id AND r.live
))
ON CONFLICT (session_id) DO UPDATE SET runtime_id = session_runtime_assignments.runtime_id
WHERE session_runtime_assignments.runtime_id = EXCLUDED.runtime_id AND session_runtime_assignments.desired_state = 'bound'
RETURNING runtime_id;

-- name: GetSessionDevice :one
-- The Session's bound Runtime: a device of its tenant or the deployment's
-- agent host, with the Session's Environment, which the assignment binds.
SELECT d.id, d.name, e.id AS session_environment_id, b.assignment_id, b.epoch FROM session_runtime_assignments b
JOIN sessions s ON s.id = b.session_id
JOIN devices d ON d.id = b.runtime_id AND (d.tenant_id = s.tenant_id OR (d.agent_host AND d.tenant_id IS NULL))
LEFT JOIN environments e ON e.session_id = s.id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.revoked_at IS NULL AND b.desired_state = 'bound'
AND EXISTS (SELECT 1 FROM runtime_device_authority a WHERE a.id = d.id);

-- name: GetSessionExecutionBinding :one
-- The bound Runtime as GetSessionDevice reads it, with the native session.
SELECT d.id, d.name, b.native_session_id, e.id AS session_environment_id, b.assignment_id, b.epoch,
    EXISTS (SELECT 1 FROM turns t WHERE t.session_id = s.id AND t.started_at IS NOT NULL) AS has_started_turn
FROM session_runtime_assignments b
JOIN sessions s ON s.id = b.session_id
JOIN devices d ON d.id = b.runtime_id AND (d.tenant_id = s.tenant_id OR (d.agent_host AND d.tenant_id IS NULL))
LEFT JOIN environments e ON e.session_id = s.id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.revoked_at IS NULL AND b.desired_state = 'bound'
AND EXISTS (SELECT 1 FROM runtime_device_authority a WHERE a.id = d.id);

-- name: RememberNativeSession :execrows
UPDATE session_runtime_assignments SET native_session_id = $2 WHERE session_id = $1;

-- name: LockAssignmentRuntime :exec
-- Locks the device row of the Session's Runtime before a release reads its
-- authority; see SettleRevokedRuntimeReleases.
SELECT 1 FROM session_runtime_assignments b JOIN devices d ON d.id = b.runtime_id
WHERE b.session_id = $1 FOR SHARE OF d;

-- name: ReleaseSessionAssignment :exec
-- An identical release keeps its epoch; a release that adds home removal
-- advances it. A release whose Runtime has no authority is settled, since no
-- Runtime can act on it.
UPDATE session_runtime_assignments b
SET desired_state = 'released', epoch = b.epoch + 1, remove_home = b.remove_home OR sqlc.arg(remove_home)::boolean,
    applied_epoch = CASE WHEN EXISTS (SELECT 1 FROM runtime_device_authority a WHERE a.id = b.runtime_id) THEN b.applied_epoch ELSE b.epoch + 1 END
WHERE b.session_id = sqlc.arg(session_id) AND (b.desired_state = 'bound' OR (sqlc.arg(remove_home)::boolean AND NOT b.remove_home));

-- name: ListPendingAssignmentReleases :many
-- Each release carries its Session's Link resource, live or not, which the
-- release revokes before it is sent.
SELECT b.session_id, b.runtime_id, b.assignment_id, b.epoch, b.remove_home,
    r.tenant_id AS resource_tenant_id, r.environment_id AS resource_environment_id, r.kind AS resource_kind,
    r.id AS resource_id, r.generation AS resource_generation
FROM session_runtime_assignments b
LEFT JOIN environments e ON e.session_id = b.session_id
LEFT JOIN sandbox_resources r ON r.environment_id = e.id
WHERE b.desired_state = 'released' AND b.applied_epoch < b.epoch AND b.runtime_id = ANY(sqlc.arg(runtime_ids)::uuid[])
ORDER BY b.runtime_id, b.session_id;

-- name: AcknowledgeAssignmentRelease :execrows
UPDATE session_runtime_assignments SET applied_epoch = epoch
WHERE session_id = $1 AND assignment_id = $2 AND epoch = $3 AND desired_state = 'released';
