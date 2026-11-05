-- name: CreateEnvironment :exec
INSERT INTO environments (id, tenant_id, configuration, session_owned, initialization)
VALUES (sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(configuration), true, CASE WHEN sqlc.arg(prepared)::boolean THEN 'pending' ELSE 'complete' END);

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
SELECT e.session_owned, (SELECT s.id FROM sessions s WHERE s.environment_id = e.id AND s.deleted_at IS NULL) AS attached_session_id
FROM environments e WHERE e.tenant_id = $1 AND e.id = $2 FOR UPDATE;
