-- name: CreateRuntimeAllocation :one
INSERT INTO runtime_allocations (id, environment_id, provider_key, node_id, deployment_generation, serve_credential_hash)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetRuntimeAllocation :one
SELECT sqlc.embed(a), e.session_id, s.tenant_id, s.deleted_at, (a.compute_phase NOT IN ('disabled', 'running') AND a.compute_retained_until IS NOT NULL AND a.compute_retained_until <= clock_timestamp())::boolean AS expired
FROM runtime_allocations a
JOIN environments e ON e.id = a.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND a.environment_id = $2;

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
LEFT JOIN runtime_allocations a ON a.environment_id = e.id
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
