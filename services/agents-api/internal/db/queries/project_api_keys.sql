-- name: CreateProjectAPIKey :one
INSERT INTO project_api_keys (id,name,prefix,token_sha256,tenant_id,organization_id,project_id,subject_kind,subject_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,'service_account',$1::uuid::text)
ON CONFLICT (id) DO NOTHING RETURNING *;

-- name: ListProjectAPIKeys :many
SELECT * FROM project_api_keys
WHERE (sqlc.arg(after_id)::text = '' OR
    (sqlc.arg(ascending)::bool AND id::text > sqlc.arg(after_id)::text) OR
    (NOT sqlc.arg(ascending)::bool AND id::text < sqlc.arg(after_id)::text))
ORDER BY CASE WHEN sqlc.arg(ascending)::bool THEN id END ASC,
         CASE WHEN NOT sqlc.arg(ascending)::bool THEN id END DESC
LIMIT sqlc.arg(page_limit)::int;

-- name: GetProjectAPIKey :one
SELECT * FROM project_api_keys WHERE id = $1;

-- name: RevokeProjectAPIKey :one
UPDATE project_api_keys SET revoked_at = COALESCE(revoked_at, now())
WHERE id = $1 RETURNING *;

-- name: ResetProjectAPIKey :one
UPDATE project_api_keys SET prefix = $2, token_sha256 = $3
WHERE id = $1 AND revoked_at IS NULL RETURNING *;

-- name: ResolveProjectAPIKey :one
SELECT * FROM project_api_keys WHERE token_sha256 = $1 AND revoked_at IS NULL;

-- name: ProjectAPIKeyDigestExists :one
SELECT EXISTS (SELECT 1 FROM project_api_keys WHERE token_sha256 = $1);

-- name: ProjectAPIKeyTenantExists :one
SELECT EXISTS (SELECT 1 FROM project_api_keys WHERE tenant_id = $1);
