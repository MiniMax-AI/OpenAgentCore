-- name: CreateEnvironmentSetup :exec
INSERT INTO environment_setups (environment_id, contents) VALUES ($1, $2);

-- name: GetEnvironmentSetup :one
SELECT f.contents, (SELECT s.id FROM sessions s WHERE s.environment_id = e.id AND s.deleted_at IS NULL) AS owner_session_id
FROM environments e LEFT JOIN environment_setups f ON f.environment_id = e.id
WHERE e.tenant_id = $1 AND e.id = $2;

-- name: SetEnvironmentSetupMetadata :exec
UPDATE environments SET configuration = configuration || jsonb_build_object(
        'packages', sqlc.arg(packages)::jsonb,
        'skills', sqlc.arg(skills)::jsonb,
        'plugins', sqlc.arg(plugins)::jsonb,
        'capability_directories', sqlc.arg(capability_directories)::jsonb,
        'initialization', true)
WHERE id = $1;
