-- name: CreateProjectAPIKey :one
INSERT INTO project_api_keys (id,name,prefix,token_sha256,project_id)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (id) DO NOTHING RETURNING *;

-- name: ListProjectAPIKeys :many
SELECT * FROM project_api_keys
WHERE project_id=sqlc.arg(project_id) AND (sqlc.arg(after_id)::text = '' OR
    (sqlc.arg(ascending)::bool AND id::text > sqlc.arg(after_id)::text) OR
    (NOT sqlc.arg(ascending)::bool AND id::text < sqlc.arg(after_id)::text))
ORDER BY CASE WHEN sqlc.arg(ascending)::bool THEN id END ASC,
         CASE WHEN NOT sqlc.arg(ascending)::bool THEN id END DESC
LIMIT sqlc.arg(page_limit)::int;

-- name: GetProjectAPIKeyForProject :one
SELECT * FROM project_api_keys WHERE id=$1 AND project_id=$2;

-- name: RevokeProjectAPIKey :one
UPDATE project_api_keys SET revoked_at = COALESCE(revoked_at, now())
WHERE id = $1 AND project_id=$2 RETURNING *;

-- name: ResolveProjectAPIKey :one
SELECT k.*, p.tenant_id, p.subject_kind, p.subject_id, s.organization_id, s.project_id AS external_project_id
FROM project_api_keys k JOIN projects p ON p.id=k.project_id
JOIN execution_project_scopes s ON s.tenant_id=p.tenant_id
WHERE k.token_sha256 = $1 AND k.revoked_at IS NULL AND p.archived_at IS NULL;

-- name: ProjectAPIKeyDigestExists :one
SELECT EXISTS (SELECT 1 FROM project_api_keys WHERE token_sha256 = $1);
