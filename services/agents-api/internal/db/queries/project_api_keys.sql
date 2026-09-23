-- name: CreateProjectAPIKey :one
INSERT INTO project_api_keys
    (id, name, prefix, token_sha256, binding_digest, tenant_id, organization_id, project_id, subject_kind, subject_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO NOTHING
RETURNING id, name, prefix, created_at, revoked_at;

-- name: ListProjectAPIKeys :many
SELECT id, name, prefix, created_at, revoked_at FROM project_api_keys
WHERE binding_digest = $1 AND tenant_id = $2 AND organization_id = $3
  AND project_id = $4 AND subject_kind = $5 AND subject_id = $6
ORDER BY created_at DESC, id;

-- name: RevokeProjectAPIKey :execrows
UPDATE project_api_keys SET revoked_at = COALESCE(revoked_at, now())
WHERE id = $1 AND binding_digest = $2 AND tenant_id = $3 AND organization_id = $4
  AND project_id = $5 AND subject_kind = $6 AND subject_id = $7;

-- name: ResolveProjectAPIKey :one
SELECT binding_digest, tenant_id, organization_id, project_id, subject_kind, subject_id
FROM project_api_keys WHERE token_sha256 = $1 AND revoked_at IS NULL;
