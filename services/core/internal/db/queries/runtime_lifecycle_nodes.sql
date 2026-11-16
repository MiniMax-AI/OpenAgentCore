-- name: ListRuntimeLifecycleNodes :many
SELECT n.id FROM runtime_nodes n CROSS JOIN runtime_deployment d
WHERE n.removed_at IS NULL AND n.installation_id=d.installation_id AND d.mode='nodes'
UNION ALL
SELECT NULL::uuid AS id FROM runtime_deployment WHERE mode = 'direct'
ORDER BY id;

-- name: ListRuntimeAllocationsForNode :many
SELECT sqlc.embed(a), s.id AS attached_session_id, e.tenant_id, l.ended::boolean AS ended, (a.compute_phase NOT IN ('disabled', 'running') AND a.compute_retained_until IS NOT NULL AND a.compute_retained_until <= clock_timestamp())::boolean AS expired
FROM runtime_allocations a
JOIN environments e ON e.id=a.environment_id
JOIN environment_liveness l ON l.id=e.id
LEFT JOIN sessions s ON s.environment_id=e.id AND s.deleted_at IS NULL
WHERE a.node_id IS NOT DISTINCT FROM sqlc.narg(node_id)::uuid
  AND a.id > sqlc.arg(after_id)::uuid AND a.state<>'released'
ORDER BY a.id LIMIT 32;

-- name: ListUnallocatedHostedEnvironmentsForNode :many
SELECT e.id, e.tenant_id
FROM environments e JOIN environment_liveness l ON l.id=e.id AND l.active
LEFT JOIN runtime_placements p ON p.environment_id=e.id
WHERE p.node_id IS NOT DISTINCT FROM sqlc.narg(node_id)::uuid
  AND (p.environment_id IS NOT NULL OR (SELECT mode = 'direct' FROM runtime_deployment))
  AND p.released_at IS NULL
  AND (SELECT reset_clear IS NULL FROM runtime_deployment)
  AND e.id > sqlc.arg(after_id)::uuid
  AND e.configuration->>'type'='openai_hosted'
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id=e.id AND a.state<>'released')
ORDER BY e.id LIMIT 32;

-- name: GetRuntimeLifecyclePlacement :one
SELECT d.provider_kind, d.mode, a.id AS allocation_id, a.node_id AS allocation_node_id,
       p.node_id AS placement_node_id, p.deployment_generation AS placement_generation, p.released_at,
       CASE WHEN COALESCE(a.deployment_generation, p.deployment_generation, d.generation)=d.generation
            THEN d.specification ELSE g.specification END::jsonb AS specification
FROM environments e
CROSS JOIN runtime_deployment d
LEFT JOIN runtime_allocations a ON a.environment_id=e.id AND a.state<>'released'
LEFT JOIN runtime_placements p ON p.environment_id=e.id
LEFT JOIN runtime_deployment_generations g ON g.generation=COALESCE(a.deployment_generation, p.deployment_generation, d.generation)
WHERE e.tenant_id=$1 AND e.id=$2;

-- name: ListPlacementDemand :many
SELECT id, tenant_id, engine, retained, demanded_at, COALESCE(sqlc.narg(until_time)::timestamptz, statement_timestamp())::timestamptz AS scan_until FROM runtime_placement_demand
WHERE NOT reserved AND (demanded_at, id) > (sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::uuid)
AND demanded_at <= COALESCE(sqlc.narg(until_time)::timestamptz, statement_timestamp())
ORDER BY demanded_at, id LIMIT 32;

-- name: IsPlacementDemanded :one
-- A plain id predicate keeps an index plan under a cached generic plan; the scan's statement could not.
SELECT EXISTS (SELECT 1 FROM runtime_placement_demand WHERE id = $1)::boolean;
