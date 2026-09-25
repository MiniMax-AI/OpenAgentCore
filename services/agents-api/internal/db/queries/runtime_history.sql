-- name: InsertRuntimeHistorySample :exec
INSERT INTO runtime_history_samples (
    tenant_id, session_id, environment_id, resolved_at_ns, allocation_id, provider_type,
    status, observed_at_ns, started_at_ns, cpu_usage_seconds, cpu_capacity_cores,
    memory_usage_bytes, memory_limit_bytes, input_tokens, output_tokens, cpu_utilization_ratio
)
SELECT s.tenant_id, s.id, e.id, sqlc.arg(resolved_at_ns), sqlc.narg(allocation_id), sqlc.arg(provider_type),
    sqlc.arg(status), sqlc.narg(observed_at_ns), sqlc.narg(started_at_ns), sqlc.narg(cpu_usage_seconds), sqlc.narg(cpu_capacity_cores),
    sqlc.narg(memory_usage_bytes), sqlc.narg(memory_limit_bytes), sqlc.narg(input_tokens), sqlc.narg(output_tokens), sqlc.narg(cpu_utilization_ratio)
FROM sessions s JOIN environments e ON e.session_id = s.id
WHERE s.tenant_id = sqlc.arg(tenant_id) AND s.id = sqlc.arg(session_id) AND e.id = sqlc.arg(environment_id)
ON CONFLICT (tenant_id, session_id, environment_id, resolved_at_ns) DO NOTHING;

-- name: ListRuntimeHistorySamples :many
SELECT * FROM runtime_history_samples
WHERE tenant_id = sqlc.arg(tenant_id) AND session_id = sqlc.arg(session_id) AND environment_id = sqlc.arg(environment_id)
  AND resolved_at_ns >= sqlc.arg(start_ns) AND resolved_at_ns < sqlc.arg(end_ns)
ORDER BY resolved_at_ns
LIMIT sqlc.arg(row_limit);

-- name: PruneRuntimeHistorySamples :execrows
WITH expired AS (
    SELECT p.tenant_id, p.session_id, p.environment_id, p.resolved_at_ns FROM runtime_history_samples p
    WHERE p.resolved_at_ns < sqlc.arg(before_ns)
    ORDER BY p.resolved_at_ns LIMIT 256 FOR UPDATE SKIP LOCKED
)
DELETE FROM runtime_history_samples h USING expired e
WHERE h.tenant_id = e.tenant_id AND h.session_id = e.session_id
  AND h.environment_id = e.environment_id AND h.resolved_at_ns = e.resolved_at_ns;
