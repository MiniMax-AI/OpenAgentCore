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
  AND (p.environment_id IS NOT NULL OR (SELECT mode = 'direct' FROM runtime_deployment))
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

-- name: ListPlacementDemand :many
WITH demand AS (
SELECT e.id, s.tenant_id, s.engine, (latest.id IS NOT NULL)::boolean AS retained,
       CASE WHEN latest.id IS NULL THEN s.created_at
            ELSE LEAST((SELECT min(r.created_at) FROM environment_input_reservations r WHERE r.session_id = s.id AND r.state = 'pending' AND r.deadline > clock_timestamp()), CASE WHEN latest.compute_wake_requested THEN latest.compute_activity_at END) END::timestamptz AS demanded_at
FROM environments e JOIN sessions s ON s.id = e.session_id
LEFT JOIN LATERAL (SELECT a.id, a.state, a.compute_wake_requested, a.compute_activity_at FROM runtime_allocations a WHERE a.environment_id = e.id ORDER BY a.created_at DESC, a.id DESC LIMIT 1) latest ON true
WHERE s.deleted_at IS NULL AND e.status NOT IN ('failed','expired')
  AND s.configuration->'environment'->>'type' = 'openai_hosted'
  AND (SELECT reset_clear IS NULL AND mode = 'nodes' FROM runtime_deployment)
  AND NOT EXISTS (SELECT 1 FROM runtime_placements p WHERE p.environment_id = e.id AND p.released_at IS NULL)
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state <> 'released')
  AND (
    (e.initialization IN ('pending','complete') AND latest.id IS NULL)
    OR (e.initialization = 'complete'
      AND EXISTS (SELECT 1 FROM environment_workspaces w WHERE w.environment_id = e.id AND w.state = 'ready')
      AND EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state = 'released')
      AND (latest.compute_wake_requested OR EXISTS (SELECT 1 FROM environment_input_reservations r WHERE r.session_id = s.id AND r.state = 'pending' AND r.deadline > clock_timestamp())))
  )
)
SELECT id, tenant_id, engine, retained, demanded_at, COALESCE(sqlc.narg(until_time)::timestamptz, statement_timestamp())::timestamptz AS scan_until FROM demand
WHERE (demanded_at, id) > (sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::uuid)
AND demanded_at <= COALESCE(sqlc.narg(until_time)::timestamptz, statement_timestamp())
ORDER BY demanded_at, id LIMIT 32;
