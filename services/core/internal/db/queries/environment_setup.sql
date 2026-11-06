-- name: CreateEnvironmentSetup :exec
INSERT INTO environment_setups (environment_id, contents) VALUES ($1, $2);

-- name: GetEnvironmentSetup :one
SELECT f.contents, l.ended::boolean AS ended
FROM environments e JOIN environment_liveness l ON l.id = e.id LEFT JOIN environment_setups f ON f.environment_id = e.id
WHERE e.tenant_id = $1 AND e.id = $2;

-- name: SetEnvironmentSetupMetadata :exec
UPDATE environments SET configuration = configuration || jsonb_build_object(
        'packages', sqlc.arg(packages)::jsonb,
        'skills', sqlc.arg(skills)::jsonb,
        'plugins', sqlc.arg(plugins)::jsonb,
        'capability_directories', sqlc.arg(capability_directories)::jsonb,
        'initialization', true)
WHERE id = $1;
