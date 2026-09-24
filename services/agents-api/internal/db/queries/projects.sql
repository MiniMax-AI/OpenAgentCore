-- name: CreateProject :one
INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id)
VALUES ($1,$2,$3,'service_account',$4) RETURNING *;

-- name: GetProject :one
SELECT p.*, s.organization_id, s.project_id AS external_project_id,
    (SELECT count(*) FROM project_api_keys k WHERE k.project_id=p.id AND k.revoked_at IS NULL) AS active_key_count
FROM projects p JOIN execution_project_scopes s ON s.tenant_id=p.tenant_id WHERE p.id=$1;

-- name: ListProjects :many
SELECT p.*, s.organization_id, s.project_id AS external_project_id,
    (SELECT count(*) FROM project_api_keys k WHERE k.project_id=p.id AND k.revoked_at IS NULL) AS active_key_count
FROM projects p JOIN execution_project_scopes s ON s.tenant_id=p.tenant_id
WHERE (sqlc.arg(after_id)::text='' OR
    (sqlc.arg(ascending)::bool AND p.id::text>sqlc.arg(after_id)::text) OR
    (NOT sqlc.arg(ascending)::bool AND p.id::text<sqlc.arg(after_id)::text))
ORDER BY CASE WHEN sqlc.arg(ascending)::bool THEN p.id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::bool THEN p.id END DESC
LIMIT sqlc.arg(page_limit)::int;

-- name: LockProject :one
SELECT * FROM projects WHERE id=$1 FOR SHARE;

-- name: LockProjectForUpdate :one
SELECT * FROM projects WHERE id=$1 FOR UPDATE;

-- name: RenameProject :exec
UPDATE projects SET name=$2 WHERE id=$1;

-- name: ArchiveProject :exec
UPDATE projects SET archived_at=COALESCE(archived_at,now()) WHERE id=$1;

-- name: RevokeProjectKeys :exec
UPDATE project_api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE project_id=$1;
