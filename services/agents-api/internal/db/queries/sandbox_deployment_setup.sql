-- name: ClaimWebSandboxDeployment :exec
UPDATE runtime_deployment SET installation_id=$1, web_managed=true,
owner_epoch=owner_epoch+1, updated_at=clock_timestamp() WHERE singleton=true;

-- name: InitializeSandboxDeployment :exec
UPDATE runtime_deployment SET provider_kind=$1, core_url=$2, backend_fingerprint=$3,
idle_seconds=$4, retention_seconds=$5, generation=$6, mode=$7, e2b_template=$8, e2b_credential=$9, specification=$10,
e2b_template_build_status=sqlc.narg(e2b_template_build_status), e2b_template_cpus=sqlc.narg(e2b_template_cpus),
e2b_template_memory_mib=sqlc.narg(e2b_template_memory_mib), e2b_template_root_disk_mib=sqlc.narg(e2b_template_root_disk_mib),
updated_at=clock_timestamp() WHERE singleton=true;

-- name: RecordSandboxTemplateBuild :exec
UPDATE runtime_deployment SET e2b_template_build_status=sqlc.narg(e2b_template_build_status), e2b_template_cpus=sqlc.narg(e2b_template_cpus),
e2b_template_memory_mib=sqlc.narg(e2b_template_memory_mib), e2b_template_root_disk_mib=sqlc.narg(e2b_template_root_disk_mib),
updated_at=clock_timestamp() WHERE singleton=true AND provider_kind='e2b';

-- name: SetSandboxMaintenance :exec
UPDATE runtime_deployment SET maintenance=$1,updated_at=clock_timestamp() WHERE singleton=true;

-- name: RetireSandboxNodes :exec
UPDATE runtime_nodes SET removed_at=clock_timestamp(),connection_id=NULL,provider_ready=false WHERE removed_at IS NULL;

-- name: RetireSandboxEnrollments :exec
UPDATE runtime_node_enrollments SET expires_at=clock_timestamp() WHERE consumed_at IS NULL;

-- name: AdvanceSandboxOwnerEpoch :exec
UPDATE runtime_deployment SET owner_epoch=owner_epoch+1 WHERE singleton=true;
