-- name: SetRuntimeCompute :one
UPDATE runtime_allocations
SET compute_phase_changed_at = CASE WHEN compute_phase = sqlc.arg(phase)::text THEN compute_phase_changed_at ELSE clock_timestamp() END,
    compute_phase = sqlc.arg(phase), compute_state = sqlc.arg(state)::jsonb,
    compute_revision = compute_revision + 1,
    compute_retained_until = sqlc.narg(retained_until)
WHERE runtime_allocations.id = sqlc.arg(id) AND compute_revision = sqlc.arg(revision)
    AND state = 'running' AND EXISTS (SELECT 1 FROM environments e WHERE e.id = runtime_allocations.environment_id AND e.initialization = 'complete')
    AND (compute_phase IN ('disabled','running') OR compute_retained_until > clock_timestamp())
RETURNING *;

-- name: TouchRuntimeActivity :exec
UPDATE runtime_allocations a
SET compute_activity_at = CASE WHEN a.state = 'released' AND a.compute_wake_requested THEN a.compute_activity_at ELSE clock_timestamp() END, compute_wake_requested = true
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE a.environment_id = e.id AND s.tenant_id = sqlc.arg(tenant_id)
    AND e.id = sqlc.arg(environment_id) AND s.deleted_at IS NULL
    AND ((a.state = 'running' AND a.compute_phase <> 'disabled') OR a.id = sqlc.narg(retained_id)::uuid);

-- name: ClearRuntimeWake :exec
UPDATE runtime_allocations
SET compute_wake_requested = false
WHERE id = $1 AND compute_phase = 'running' AND compute_activity_at <= $2;

-- name: GetRuntimeActivity :one
SELECT clock_timestamp()::timestamptz AS observed_at,
    GREATEST(a.compute_activity_at, a.compute_phase_changed_at,
    CASE WHEN a.node_id IS NULL THEN COALESCE((SELECT max(t.completed_at) FROM turns t WHERE t.session_id = e.session_id), a.created_at) END,
    CASE WHEN a.node_id IS NULL THEN (SELECT max(t.completed_at) FROM subagent_turns t WHERE t.session_id = e.session_id) END,
    (SELECT max(f.settled_at) FROM environment_file_writes f WHERE f.environment_id = e.id))::timestamptz AS last_activity,
    (EXISTS (SELECT 1 FROM turns t WHERE t.session_id = e.session_id AND t.status IN ('queued','in_progress','waiting'))
     OR EXISTS (SELECT 1 FROM subagent_turns t WHERE t.session_id = e.session_id AND t.status IN ('queued','in_progress','waiting'))
     OR EXISTS (SELECT 1 FROM environment_input_reservations r WHERE r.session_id = e.session_id AND r.state = 'pending')
     OR EXISTS (SELECT 1 FROM environment_file_writes f WHERE f.environment_id = e.id AND f.state = 'pending'))::boolean AS busy,
    a.compute_wake_requested
FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
WHERE a.id = $1;

-- name: RuntimeComputeBlocksAdmission :one
SELECT EXISTS (
    SELECT 1 FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
    WHERE e.session_id = $1 AND a.state <> 'released' AND a.compute_phase NOT IN ('disabled', 'running')
)::boolean;

-- name: RecordRuntimeTerminalActivity :exec
UPDATE runtime_allocations a SET compute_activity_at = clock_timestamp()
FROM environments e
WHERE a.environment_id = e.id AND e.session_id = $1
    AND a.node_id IS NOT NULL AND a.state = 'running';

-- name: SessionHasRuntimeNode :one
SELECT EXISTS (
    SELECT 1 FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
    WHERE e.session_id = $1 AND a.state <> 'released' AND a.node_id IS NOT NULL
)::boolean;

-- name: MoveSuspendedRuntimeCompute :one
WITH moved AS (
    UPDATE runtime_placements p SET node_id=sqlc.arg(destination)::uuid
    FROM runtime_allocations a
    WHERE a.id=sqlc.arg(id) AND a.environment_id=p.environment_id
      AND p.node_id=sqlc.arg(source)::uuid AND p.released_at IS NULL
      AND p.deployment_generation=a.deployment_generation
      AND a.node_id=p.node_id AND a.compute_revision=sqlc.arg(revision)
      AND a.compute_phase='suspended'
      AND ((a.state='running' AND sqlc.arg(phase)::text='restoring'
            AND a.compute_retained_until>clock_timestamp()
            AND EXISTS(SELECT 1 FROM environments e WHERE e.id=a.environment_id AND e.initialization='complete'))
           OR (a.state='cleanup_pending' AND sqlc.arg(phase)::text='suspended'))
    RETURNING a.id
)
UPDATE runtime_allocations a
SET node_id=sqlc.arg(destination)::uuid, compute_phase=sqlc.arg(phase),
    compute_state=sqlc.arg(state)::jsonb, compute_revision=a.compute_revision+1,
    compute_phase_changed_at=CASE WHEN a.compute_phase=sqlc.arg(phase)::text THEN a.compute_phase_changed_at ELSE clock_timestamp() END
FROM moved WHERE a.id=moved.id RETURNING a.*;
