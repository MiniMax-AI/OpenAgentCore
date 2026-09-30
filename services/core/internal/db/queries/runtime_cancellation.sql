-- name: GetArchivedCancellationReceipt :one
SELECT t.id, t.cancel_requested_at
FROM devices d
JOIN runtime_allocations a ON a.device_id = d.id
JOIN environments e ON e.id = a.environment_id AND e.id = d.environment_id
JOIN sessions s ON s.id = e.session_id AND s.tenant_id = d.tenant_id
JOIN turns t ON t.session_id = s.id AND t.id = d.archive_cancel_turn_id
WHERE d.id = sqlc.arg(device_id)
  AND d.credential_hash = sqlc.arg(credential_hash)
  AND d.executor_key_id IS NULL AND d.revoked_at IS NOT NULL
  AND a.state = 'cleanup_pending' AND a.released_at IS NULL
  AND e.status = 'expired' AND s.deleted_at IS NULL
  AND s.configuration->'environment'->>'type' = 'openai_hosted'
  AND t.status IN ('in_progress', 'waiting')
  AND t.id::text = ANY(sqlc.arg(run_ids)::text[])
  AND t.cancel_requested_at IS NOT NULL
  AND d.revoked_at >= t.cancel_requested_at
  AND t.cancel_requested_at > clock_timestamp() - sqlc.arg(limit_seconds)::int * interval '1 second';

-- name: RevokeArchivedRuntimeDevice :execrows
UPDATE devices d
SET archive_cancel_turn_id = CASE WHEN d.revoked_at IS NULL THEN (
    SELECT t.id FROM turns t
    WHERE t.session_id = sqlc.arg(session_id)
      AND t.status IN ('in_progress', 'waiting') AND t.cancel_requested_at IS NOT NULL
) ELSE d.archive_cancel_turn_id END,
    revoked_at = COALESCE(d.revoked_at, clock_timestamp())
WHERE d.tenant_id = sqlc.arg(tenant_id) AND d.id = sqlc.arg(device_id);

-- name: RevokeRuntimeCleanupDevice :execrows
UPDATE devices SET revoked_at = COALESCE(revoked_at, clock_timestamp())
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(device_id);
