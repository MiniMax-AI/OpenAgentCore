-- name: ClaimWebSandboxDeployment :exec
UPDATE runtime_deployment SET installation_id=$1, web_managed=true,
owner_epoch=owner_epoch+1, updated_at=clock_timestamp() WHERE singleton=true;

-- name: InitializeSandboxDeployment :exec
UPDATE runtime_deployment SET provider_kind=$1, backend_fingerprint=$2,
idle_seconds=$3, retention_seconds=$4, generation=$5, mode=$6,
provider_config=sqlc.arg(provider_config),provider_metadata=sqlc.arg(provider_metadata),provider_credential=sqlc.arg(provider_credential),specification=sqlc.arg(specification),
updated_at=clock_timestamp() WHERE singleton=true;

-- name: RecordSandboxConfigurationMetadata :exec
UPDATE runtime_deployment SET provider_metadata=$1,updated_at=clock_timestamp()
WHERE singleton=true AND provider_kind<>'';

-- name: RetireSandboxNodes :exec
UPDATE runtime_nodes SET removed_at=clock_timestamp(),connection_id=NULL,provider_ready=false,ready_generation=NULL WHERE removed_at IS NULL;

-- name: RetireSandboxEnrollments :exec
UPDATE runtime_node_enrollments SET expires_at=clock_timestamp() WHERE consumed_at IS NULL;

-- name: AdvanceSandboxOwnerEpoch :exec
UPDATE runtime_deployment SET owner_epoch=owner_epoch+1 WHERE singleton=true;
