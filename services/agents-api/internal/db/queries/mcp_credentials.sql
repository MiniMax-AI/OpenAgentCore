-- name: GetAttachedVaultIDs :many
SELECT id FROM vaults
WHERE tenant_id = sqlc.arg(tenant_id) AND id = ANY(sqlc.arg(vault_ids)::uuid[]);

-- name: FindMCPCredentials :many
-- An explicit credential ID is found in the attached Vaults by ID alone, so the
-- caller can compare its destination; otherwise the exact destination selects.
SELECT c.id, c.vault_id, c.name, c.auth_type, c.mcp_server_url, c.created_at, c.updated_at
FROM vault_credentials c
JOIN vaults v ON v.id = c.vault_id
WHERE v.tenant_id = sqlc.arg(tenant_id)
  AND v.id = ANY(sqlc.arg(vault_ids)::uuid[])
  AND c.auth_type IN ('static_bearer', 'mcp_oauth')
  AND CASE WHEN sqlc.narg(credential_id)::uuid IS NULL THEN c.mcp_server_url = sqlc.arg(mcp_server_url)
           ELSE c.id = sqlc.narg(credential_id)::uuid END
ORDER BY c.id
LIMIT 2;

-- name: GetMCPStaticCredentialCiphertext :one
SELECT c.token_ciphertext
FROM vault_credentials c
JOIN vaults v ON v.id = c.vault_id
WHERE v.tenant_id = sqlc.arg(tenant_id)
  AND v.id = ANY(sqlc.arg(vault_ids)::uuid[])
  AND v.id = sqlc.arg(vault_id)
  AND c.id = sqlc.arg(credential_id)
  AND c.auth_type = 'static_bearer'
  AND c.mcp_server_url = sqlc.arg(mcp_server_url);
