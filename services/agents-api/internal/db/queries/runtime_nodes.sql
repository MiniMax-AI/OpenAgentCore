-- name: SetRuntimeManagerDeployment :exec
UPDATE runtime_deployment SET provider_kind=$1, local_node_id=$2, mode='nodes', generation=GREATEST(generation,1), owner_epoch=owner_epoch+1 WHERE singleton=true;

-- name: GetRuntimeDeployment :one
SELECT * FROM runtime_deployment WHERE singleton=true;

-- name: InsertRuntimeNode :one
INSERT INTO runtime_nodes(id,installation_id,name,backend_fingerprint,credential_sha256,max_active,max_retained,specification_digest,deployment_generation)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;

-- name: GetRuntimeNode :one
SELECT * FROM runtime_nodes WHERE id=$1 AND removed_at IS NULL;

-- name: ListRuntimeNodes :many
SELECT n.*, (n.connection_id IS NOT NULL AND n.connected_epoch=d.owner_epoch AND n.last_seen_at > clock_timestamp()-interval '45 seconds')::boolean AS online,
 d.provider_kind,
 (SELECT count(*) FROM runtime_placements p LEFT JOIN runtime_allocations a ON a.environment_id=p.environment_id WHERE p.node_id=n.id AND p.released_at IS NULL AND (a.id IS NULL OR a.compute_phase <> 'suspended'))::bigint AS active,
 (SELECT count(*) FROM runtime_placements p WHERE p.node_id=n.id AND p.released_at IS NULL)::bigint AS retained,
 (SELECT count(*) FROM runtime_placements p WHERE p.node_id=n.id AND p.released_at IS NULL AND NOT EXISTS(SELECT 1 FROM runtime_allocations a WHERE a.environment_id=p.environment_id))::bigint AS reserved,
 (SELECT count(*) FROM runtime_allocations a WHERE a.node_id=n.id AND a.state='cleanup_pending')::bigint AS cleanup_pending,
 (SELECT count(*) FROM runtime_allocations a WHERE a.node_id=n.id AND a.state='running' AND a.compute_phase IN('running','disabled'))::bigint AS running,
 (SELECT count(*) FROM runtime_allocations a WHERE a.node_id=n.id AND a.state<>'released' AND a.compute_state->'snapshot' IS NOT NULL AND a.compute_state->'snapshot'<>'null'::jsonb)::bigint AS snapshots
FROM runtime_nodes n CROSS JOIN runtime_deployment d
WHERE n.removed_at IS NULL AND n.installation_id=d.installation_id ORDER BY n.id;

-- name: UpdateRuntimeNode :one
UPDATE runtime_nodes SET name=$2,max_active=$3,max_retained=$4 WHERE id=$1 AND removed_at IS NULL RETURNING *;

-- name: RemoveRuntimeNode :exec
UPDATE runtime_nodes SET removed_at=clock_timestamp(),connection_id=NULL WHERE id=$1 AND removed_at IS NULL;

-- name: ConnectRuntimeNode :execrows
UPDATE runtime_nodes SET connection_id=$2,provider_ready=false,connected_epoch=d.owner_epoch,last_seen_at=clock_timestamp()
FROM runtime_deployment d WHERE runtime_nodes.id=$1 AND removed_at IS NULL AND runtime_nodes.installation_id=d.installation_id AND d.owner_epoch=sqlc.arg(owner_epoch)
AND runtime_nodes.connected_epoch <= sqlc.arg(owner_epoch);

-- name: HeartbeatRuntimeNode :execrows
UPDATE runtime_nodes SET last_seen_at=clock_timestamp(),provider_ready=$3,health=sqlc.arg(health)::jsonb FROM runtime_deployment d
WHERE runtime_nodes.id=$1 AND connection_id=$2 AND removed_at IS NULL AND connected_epoch=d.owner_epoch AND d.owner_epoch=sqlc.arg(owner_epoch);

-- name: LockRuntimeNodePresence :one
SELECT id FROM runtime_nodes WHERE id=$1 FOR UPDATE;

-- name: DisconnectRuntimeNode :exec
UPDATE runtime_nodes SET connection_id=NULL FROM runtime_deployment d WHERE runtime_nodes.id=$1 AND connection_id=$2 AND connected_epoch=d.owner_epoch AND d.owner_epoch=sqlc.arg(owner_epoch);

-- name: CreateRuntimeEnrollment :exec
INSERT INTO runtime_node_enrollments(token_sha256,installation_id,expires_at,max_active,max_retained) VALUES($1,$2,clock_timestamp()+interval '10 minutes',$3,$4);

-- name: ConsumeRuntimeEnrollment :execrows
UPDATE runtime_node_enrollments SET consumed_at=clock_timestamp(),node_id=$2
WHERE token_sha256=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp()
AND installation_id=(SELECT installation_id FROM runtime_deployment WHERE singleton=true);

-- name: GetRuntimeEnrollment :one
SELECT * FROM runtime_node_enrollments WHERE token_sha256=$1;

-- name: CreateRuntimePlacement :exec
INSERT INTO runtime_placements(environment_id,node_id) VALUES($1,$2);

-- name: GetRuntimePlacement :one
SELECT p.*, n.name, (n.provider_ready AND n.connection_id IS NOT NULL AND n.connected_epoch=d.owner_epoch AND n.last_seen_at>clock_timestamp()-interval '45 seconds' AND n.removed_at IS NULL)::boolean AS available,
 COALESCE(a.observation_error,'')::text AS observation_error, COALESCE(a.state,'reserved')::text AS state, COALESCE(a.compute_phase,'disabled')::text AS compute_phase
FROM runtime_placements p JOIN runtime_nodes n ON n.id=p.node_id CROSS JOIN runtime_deployment d
LEFT JOIN runtime_allocations a ON a.environment_id=p.environment_id WHERE p.environment_id=$1;

-- name: ReleaseRuntimePlacement :exec
UPDATE runtime_placements SET released_at=COALESCE(released_at,clock_timestamp()) WHERE environment_id=$1;

-- name: ReleaseUnallocatedRuntimePlacement :exec
UPDATE runtime_placements SET released_at=COALESCE(released_at,clock_timestamp())
WHERE environment_id IN(SELECT id FROM environments WHERE session_id=$1)
AND NOT EXISTS(SELECT 1 FROM runtime_allocations a WHERE a.environment_id=runtime_placements.environment_id);

-- name: AdoptRuntimePlacements :exec
INSERT INTO runtime_placements(environment_id,node_id,released_at)
SELECT a.environment_id,$1,a.released_at FROM runtime_allocations a WHERE a.provider_key=$2 AND a.state<>'released' AND a.node_id IS NULL
ON CONFLICT(environment_id) DO NOTHING;

-- name: AdoptPendingRuntimePlacements :exec
INSERT INTO runtime_placements(environment_id,node_id)
SELECT e.id,$1 FROM environments e JOIN sessions s ON s.id=e.session_id
WHERE s.deleted_at IS NULL AND e.status='pending' AND s.configuration->'environment'->>'type'='openai_hosted'
AND NOT EXISTS(SELECT 1 FROM runtime_allocations a WHERE a.environment_id=e.id)
ON CONFLICT(environment_id) DO NOTHING;

-- name: BindLegacyRuntimeAllocations :exec
UPDATE runtime_allocations SET node_id=$1,compute_activity_at=clock_timestamp()
WHERE provider_key=$2 AND node_id IS NULL AND state<>'released';

-- name: ListNodeRuntimeAllocations :many
SELECT a.id,a.node_id,a.observation_error,a.state,a.compute_phase,a.initialization,a.created_at,a.environment_id,e.session_id,s.tenant_id
FROM runtime_allocations a JOIN environments e ON e.id=a.environment_id JOIN sessions s ON s.id=e.session_id
WHERE a.node_id=$1 AND a.state<>'released' ORDER BY a.created_at,a.id LIMIT 1000;

-- name: CreateSessionRuntimePlacement :exec
INSERT INTO runtime_placements(environment_id,node_id)
SELECT id,$2 FROM environments WHERE session_id=$1;

-- name: SetRuntimeObservation :exec
UPDATE runtime_allocations SET observation_error=$4 WHERE id=$1 AND compute_revision=$2 AND state=$3 AND state<>'released';

-- name: ListLegacyRuntimeAllocations :many
SELECT sqlc.embed(a), e.session_id, s.tenant_id, s.deleted_at
FROM runtime_allocations a JOIN environments e ON e.id=a.environment_id JOIN sessions s ON s.id=e.session_id
WHERE a.node_id IS NULL AND a.state<>'released' AND a.id>$1 ORDER BY a.id LIMIT 32 FOR UPDATE OF a;
