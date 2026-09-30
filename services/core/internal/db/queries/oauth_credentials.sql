-- name: CreateOAuthCredential :one
INSERT INTO vault_credentials (id, vault_id, name, auth_type, mcp_server_url, token_ciphertext, oauth_metadata)
SELECT sqlc.arg(id), v.id, sqlc.arg(name), 'mcp_oauth', sqlc.arg(mcp_server_url), sqlc.arg(token_ciphertext), sqlc.arg(oauth_metadata)
FROM vaults v
WHERE v.tenant_id = sqlc.arg(tenant_id) AND v.id = sqlc.arg(vault_id)
RETURNING id, vault_id, name, auth_type, mcp_server_url, created_at, updated_at, oauth_metadata;

-- name: GetOAuthCredentialForUpdate :one
SELECT c.id, c.vault_id, c.name, c.auth_type, c.mcp_server_url, c.created_at, c.updated_at,
    c.oauth_metadata, c.token_ciphertext
FROM vault_credentials c
JOIN vaults v ON v.id = c.vault_id
WHERE v.tenant_id = sqlc.arg(tenant_id) AND v.id = sqlc.arg(vault_id)
    AND c.id = sqlc.arg(id) AND c.auth_type = 'mcp_oauth'
FOR UPDATE OF c;

-- name: UpdateOAuthCredential :one
UPDATE vault_credentials c
SET token_ciphertext = sqlc.arg(token_ciphertext), oauth_metadata = sqlc.arg(oauth_metadata),
    updated_at = statement_timestamp()
FROM vaults v
WHERE v.id = c.vault_id AND v.tenant_id = sqlc.arg(tenant_id)
    AND v.id = sqlc.arg(vault_id) AND c.id = sqlc.arg(id)
    AND c.auth_type = 'mcp_oauth' AND c.mcp_server_url = sqlc.arg(mcp_server_url)
RETURNING c.id, c.vault_id, c.name, c.auth_type, c.mcp_server_url, c.created_at, c.updated_at, c.oauth_metadata;
