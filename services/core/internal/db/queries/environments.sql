-- name: CreateEnvironment :exec
INSERT INTO environments (id, session_id, initialization)
VALUES ($1, $2, CASE WHEN EXISTS (SELECT 1 FROM initial_environment_files WHERE session_id = $2)
 OR EXISTS (SELECT 1 FROM environment_setups WHERE session_id = $2) THEN 'pending' ELSE 'complete' END);

-- name: GetEnvironment :one
SELECT sqlc.embed(e), s.tenant_id, (s.configuration->'environment')::jsonb AS configuration
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND e.id = $2 AND s.deleted_at IS NULL;

-- name: GetSessionEnvironment :one
SELECT sqlc.embed(e), s.tenant_id, (s.configuration->'environment')::jsonb AS configuration
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND s.id = $2 AND s.deleted_at IS NULL;
