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
WHERE e.id = sqlc.arg(id) AND e.tenant_id = sqlc.arg(tenant_id) AND e.status NOT IN ('failed', 'expired')
RETURNING e.failed_at;

-- name: ExpireEnvironment :execrows
UPDATE environments e SET status = 'expired'
WHERE e.id = sqlc.arg(id) AND e.tenant_id = sqlc.arg(tenant_id);

-- name: DeleteEnvironmentConnection :exec
DELETE FROM environment_connections WHERE environment_id = $1;

-- name: ListEnvironmentConnections :many
SELECT e.id, e.tenant_id
FROM environments e
WHERE e.id > $1 AND NOT (SELECT l.ended FROM environment_liveness l WHERE l.id = e.id)
  AND (e.status = 'connected' OR EXISTS (
      SELECT 1 FROM environment_connections c WHERE c.environment_id = e.id
  ))
ORDER BY e.id LIMIT 32;
