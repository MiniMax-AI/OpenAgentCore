-- name: ClaimWebSandboxDeployment :exec
UPDATE runtime_deployment SET installation_id=$1, web_managed=true,
owner_epoch=owner_epoch+1, updated_at=clock_timestamp() WHERE singleton=true;

-- name: InitializeSandboxDeployment :exec
UPDATE runtime_deployment SET provider_kind=$1, core_url=$2, backend_fingerprint=$3,
idle_seconds=$4, retention_seconds=$5, updated_at=clock_timestamp() WHERE singleton=true;
