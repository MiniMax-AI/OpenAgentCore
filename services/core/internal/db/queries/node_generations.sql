-- name: NodeGenerationKept :one
SELECT (EXISTS(SELECT 1 FROM runtime_deployment d WHERE d.generation=sqlc.arg(generation))
 OR EXISTS(SELECT 1 FROM runtime_nodes n WHERE n.id=sqlc.arg(node_id) AND n.removed_at IS NULL AND n.ready_generation=sqlc.arg(generation))
 OR EXISTS(SELECT 1 FROM runtime_placements p WHERE p.node_id=sqlc.arg(node_id) AND p.released_at IS NULL AND p.deployment_generation=sqlc.arg(generation))
 OR EXISTS(SELECT 1 FROM runtime_allocations a WHERE a.node_id=sqlc.arg(node_id) AND a.state<>'released' AND a.deployment_generation=sqlc.arg(generation)))::boolean AS kept;

-- name: GetNodeGenerationSpecification :one
SELECT d.provider_kind,d.specification FROM runtime_deployment d WHERE d.generation=$1
UNION ALL SELECT g.provider_kind,g.specification FROM runtime_deployment_generations g WHERE g.generation=$1 AND g.generation<>(SELECT d.generation FROM runtime_deployment d)
LIMIT 1;

-- name: UpsertNodeGenerationStatus :exec
INSERT INTO runtime_node_generation_status(node_id,generation,specification_digest,connection_id,owner_epoch,state,diagnostic)
VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT(node_id,generation) DO UPDATE SET specification_digest=EXCLUDED.specification_digest,connection_id=EXCLUDED.connection_id,
 owner_epoch=EXCLUDED.owner_epoch,state=EXCLUDED.state,diagnostic=EXCLUDED.diagnostic,observed_at=clock_timestamp();

-- name: PromoteNodeServingGeneration :exec
UPDATE runtime_nodes SET ready_generation=$2 WHERE id=$1 AND removed_at IS NULL
AND $2=(SELECT generation FROM runtime_deployment) AND (ready_generation IS NULL OR ready_generation<$2);

-- name: RefreshNodeServingReadiness :exec
UPDATE runtime_nodes n SET protocol_version=$2, provider_ready=EXISTS(
 SELECT 1 FROM runtime_node_generation_status g WHERE g.node_id=n.id AND g.generation=n.ready_generation
 AND g.connection_id=n.connection_id AND g.owner_epoch=n.connected_epoch AND g.state='ready')
WHERE n.id=$1 AND n.removed_at IS NULL;

-- name: NodeGenerationReady :one
SELECT EXISTS(SELECT 1 FROM runtime_node_generation_status g JOIN runtime_nodes n ON n.id=g.node_id CROSS JOIN runtime_deployment d
 WHERE n.id=sqlc.arg(node_id) AND g.generation=sqlc.arg(generation) AND n.removed_at IS NULL
 AND n.connection_id=g.connection_id AND n.connected_epoch=g.owner_epoch AND g.owner_epoch=d.owner_epoch
 AND n.last_seen_at>clock_timestamp()-interval '45 seconds' AND g.state='ready')::boolean AS ready;

-- name: DeleteNodeGenerationStatus :exec
DELETE FROM runtime_node_generation_status WHERE node_id=$1 AND generation=$2;
