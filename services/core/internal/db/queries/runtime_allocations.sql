-- name: CreateRuntimeAllocation :one
INSERT INTO runtime_allocations (id, environment_id, device_id, provider_key, node_id, deployment_generation)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetLatestRuntimeAllocation :one
SELECT sqlc.embed(a), e.session_id, s.tenant_id, s.deleted_at, (a.compute_phase NOT IN ('disabled', 'running') AND a.compute_retained_until IS NOT NULL AND a.compute_retained_until <= clock_timestamp())::boolean AS expired
FROM runtime_allocations a
JOIN environments e ON e.id = a.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND a.environment_id = $2
ORDER BY a.created_at DESC, a.id DESC LIMIT 1;

-- name: ListRuntimeAllocations :many
SELECT sqlc.embed(a), e.session_id, s.tenant_id, s.deleted_at, (a.compute_phase NOT IN ('disabled', 'running') AND a.compute_retained_until IS NOT NULL AND a.compute_retained_until <= clock_timestamp())::boolean AS expired
FROM runtime_allocations a
JOIN environments e ON e.id = a.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE a.id > $1 AND a.state <> 'released'
ORDER BY a.id LIMIT 32;

-- name: ListRuntimeObservationSessions :many
SELECT s.id, s.tenant_id
FROM sessions s
LEFT JOIN environments e ON e.session_id = s.id
LEFT JOIN runtime_allocations a ON a.environment_id = e.id AND a.state <> 'released'
WHERE s.id > $1
  AND s.deleted_at IS NULL
  AND s.configuration->'environment'->>'type' = 'openai_hosted'
  AND (a.id IS NULL OR a.state <> 'released')
ORDER BY s.id
LIMIT $2;

-- name: ObserveRuntimeRunning :one
UPDATE runtime_allocations SET state = 'running', create_settled = true
WHERE id = $1 AND state IN ('creating', 'running')
RETURNING *;

-- name: RequestRuntimeCleanup :one
UPDATE runtime_allocations SET state = 'cleanup_pending'
WHERE id = $1 AND state <> 'released' RETURNING *;

-- name: SettleRuntimeCreation :one
UPDATE runtime_allocations SET create_settled = true
WHERE id = $1 AND state <> 'released' RETURNING *;

-- name: ReleaseRuntimeAllocation :one
UPDATE runtime_allocations SET state = 'released', released_at = clock_timestamp()
WHERE id = $1 AND state = 'cleanup_pending' AND create_settled RETURNING *;

-- name: CanReplaceRuntimeAllocation :one
SELECT EXISTS (
    SELECT 1 FROM runtime_allocations a JOIN devices d ON d.id = a.device_id AND d.environment_id = a.environment_id
    WHERE a.id = $1 AND a.state = 'released' AND a.create_settled
      AND d.revoked_at IS NOT NULL AND d.executor_key_id IS NULL
      AND NOT EXISTS (SELECT 1 FROM runtime_allocations current WHERE current.environment_id = a.environment_id AND current.state <> 'released')
      AND NOT EXISTS (SELECT 1 FROM devices current WHERE current.environment_id = a.environment_id AND current.revoked_at IS NULL)
)::boolean;

-- name: CanRetainRuntimeEnvironment :one
SELECT EXISTS (
    SELECT 1 FROM runtime_allocations a
    JOIN devices d ON d.id = a.device_id AND d.environment_id = a.environment_id
    JOIN environments e ON e.id = a.environment_id
    JOIN sessions s ON s.id = e.session_id
    JOIN environment_workspaces w ON w.environment_id = e.id AND w.state = 'ready'
    WHERE a.id = $1 AND s.deleted_at IS NULL AND e.status NOT IN ('failed','expired')
      AND e.initialization = 'complete'
      AND s.configuration->'environment'->>'type' = 'openai_hosted'
      AND d.supported_agent_kinds @> jsonb_build_array(jsonb_build_object(
        'kind', s.engine, 'available', true, 'capabilities', jsonb_build_object('retained_native_history', true)))
)::boolean;
