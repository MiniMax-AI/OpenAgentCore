-- name: CreateDevice :one
INSERT INTO devices (id, tenant_id, name, credential_hash)
VALUES ($1, $2, $3, $4) RETURNING id;

-- name: GetDevice :one
SELECT id, name FROM devices WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL;

-- name: GetDeviceCredential :one
SELECT d.id, d.name, d.credential_hash, COALESCE(a.node_id::text, '')::text AS runtime_node_id, COALESCE(a.id::text, '')::text AS runtime_allocation_id
FROM runtime_device_authority d
LEFT JOIN runtime_allocations a ON a.device_id = d.id
WHERE d.id = $1;

-- name: RevokeDevice :execrows
UPDATE devices SET revoked_at = COALESCE(revoked_at, clock_timestamp()), archive_cancel_turn_id = NULL
WHERE tenant_id = $1 AND id = $2;

-- name: TouchDevice :execrows
UPDATE devices SET last_seen_at = clock_timestamp()
WHERE devices.id = $1 AND EXISTS (SELECT 1 FROM runtime_device_authority a WHERE a.id = devices.id);

-- name: BindSessionDevice :one
INSERT INTO session_runtime_assignments (session_id, runtime_id)
SELECT s.id, d.id FROM sessions s JOIN devices d ON d.tenant_id = s.tenant_id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.id = $3 AND d.revoked_at IS NULL
AND (d.environment_id IS NULL OR EXISTS (
    SELECT 1 FROM environments e WHERE e.id = d.environment_id AND e.session_id = s.id
))
ON CONFLICT (session_id) DO UPDATE SET runtime_id = session_runtime_assignments.runtime_id
WHERE session_runtime_assignments.runtime_id = EXCLUDED.runtime_id AND session_runtime_assignments.desired_state = 'bound'
RETURNING runtime_id;

-- name: GetSessionDevice :one
SELECT d.id, d.name, d.environment_id, b.assignment_id, b.epoch FROM session_runtime_assignments b
JOIN sessions s ON s.id = b.session_id
JOIN devices d ON d.id = b.runtime_id AND d.tenant_id = s.tenant_id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.revoked_at IS NULL AND b.desired_state = 'bound'
AND EXISTS (SELECT 1 FROM runtime_device_authority a WHERE a.id = d.id)
AND (d.environment_id IS NULL OR EXISTS (
    SELECT 1 FROM environments e WHERE e.id = d.environment_id AND e.session_id = s.id
));

-- name: GetSessionExecutionBinding :one
SELECT d.id, d.name, b.native_session_id, d.environment_id, b.assignment_id, b.epoch,
    EXISTS (SELECT 1 FROM turns t WHERE t.session_id = s.id AND t.started_at IS NOT NULL) AS has_started_turn
FROM session_runtime_assignments b
JOIN sessions s ON s.id = b.session_id
JOIN devices d ON d.id = b.runtime_id AND d.tenant_id = s.tenant_id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.revoked_at IS NULL AND b.desired_state = 'bound'
AND EXISTS (SELECT 1 FROM runtime_device_authority a WHERE a.id = d.id)
AND (d.environment_id IS NULL OR EXISTS (
    SELECT 1 FROM environments e WHERE e.id = d.environment_id AND e.session_id = s.id
));

-- name: RememberNativeSession :execrows
UPDATE session_runtime_assignments SET native_session_id = $2 WHERE session_id = $1;

-- name: ReleaseSessionAssignment :exec
-- An identical release keeps its epoch; a release that adds home removal
-- advances it.
UPDATE session_runtime_assignments
SET desired_state = 'released', epoch = epoch + 1, remove_home = remove_home OR sqlc.arg(remove_home)::boolean
WHERE session_id = sqlc.arg(session_id) AND (desired_state = 'bound' OR (sqlc.arg(remove_home)::boolean AND NOT remove_home));

-- name: ListPendingAssignmentReleases :many
SELECT session_id, runtime_id, assignment_id, epoch, remove_home FROM session_runtime_assignments
WHERE desired_state = 'released' AND applied_epoch < epoch AND runtime_id = ANY(sqlc.arg(runtime_ids)::uuid[])
ORDER BY runtime_id, session_id;

-- name: AcknowledgeAssignmentRelease :execrows
UPDATE session_runtime_assignments SET applied_epoch = epoch
WHERE session_id = $1 AND assignment_id = $2 AND epoch = $3 AND desired_state = 'released';
