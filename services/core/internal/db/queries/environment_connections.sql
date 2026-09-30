-- name: GetEnvironmentConnection :one
SELECT * FROM environment_connections WHERE environment_id = $1;

-- name: ReplaceEnvironmentConnection :exec
INSERT INTO environment_connections (environment_id, generation, revision)
VALUES ($1, $2, 0)
ON CONFLICT (environment_id) DO UPDATE SET generation = EXCLUDED.generation, revision = 0;

-- name: AdvanceEnvironmentConnection :exec
UPDATE environment_connections SET revision = $2 WHERE environment_id = $1;

-- name: SetEnvironmentConnectionStatus :exec
UPDATE environments SET status = $2 WHERE id = $1;

-- name: RecordEnvironmentFailure :one
UPDATE environments e SET status = 'failed', failure_reason = sqlc.arg(failure_reason), failure_detail = sqlc.arg(failure_detail), failed_at = clock_timestamp()
WHERE e.id = sqlc.arg(id) AND e.session_id = sqlc.arg(session_id) AND e.status NOT IN ('failed', 'expired')
AND EXISTS (SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.tenant_id = sqlc.arg(tenant_id))
RETURNING e.failed_at;

-- name: ExpireSessionEnvironment :execrows
UPDATE environments e SET status = 'expired'
WHERE e.id = sqlc.arg(id) AND e.session_id = sqlc.arg(session_id)
AND EXISTS (SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.tenant_id = sqlc.arg(tenant_id));

-- name: DeleteEnvironmentConnection :exec
DELETE FROM environment_connections WHERE environment_id = $1;

-- name: ListEnvironmentConnections :many
SELECT e.id, e.session_id, s.tenant_id
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE e.id > $1 AND s.deleted_at IS NULL
  AND (e.status = 'connected' OR EXISTS (
      SELECT 1 FROM environment_connections c WHERE c.environment_id = e.id
  ))
ORDER BY e.id LIMIT 32;
