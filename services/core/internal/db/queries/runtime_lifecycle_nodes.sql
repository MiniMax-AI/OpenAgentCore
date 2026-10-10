-- name: ListRuntimeLifecycleNodes :many
SELECT n.id FROM runtime_nodes n CROSS JOIN runtime_deployment d
WHERE n.removed_at IS NULL AND n.installation_id=d.installation_id AND d.mode='nodes'
UNION ALL
SELECT NULL::uuid AS id FROM runtime_deployment WHERE mode = 'direct'
ORDER BY id;

-- name: ListRuntimeAllocationsForNode :many
SELECT sqlc.embed(a), e.session_id, s.tenant_id, s.deleted_at, (a.compute_phase NOT IN ('disabled', 'running') AND a.compute_retained_until IS NOT NULL AND a.compute_retained_until <= clock_timestamp())::boolean AS expired
FROM runtime_allocations a
JOIN environments e ON e.id=a.environment_id
JOIN sessions s ON s.id=e.session_id
WHERE a.node_id IS NOT DISTINCT FROM sqlc.narg(node_id)::uuid
  AND a.id > sqlc.arg(after_id)::uuid AND a.state<>'released'
ORDER BY a.id LIMIT 32;

-- name: ListUnallocatedHostedEnvironmentsForNode :many
SELECT e.id, s.tenant_id
FROM environments e JOIN sessions s ON s.id=e.session_id
LEFT JOIN runtime_placements p ON p.environment_id=e.id
WHERE p.node_id IS NOT DISTINCT FROM sqlc.narg(node_id)::uuid
  AND p.released_at IS NULL
  AND (SELECT reset_clear IS NULL FROM runtime_deployment)
  AND e.id > sqlc.arg(after_id)::uuid AND s.deleted_at IS NULL AND e.status NOT IN ('failed','expired')
  AND s.configuration->'environment'->>'type'='openai_hosted'
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id=e.id AND a.state<>'released')
ORDER BY e.id LIMIT 32;

-- name: GetRuntimeLifecyclePlacement :one
SELECT d.provider_kind, d.mode, a.id AS allocation_id, a.node_id AS allocation_node_id,
       p.node_id AS placement_node_id, p.released_at,
       CASE WHEN COALESCE(a.deployment_generation, p.deployment_generation, d.generation)=d.generation
            THEN d.specification ELSE g.specification END::jsonb AS specification
FROM environments e JOIN sessions s ON s.id=e.session_id
CROSS JOIN runtime_deployment d
LEFT JOIN runtime_allocations a ON a.environment_id=e.id AND a.state<>'released'
LEFT JOIN runtime_placements p ON p.environment_id=e.id
LEFT JOIN runtime_deployment_generations g ON g.generation=COALESCE(a.deployment_generation, p.deployment_generation, d.generation)
WHERE s.tenant_id=$1 AND e.id=$2;

-- name: ListReplacementEnvironments :many
SELECT e.id, s.tenant_id
FROM environments e JOIN sessions s ON s.id = e.session_id
JOIN environment_workspaces w ON w.environment_id = e.id AND w.state = 'ready'
WHERE e.id > $1 AND s.deleted_at IS NULL AND e.status NOT IN ('failed','expired')
  AND e.initialization = 'complete' AND s.configuration->'environment'->>'type' = 'openai_hosted'
  AND (SELECT reset_clear IS NULL FROM runtime_deployment)
  AND EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state = 'released')
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state <> 'released')
  AND EXISTS (SELECT 1 FROM environment_input_reservations r WHERE r.session_id = s.id AND r.state = 'pending')
ORDER BY e.id LIMIT 32;
