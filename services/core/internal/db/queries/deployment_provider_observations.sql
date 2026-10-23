-- name: ObserveDeploymentModelProvider :execrows
-- Root turns live in turns; child turns have their own table. Read only committed
-- outcomes, lock only the matching default, and sample receipt time after its lock.
WITH locked AS MATERIALIZED (
    SELECT d.harness, d.revision, d.last_used_at, d.last_error_at, d.recovery_pending,
           t.status = 'completed' AS success, t.outcome->>'engine_error_code' AS provider_code
    FROM deployment_model_providers d
    JOIN sessions s ON s.engine = d.harness
    JOIN session_execution_configuration c ON c.session_id = s.id
    JOIN turns t ON t.session_id = s.id
    WHERE s.tenant_id = @tenant_id AND s.id = @session_id AND t.id = @turn_id
      AND c.configuration #>> '{model_provider,source}' = 'deployment'
      AND c.deployment_provider_revision = d.revision
      AND (t.status = 'completed' OR
           (t.status = 'failed' AND t.outcome->>'error_code' = 'engine_failed'
            AND t.outcome->>'engine_error_code' IN (
                'authentication_error', 'connection_failed', 'rate_limit_exceeded',
                'usage_limit_exceeded', 'server_overloaded', 'server_error',
                'resource_not_found', 'request_timeout', 'invalid_request')))
    FOR UPDATE OF d
), observed AS MATERIALIZED (
    SELECT harness, revision, clock_timestamp() AS received_at FROM locked
)
UPDATE deployment_model_providers d
SET last_used_at = CASE WHEN l.success THEN o.received_at ELSE d.last_used_at END,
    last_error_code = CASE WHEN l.success THEN d.last_error_code ELSE l.provider_code END,
    last_error_at = CASE WHEN l.success THEN d.last_error_at ELSE o.received_at END,
    recovery_pending = NOT l.success
FROM locked l JOIN observed o ON o.harness = l.harness AND o.revision = l.revision
WHERE d.harness = l.harness AND d.revision = l.revision
  AND ((l.success AND (l.recovery_pending OR l.last_used_at IS NULL
                      OR o.received_at >= l.last_used_at + interval '30 seconds'))
       OR (NOT l.success AND (l.last_error_at IS NULL
                              OR o.received_at >= l.last_error_at + interval '30 seconds')));
