-- name: ListEnvironmentInitializations :many
SELECT e.id, e.tenant_id, e.initialization, d.id AS device_id
FROM environments e
LEFT JOIN devices d ON d.environment_id = e.id AND d.revoked_at IS NULL
WHERE e.id > $1 AND (NOT e.session_owned OR EXISTS (SELECT 1 FROM sessions s WHERE s.environment_id = e.id AND s.deleted_at IS NULL))
 AND e.status NOT IN ('failed', 'expired')
 AND e.initialization IN ('pending', 'running')
ORDER BY e.id LIMIT 32;

-- name: ClaimEnvironmentInitialization :execrows
UPDATE environments SET initialization = 'running'
WHERE id = $1 AND initialization = 'pending' AND status NOT IN ('failed', 'expired');

-- name: CompleteEnvironmentInitialization :execrows
UPDATE environments SET initialization = 'complete'
WHERE id = $1 AND initialization = 'running' AND status NOT IN ('failed', 'expired');

-- name: RecordEnvironmentInitializationSteps :exec
UPDATE environments SET initialization_steps = sqlc.arg(steps)
WHERE id = sqlc.arg(id) AND initialization = 'running';

-- name: FailEnvironmentInitialization :exec
UPDATE environments SET initialization = 'failed' WHERE id = $1 AND initialization <> 'complete';
