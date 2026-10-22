-- name: ListRuntimeSuspensionInFlightNodes :many
SELECT DISTINCT a.node_id
FROM runtime_allocations a
WHERE a.node_id IS NOT NULL AND a.state <> 'released'
  AND (a.compute_phase IN ('quiescing', 'suspending')
       OR (a.compute_retained_until <= clock_timestamp() AND a.compute_phase <> 'disabled'));

-- name: ListRuntimeStartingNodes :many
-- A node is starting a Session while that Session's sandbox is not created or
-- its initialization has not finished.
SELECT DISTINCT p.node_id
FROM runtime_placements p JOIN environments e ON e.id=p.environment_id
LEFT JOIN runtime_allocations a ON a.environment_id=p.environment_id AND a.state <> 'released'
WHERE p.released_at IS NULL AND (a.id IS NULL OR e.initialization IN ('pending', 'running'));

-- name: ListWaitingCheckpointRestores :many
SELECT DISTINCT a.node_id, a.deployment_generation, (a.compute_state->'snapshot'->'Compatibility')::jsonb AS checkpoint
FROM runtime_allocations a JOIN environments e ON e.id=a.environment_id JOIN sessions s ON s.id=e.session_id
WHERE a.node_id IS NOT NULL AND a.state='running' AND a.compute_phase='suspended'
 AND a.compute_retained_until>clock_timestamp() AND s.deleted_at IS NULL AND e.status NOT IN ('failed','expired')
 AND a.compute_state->'snapshot'->'Compatibility' IS NOT NULL
 AND (a.compute_wake_requested OR EXISTS(SELECT 1 FROM environment_input_reservations r WHERE r.session_id=s.id AND r.state='pending' AND r.deadline>clock_timestamp()));

-- name: ListIdleRuntimeCheckpoints :many
SELECT a.id
FROM runtime_allocations a JOIN environments e ON e.id=a.environment_id
WHERE a.node_id=$1 AND a.state='running' AND a.compute_phase='suspended' AND a.compute_retained_until>clock_timestamp()
 AND NOT a.compute_wake_requested
 AND NOT EXISTS(SELECT 1 FROM environment_input_reservations r WHERE r.session_id=e.session_id AND r.state='pending')
 AND NOT EXISTS(SELECT 1 FROM environment_file_writes f WHERE f.environment_id=e.id AND f.state='pending')
ORDER BY a.compute_retained_until, a.id;

-- name: ExpireRuntimeCheckpoint :one
UPDATE runtime_allocations
SET compute_retained_until=clock_timestamp(), compute_revision=compute_revision+1
WHERE id=$1 AND compute_revision=$2 AND state='running' AND compute_phase IN ('suspended','restoring') AND compute_retained_until>clock_timestamp()
RETURNING *;
