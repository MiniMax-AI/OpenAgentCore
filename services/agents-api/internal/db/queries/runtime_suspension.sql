-- name: SetRuntimeCompute :one
UPDATE runtime_allocations
SET compute_phase_changed_at = CASE WHEN compute_phase = sqlc.arg(phase)::text THEN compute_phase_changed_at ELSE clock_timestamp() END,
    compute_phase = sqlc.arg(phase), compute_state = sqlc.arg(state)::jsonb,
    compute_revision = compute_revision + 1,
    compute_retained_until = sqlc.narg(retained_until),
    kept_at = CASE WHEN sqlc.arg(phase)::text = 'running' THEN clock_timestamp() ELSE kept_at END
WHERE runtime_allocations.id = sqlc.arg(id) AND compute_revision = sqlc.arg(revision)
    AND state = 'running' AND EXISTS (SELECT 1 FROM environments e WHERE e.id = runtime_allocations.environment_id AND e.initialization = 'complete')
    AND ((compute_phase IN ('disabled','running') AND (node_id IS NOT NULL OR (SELECT mode FROM runtime_deployment) = 'direct' OR kept_at > clock_timestamp() - interval '1 hour'))
      OR (compute_phase NOT IN ('disabled','running') AND compute_retained_until > clock_timestamp()))
RETURNING *;

-- name: TouchRuntimeActivity :exec
UPDATE runtime_allocations a
SET compute_activity_at = clock_timestamp(), compute_wake_requested = true
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE a.environment_id = e.id AND s.tenant_id = sqlc.arg(tenant_id)
    AND e.id = sqlc.arg(environment_id) AND s.deleted_at IS NULL
    AND a.state = 'running' AND a.compute_phase <> 'disabled';

-- name: ClearRuntimeWake :exec
UPDATE runtime_allocations
SET compute_wake_requested = false
WHERE id = $1 AND compute_phase = 'running' AND compute_activity_at <= $2;

-- name: GetRuntimeActivity :one
SELECT clock_timestamp()::timestamptz AS observed_at,
    GREATEST(a.compute_activity_at,
    CASE WHEN a.node_id IS NULL THEN COALESCE((SELECT max(t.completed_at) FROM turns t WHERE t.session_id = e.session_id), a.created_at) END,
    CASE WHEN a.node_id IS NULL THEN (SELECT max(t.completed_at) FROM subagent_turns t WHERE t.session_id = e.session_id) END,
    (SELECT max(f.settled_at) FROM environment_file_writes f WHERE f.environment_id = e.id))::timestamptz AS last_activity,
    (EXISTS (SELECT 1 FROM turns t WHERE t.session_id = e.session_id AND t.status IN ('queued','in_progress','waiting'))
     OR EXISTS (SELECT 1 FROM subagent_turns t WHERE t.session_id = e.session_id AND t.status IN ('queued','in_progress','waiting'))
     OR EXISTS (SELECT 1 FROM environment_input_reservations r WHERE r.session_id = e.session_id AND r.state = 'pending')
     OR EXISTS (SELECT 1 FROM environment_file_writes f WHERE f.environment_id = e.id AND f.state = 'pending'))::boolean AS busy,
    a.compute_wake_requested,
    EXISTS (SELECT 1 FROM turns t WHERE t.session_id = e.session_id AND t.completed_at IS NOT NULL)::boolean AS has_completed_turn
FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
WHERE a.id = $1;

-- name: CountRuntimeComputeReservations :one
SELECT count(*) FROM runtime_allocations
WHERE provider_key = $1 AND state <> 'released' AND compute_phase <> 'suspended';

-- name: CountRuntimeRetainedAllocations :one
SELECT count(*) FROM runtime_allocations
WHERE provider_key = $1 AND state <> 'released';

-- name: RuntimeComputeBlocksAdmission :one
SELECT EXISTS (
    SELECT 1 FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
    WHERE e.session_id = $1 AND a.compute_phase NOT IN ('disabled', 'running')
)::boolean;

-- name: RecordRuntimeTerminalActivity :exec
UPDATE runtime_allocations a SET compute_activity_at = clock_timestamp()
FROM environments e
WHERE a.environment_id = e.id AND e.session_id = $1
    AND a.node_id IS NOT NULL AND a.state = 'running';

-- name: SessionHasRuntimeNode :one
SELECT EXISTS (
    SELECT 1 FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
    WHERE e.session_id = $1 AND a.node_id IS NOT NULL
)::boolean;
