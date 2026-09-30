-- name: RetainSandboxGeneration :exec
INSERT INTO runtime_deployment_generations(generation,provider_kind,specification,provider_config,provider_metadata)
SELECT generation,provider_kind,specification,provider_config,provider_metadata
FROM runtime_deployment d WHERE provider_kind <> '' AND (
 EXISTS(SELECT 1 FROM runtime_allocations a WHERE a.state <> 'released' AND a.deployment_generation=d.generation)
 OR EXISTS(SELECT 1 FROM runtime_placements p WHERE p.released_at IS NULL AND p.deployment_generation=d.generation)
 OR EXISTS(SELECT 1 FROM runtime_nodes n WHERE n.removed_at IS NULL AND n.ready_generation=d.generation));

-- name: CollectSandboxGenerations :exec
DELETE FROM runtime_deployment_generations WHERE generation IN (SELECT g.generation FROM runtime_deployment_generations g WHERE g.generation <> (SELECT generation FROM runtime_deployment)
AND NOT EXISTS(SELECT 1 FROM runtime_allocations a WHERE a.state <> 'released' AND a.deployment_generation=g.generation)
AND NOT EXISTS(SELECT 1 FROM runtime_placements p WHERE p.released_at IS NULL AND p.deployment_generation=g.generation)
AND NOT EXISTS(SELECT 1 FROM runtime_nodes n WHERE n.removed_at IS NULL AND n.ready_generation=g.generation) ORDER BY g.generation LIMIT 32);

-- name: GetSandboxGeneration :one
SELECT * FROM runtime_deployment_generations WHERE generation=$1;

-- name: ListSandboxGenerations :many
SELECT * FROM runtime_deployment_generations WHERE generation > $1 ORDER BY generation LIMIT 32;

-- name: ClearSandboxGenerations :exec
DELETE FROM runtime_deployment_generations;
