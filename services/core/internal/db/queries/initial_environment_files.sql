-- name: CreateInitialEnvironmentFile :exec
INSERT INTO initial_environment_files (id, environment_id, position, path, size_bytes, contents)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: SetEnvironmentInitialFileMetadata :exec
UPDATE environments SET configuration = jsonb_set(configuration, '{files}', $2::jsonb)
WHERE id = $1;

-- name: GetInitialEnvironmentFile :one
SELECT f.*, (e.session_owned AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.environment_id = e.id AND s.deleted_at IS NULL))::boolean AS reclaimed
FROM initial_environment_files f JOIN environments e ON e.id = f.environment_id
WHERE e.tenant_id = $1 AND f.environment_id = $2 AND f.position = $3;

-- name: GetSessionInitializationReady :one
SELECT NOT EXISTS (SELECT 1 FROM environments e WHERE e.id = s.environment_id AND e.initialization <> 'complete') AS ready
FROM sessions s WHERE s.tenant_id = $1 AND s.id = $2;
