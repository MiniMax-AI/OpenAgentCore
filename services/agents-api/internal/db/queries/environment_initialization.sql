-- name: ListEnvironmentInitializations :many
SELECT e.id, e.session_id, s.tenant_id, s.engine, e.initialization, b.device_id
FROM environments e JOIN sessions s ON s.id = e.session_id
LEFT JOIN session_devices b ON b.session_id = s.id
WHERE e.id > $1 AND s.deleted_at IS NULL AND e.status NOT IN ('failed', 'expired')
 AND e.initialization IN ('pending', 'running')
ORDER BY e.id LIMIT 32;

-- name: ClaimEnvironmentInitialization :execrows
UPDATE environments SET initialization = 'running'
WHERE id = $1 AND initialization = 'pending' AND status NOT IN ('failed', 'expired');

-- name: CompleteEnvironmentInitialization :execrows
UPDATE environments SET initialization = 'complete'
WHERE id = $1 AND initialization = 'running' AND status NOT IN ('failed', 'expired');

-- name: FailEnvironmentInitialization :exec
UPDATE environments SET initialization = 'failed' WHERE id = $1 AND initialization <> 'complete';
