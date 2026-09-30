-- name: LockRuntimeDeployment :one
SELECT * FROM runtime_deployment WHERE singleton = true FOR UPDATE;

-- name: SetRuntimeDeployment :exec
UPDATE runtime_deployment SET installation_id = $1, backend_fingerprint = $2,
admission_paused = $3, updated_at = clock_timestamp() WHERE singleton = true;

-- name: CountRuntimeDeploymentResources :one
SELECT
(SELECT count(*) FROM runtime_allocations WHERE state <> 'released')::bigint AS allocations,
(SELECT count(*) FROM environments e JOIN sessions s ON s.id = e.session_id
 WHERE s.deleted_at IS NULL AND e.status = 'pending'
 AND s.configuration->'environment'->>'type' = 'openai_hosted'
 AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id))::bigint AS pending;

-- name: CountAddressBindings :one
SELECT
(SELECT count(*) FROM runtime_nodes WHERE removed_at IS NULL)::bigint AS nodes,
(SELECT count(*) FROM runtime_nodes WHERE removed_at IS NULL AND core_url <> sqlc.arg(public_url)::text)::bigint AS nodes_on_other_address,
(SELECT count(*) FROM environment_executor_credentials c
 LEFT JOIN environments e ON e.id = c.environment_id LEFT JOIN sessions s ON s.id = e.session_id
 WHERE c.revoked_at IS NULL AND (c.environment_id IS NULL OR s.deleted_at IS NULL))::bigint AS self_hosted_executors;
