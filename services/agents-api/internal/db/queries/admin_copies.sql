-- name: LockAdminCopyTargetProject :one
SELECT id, archived_at FROM projects WHERE tenant_id=$1 FOR SHARE;

-- name: LockAdminAssetCopy :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 0));

-- name: GetAdminAssetCopy :one
SELECT request_hash, result FROM admin_asset_copies
WHERE target_tenant_id = $1 AND idempotency_key = $2;

-- name: SaveAdminAssetCopy :exec
INSERT INTO admin_asset_copies(target_tenant_id,idempotency_key,request_hash,result,audit_id)
VALUES($1,$2,$3,$4,$5)
ON CONFLICT (target_tenant_id,idempotency_key) DO UPDATE
SET request_hash=admin_asset_copies.request_hash;

-- name: CreateAdminResourceOwner :exec
INSERT INTO admin_resource_owners(tenant_id,resource_type,resource_id,parent_id,audit_id)
VALUES($1,$2,$3,$4,$5);

-- name: CreateAdminCopySkill :one
INSERT INTO skills(id,tenant_id,name,description,default_version,latest_version,next_version)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: AdminCopySkillVersionNumbers :many
SELECT version FROM skill_versions WHERE tenant_id=$1 AND skill_id=$2 ORDER BY version;

-- name: LockAdminCopyTemplate :one
SELECT id FROM environment_templates WHERE tenant_id=$1 AND id=$2 FOR UPDATE;

-- name: LockAdminCopyVault :one
SELECT * FROM vaults WHERE tenant_id=$1 AND id=$2 FOR UPDATE;

-- name: AdminCopyCredentialIDs :many
SELECT c.id FROM vault_credentials c JOIN vaults v ON v.id=c.vault_id
WHERE v.tenant_id=$1 AND v.id=$2 ORDER BY c.id;

-- name: GetAdminCopyCredential :one
SELECT c.* FROM vault_credentials c JOIN vaults v ON v.id=c.vault_id
WHERE v.tenant_id=$1 AND c.id=$2 FOR UPDATE OF c;

-- name: CreateAdminCopyCredential :one
INSERT INTO vault_credentials(id,vault_id,name,auth_type,mcp_server_url,token_ciphertext,oauth_metadata,status)
SELECT sqlc.arg(id),v.id,sqlc.arg(name),sqlc.arg(auth_type),sqlc.arg(mcp_server_url),sqlc.arg(token_ciphertext),sqlc.arg(oauth_metadata),sqlc.arg(status)
FROM vaults v WHERE v.tenant_id=sqlc.arg(tenant_id) AND v.id=sqlc.arg(vault_id)
RETURNING id;

-- name: CreateAdminCopyVault :one
INSERT INTO vaults(id,tenant_id,name,metadata,status) VALUES($1,$2,$3,$4,$5) RETURNING id;
