-- name: CoreExecutionSnapshot :one
SELECT count(*) FILTER (WHERE t.status = 'queued')::bigint AS queued_turns,
    count(*) FILTER (WHERE t.status = 'queued' AND
        (b.device_id IS NULL OR NOT (b.device_id = ANY(sqlc.arg(connected_device_ids)::uuid[]))))::bigint AS waiting_for_daemon,
    count(*) FILTER (WHERE t.status = 'in_progress')::bigint AS in_progress_turns,
    COALESCE(GREATEST(0, extract(epoch FROM (sqlc.arg(observed_at)::timestamptz -
        min(t.created_at) FILTER (WHERE t.status = 'queued')))), 0)::double precision AS oldest_queued_seconds
FROM turns t LEFT JOIN session_devices b ON b.session_id = t.session_id
WHERE t.status IN ('queued', 'in_progress');

-- name: CoreInterruptedTurns :one
SELECT count(*)::bigint FROM turns
WHERE status = 'failed' AND outcome->>'error_code' = 'execution_interrupted'
    AND completed_at >= sqlc.arg(range_start)::timestamptz
    AND completed_at < sqlc.arg(range_end)::timestamptz;

-- name: CoreQueueWaitSummary :one
SELECT count(*)::bigint AS samples,
    COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY extract(epoch FROM (started_at - created_at)) * 1000), 0)::double precision AS p50_ms,
    COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM (started_at - created_at)) * 1000), 0)::double precision AS p95_ms
FROM turns
WHERE started_at >= sqlc.arg(range_start)::timestamptz
    AND started_at < sqlc.arg(range_end)::timestamptz;

-- name: CoreQueueWaitBuckets :many
SELECT floor(extract(epoch FROM (started_at - sqlc.arg(range_start)::timestamptz)) /
        sqlc.arg(resolution_seconds)::integer)::integer AS bucket_number,
    count(*)::bigint AS samples,
    COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM (started_at - created_at)) * 1000), 0)::double precision AS p95_ms
FROM turns
WHERE started_at >= sqlc.arg(range_start)::timestamptz
    AND started_at < sqlc.arg(range_end)::timestamptz
GROUP BY bucket_number
ORDER BY bucket_number;

-- name: CoreDatabaseSize :one
SELECT pg_database_size(current_database())::bigint;
