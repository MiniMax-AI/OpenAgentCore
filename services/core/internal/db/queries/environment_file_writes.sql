-- name: GetEnvironmentFileWrite :one
SELECT sqlc.embed(w)
FROM environment_file_writes w
JOIN environments e ON e.id = w.environment_id
WHERE e.tenant_id = sqlc.arg(tenant_id) AND e.id = sqlc.arg(environment_id) AND w.id = sqlc.arg(id);

-- name: CreateEnvironmentFileWrite :one
INSERT INTO environment_file_writes(id, environment_id, device_id, request_sha256, audit_source)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: SettleEnvironmentFileWrite :one
UPDATE environment_file_writes SET state = $3, settled_at = clock_timestamp()
WHERE environment_id = $1 AND id = $2 AND state = 'pending' RETURNING *;

-- name: EnvironmentFileWriteBlocksSession :one
SELECT EXISTS (
    SELECT 1 FROM sessions s JOIN environment_file_writes w ON w.environment_id = s.environment_id
    WHERE s.id = $1 AND w.state = 'pending'
)::boolean;

-- name: EnvironmentFileWriteHasPendingInput :one
SELECT EXISTS (
    SELECT 1 FROM environment_input_reservations WHERE session_id = $1 AND state = 'pending'
)::boolean;
