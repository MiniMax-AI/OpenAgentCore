-- name: CreateEnvironment :exec
-- A creation identity makes the Environment standalone; without one a Session owns it.
INSERT INTO environments (id, tenant_id, configuration, session_owned, initialization, idempotency_key, request_hash, creator_kind, creator_id)
VALUES (sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(configuration), sqlc.narg(idempotency_key)::text IS NULL,
        CASE WHEN sqlc.arg(prepared)::boolean THEN 'pending' ELSE 'complete' END,
        sqlc.narg(idempotency_key), sqlc.narg(request_hash), sqlc.narg(creator_kind), sqlc.narg(creator_id));

-- name: LockEnvironmentCreation :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(tenant_id)::text || ':' || sqlc.arg(creator_kind)::text || ':' || sqlc.arg(creator_id)::text || ':' || sqlc.arg(idempotency_key)::text, 0));

-- name: FindEnvironmentCreation :one
SELECT id, idempotency_key, request_hash, creator_kind, creator_id
FROM environments WHERE tenant_id = $1 AND creator_kind = $2 AND creator_id = $3 AND idempotency_key = $4;

-- name: AttachSessionEnvironment :exec
UPDATE sessions SET environment_id = $2 WHERE id = $1;

-- name: GetEnvironment :one
SELECT sqlc.embed(e), s.id AS attached_session_id,
       EXISTS (SELECT 1 FROM environment_workspaces w WHERE w.environment_id = e.id)::boolean AS external_workspace
FROM environments e LEFT JOIN sessions s ON s.environment_id = e.id AND s.deleted_at IS NULL
WHERE e.tenant_id = $1 AND e.id = $2 AND (s.id IS NOT NULL OR NOT e.session_owned);

-- name: GetSessionEnvironment :one
SELECT sqlc.embed(e), s.id AS attached_session_id,
       EXISTS (SELECT 1 FROM environment_workspaces w WHERE w.environment_id = e.id)::boolean AS external_workspace
FROM sessions s JOIN environments e ON e.id = s.environment_id
WHERE s.tenant_id = $1 AND s.id = $2 AND s.deleted_at IS NULL;

-- name: GetEnvironmentAttachment :one
SELECT e.session_owned, s.id AS attached_session_id
FROM environments e LEFT JOIN sessions s ON s.environment_id = e.id AND s.deleted_at IS NULL
WHERE e.tenant_id = $1 AND e.id = $2;

-- name: LockEnvironment :one
SELECT e.id FROM environments e WHERE e.tenant_id = $1 AND e.id = $2 FOR UPDATE;

-- name: GetStandaloneEnvironmentCursor :one
SELECT created_at, id FROM environments
WHERE tenant_id = $1 AND id = $2 AND NOT session_owned AND creator_kind = $3 AND creator_id = $4;

-- name: ListStandaloneEnvironments :many
SELECT sqlc.embed(e), s.id AS attached_session_id,
       EXISTS (SELECT 1 FROM environment_workspaces w WHERE w.environment_id = e.id)::boolean AS external_workspace
FROM environments e LEFT JOIN sessions s ON s.environment_id = e.id AND s.deleted_at IS NULL
WHERE e.tenant_id = sqlc.arg(tenant_id) AND NOT e.session_owned
  AND e.creator_kind = sqlc.arg(creator_kind) AND e.creator_id = sqlc.arg(creator_id)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (NOT sqlc.arg(ascending)::boolean AND (e.created_at, e.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
       OR (sqlc.arg(ascending)::boolean AND (e.created_at, e.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN e.created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN e.id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN e.created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN e.id END DESC
LIMIT sqlc.arg(page_limit);
