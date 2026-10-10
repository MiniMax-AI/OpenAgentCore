-- name: CreateEnvironmentDevice :one
INSERT INTO devices (id, tenant_id, name, credential_hash, environment_id)
SELECT sqlc.arg(id), s.tenant_id, sqlc.arg(name), sqlc.arg(credential_hash), e.id
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = sqlc.arg(tenant_id) AND s.id = sqlc.arg(session_id) AND e.id = sqlc.arg(environment_id)
AND s.deleted_at IS NULL AND s.configuration->'environment'->>'type' = 'openai_hosted'
AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state <> 'released')
AND NOT EXISTS (SELECT 1 FROM devices previous WHERE previous.environment_id = e.id AND (previous.revoked_at IS NULL OR previous.executor_key_id IS NOT NULL))
ON CONFLICT (environment_id) WHERE revoked_at IS NULL OR executor_key_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: BindHostedSessionDevice :one
INSERT INTO session_devices(session_id, device_id)
SELECT s.id, d.id FROM sessions s JOIN devices d ON d.tenant_id = s.tenant_id
JOIN environments e ON e.id = d.environment_id AND e.session_id = s.id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.id = $3 AND d.revoked_at IS NULL
  AND d.executor_key_id IS NULL AND s.deleted_at IS NULL
  AND s.configuration->'environment'->>'type' = 'openai_hosted'
ON CONFLICT (session_id) DO UPDATE SET device_id = EXCLUDED.device_id
WHERE session_devices.device_id = EXCLUDED.device_id OR (
  EXISTS (SELECT 1 FROM devices previous
    JOIN runtime_allocations a ON a.device_id = previous.id
    WHERE previous.id = session_devices.device_id AND previous.revoked_at IS NOT NULL
      AND previous.executor_key_id IS NULL AND a.state = 'released'
      AND previous.environment_id = (SELECT environment_id FROM devices WHERE id = EXCLUDED.device_id))
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a
    WHERE a.environment_id = (SELECT environment_id FROM devices WHERE id = EXCLUDED.device_id)
      AND a.state <> 'released')
)
RETURNING device_id;
