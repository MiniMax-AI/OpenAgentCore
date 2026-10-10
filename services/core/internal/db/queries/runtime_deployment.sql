-- name: LockRuntimeDeployment :one
SELECT * FROM runtime_deployment WHERE singleton = true FOR UPDATE;

-- name: CountRuntimeDeploymentResources :one
SELECT
(SELECT count(*) FROM runtime_allocations WHERE state <> 'released')::bigint AS allocations,
((SELECT count(*) FROM environments e JOIN sessions s ON s.id = e.session_id
 WHERE s.deleted_at IS NULL AND e.status NOT IN ('failed','expired')
 AND ((e.status = 'pending' AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id))
      OR EXISTS (SELECT 1 FROM runtime_placements p WHERE p.environment_id = e.id AND p.released_at IS NULL))
 AND s.configuration->'environment'->>'type' = 'openai_hosted'
 AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state <> 'released'))::bigint
 + (SELECT count(*) FROM runtime_reset_retained_environments)::bigint)::bigint AS pending;

-- name: CountAddressBindings :one
SELECT
(SELECT count(*) FROM runtime_nodes WHERE removed_at IS NULL)::bigint AS nodes,
(SELECT count(*) FROM runtime_nodes WHERE removed_at IS NULL AND core_url <> sqlc.arg(public_url)::text)::bigint AS nodes_on_other_address,
(SELECT count(*) FROM environment_executor_credentials c
 LEFT JOIN environments e ON e.id = c.environment_id LEFT JOIN sessions s ON s.id = e.session_id
 WHERE c.revoked_at IS NULL AND (c.environment_id IS NULL OR s.deleted_at IS NULL))::bigint AS self_hosted_executors;
