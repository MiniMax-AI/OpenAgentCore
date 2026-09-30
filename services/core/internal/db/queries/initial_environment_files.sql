-- name: CreateInitialEnvironmentFile :exec
INSERT INTO initial_environment_files (id, session_id, position, path, size_bytes, contents)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: SetSessionInitialFileMetadata :one
UPDATE sessions SET configuration = jsonb_set(configuration, '{environment,files}', $2::jsonb)
WHERE id = $1 RETURNING *;

-- name: GetInitialEnvironmentFile :one
SELECT f.* FROM initial_environment_files f JOIN sessions s ON s.id = f.session_id
WHERE s.tenant_id = $1 AND f.session_id = $2 AND f.position = $3 AND s.deleted_at IS NULL;

-- name: GetSessionInitializationReady :one
SELECT NOT EXISTS (SELECT 1 FROM environments e WHERE e.session_id = s.id AND e.initialization <> 'complete') AS ready
FROM sessions s WHERE s.tenant_id = $1 AND s.id = $2;

-- name: LockInitialSourceFile :one
SELECT * FROM source_files WHERE tenant_id = $1 AND id = $2 FOR SHARE;
