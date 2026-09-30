-- name: SampleNodeHostHistory :execrows
INSERT INTO node_host_history_samples (node_id, observed_at, cpu_utilization, memory_used_bytes, available_disk_bytes)
SELECT n.id, (n.health->'host'->>'observed_at')::timestamptz,
    (n.health->'host'->>'cpu_utilization')::double precision,
    CASE WHEN (n.health->'host'->>'total_memory_bytes')::bigint >= (n.health->'host'->>'available_memory_bytes')::bigint
        THEN (n.health->'host'->>'total_memory_bytes')::bigint - (n.health->'host'->>'available_memory_bytes')::bigint END,
    (n.health->'host'->>'available_disk_bytes')::bigint
FROM runtime_nodes n CROSS JOIN runtime_deployment d
WHERE n.removed_at IS NULL AND n.installation_id=d.installation_id
    AND n.connection_id IS NOT NULL AND n.connected_epoch=d.owner_epoch
    AND n.last_seen_at > clock_timestamp()-interval '45 seconds'
    AND (n.health->'host'->>'observed_at')::timestamptz > clock_timestamp()-interval '45 seconds'
    AND (n.health->'host'->>'observed_at')::timestamptz <= clock_timestamp()
ON CONFLICT (node_id, observed_at) DO NOTHING;

-- name: ListNodeHostHistory :many
SELECT date_bin(sqlc.arg(bucket_width)::interval, observed_at, '1970-01-01T00:00:00Z'::timestamptz)::timestamptz AS start,
    count(cpu_utilization)::bigint AS cpu_samples,
    COALESCE(max(cpu_utilization), 0)::double precision AS cpu_utilization_max,
    count(memory_used_bytes)::bigint AS memory_samples,
    COALESCE(max(memory_used_bytes), 0)::bigint AS memory_used_bytes_max,
    count(available_disk_bytes)::bigint AS disk_samples,
    COALESCE(min(available_disk_bytes), 0)::bigint AS available_disk_bytes_min
FROM node_host_history_samples
WHERE node_id=sqlc.arg(node_id) AND observed_at >= sqlc.arg(start_at)::timestamptz AND observed_at < sqlc.arg(end_at)::timestamptz
GROUP BY 1 ORDER BY 1;

-- name: PruneNodeHostHistory :execrows
WITH expired AS (
    SELECT node_id, observed_at FROM node_host_history_samples
    WHERE observed_at < sqlc.arg(before_at)::timestamptz
    ORDER BY observed_at LIMIT 256 FOR UPDATE SKIP LOCKED
)
DELETE FROM node_host_history_samples h USING expired e
WHERE h.node_id=e.node_id AND h.observed_at=e.observed_at;
