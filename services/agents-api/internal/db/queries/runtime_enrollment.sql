-- name: AuthorizeRuntimeEnrollment :one
SELECT c.key_id, e.session_id, COALESCE(s.configuration->'environment'->>'workspace_directory', '')::text AS workspace_directory
FROM environment_executor_credentials c
JOIN environments e ON e.id = sqlc.arg(environment_id)
JOIN sessions s ON s.id = e.session_id
JOIN execution_project_scopes p ON p.tenant_id = c.tenant_id
WHERE c.token_sha256 = sqlc.arg(token_sha256) AND c.revoked_at IS NULL
    AND c.tenant_id = s.tenant_id AND s.tenant_id = sqlc.arg(tenant_id)
    AND c.subject_kind = s.creator_kind AND c.subject_id = s.creator_id
    AND (c.environment_id IS NULL OR c.environment_id = e.id)
    AND s.deleted_at IS NULL AND e.status NOT IN ('failed', 'expired')
    AND s.configuration->'environment'->>'type' = 'self_hosted'
FOR SHARE OF c;

-- name: EnrollRuntimeDevice :one
INSERT INTO devices (id, tenant_id, name, environment_id, executor_key_id)
VALUES (sqlc.arg(id), sqlc.arg(tenant_id), 'User-managed Runtime', sqlc.arg(environment_id), sqlc.arg(executor_key_id))
ON CONFLICT (environment_id) DO UPDATE SET name = devices.name
WHERE devices.executor_key_id = EXCLUDED.executor_key_id AND devices.revoked_at IS NULL
RETURNING id, name, environment_id;

-- name: TouchAuthenticatedDevice :execrows
UPDATE devices SET last_seen_at = clock_timestamp()
WHERE devices.id = sqlc.arg(id) AND EXISTS (
    SELECT 1 FROM runtime_device_authority a
    WHERE a.id = devices.id AND a.credential_hash = sqlc.arg(credential_hash)
);

-- name: ListEnrolledRuntimeBindings :many
SELECT d.id AS device_id, d.tenant_id, e.id AS environment_id, e.session_id
FROM devices d
JOIN environments e ON e.id = d.environment_id
JOIN sessions s ON s.id = e.session_id AND s.tenant_id = d.tenant_id
WHERE d.executor_key_id IS NOT NULL AND s.deleted_at IS NULL
    AND e.status NOT IN ('failed', 'expired')
ORDER BY d.id;
