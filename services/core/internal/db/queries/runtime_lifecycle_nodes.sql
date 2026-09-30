-- name: ListRuntimeLifecycleNodes :many
SELECT n.id FROM runtime_nodes n CROSS JOIN runtime_deployment d
WHERE n.removed_at IS NULL AND n.installation_id=d.installation_id AND d.mode='nodes'
UNION ALL
SELECT NULL::uuid AS id FROM runtime_deployment WHERE mode IN ('','direct')
ORDER BY id;

-- name: ListRuntimeAllocationsForNode :many
SELECT sqlc.embed(a), e.session_id, s.tenant_id, s.deleted_at, (CASE WHEN a.compute_phase NOT IN ('disabled', 'running') THEN a.compute_retained_until IS NOT NULL AND a.compute_retained_until <= clock_timestamp() ELSE a.node_id IS NULL AND (SELECT mode FROM runtime_deployment) <> 'direct' AND a.kept_at <= clock_timestamp() - interval '1 hour' END)::boolean AS expired
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
  AND NOT (SELECT admission_paused FROM runtime_deployment)
  AND e.id > sqlc.arg(after_id)::uuid AND s.deleted_at IS NULL AND e.status='pending'
  AND s.configuration->'environment'->>'type'='openai_hosted'
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id=e.id)
ORDER BY e.id LIMIT 32;

-- name: GetRuntimeLifecyclePlacement :one
SELECT d.provider_kind, d.mode, a.id AS allocation_id, a.node_id AS allocation_node_id,
       p.node_id AS placement_node_id, p.released_at
FROM environments e JOIN sessions s ON s.id=e.session_id
CROSS JOIN runtime_deployment d
LEFT JOIN runtime_allocations a ON a.environment_id=e.id
LEFT JOIN runtime_placements p ON p.environment_id=e.id
WHERE s.tenant_id=$1 AND e.id=$2;
