package store

import (
	"context"
	"time"
)

type RequestMetricBucket struct {
	Start         time.Time `json:"start"`
	RouteFamily   string    `json:"route_family"`
	Outcome       string    `json:"outcome"`
	Count         int64     `json:"count"`
	LatencySumMS  int64     `json:"latency_sum_ms"`
	LatencyCounts []int64   `json:"latency_bucket_counts"`
}

type CollectorMetricBucket struct {
	Start             time.Time `json:"start"`
	Source            string    `json:"source"`
	AttemptedCount    int64     `json:"attempted_count"`
	ObservedCount     int64     `json:"observed_count"`
	UnavailableCount  int64     `json:"unavailable_count"`
	TimeoutCount      int64     `json:"timeout_count"`
	DroppedCount      int64     `json:"dropped_count"`
	ExportFailedCount int64     `json:"export_failed_count"`
}

type OperatorMetrics struct {
	Requests  []RequestMetricBucket   `json:"requests"`
	Collector []CollectorMetricBucket `json:"collector"`
	Turns     []TurnMetricBucket      `json:"turns"`
	Tools     []ToolMetricBucket      `json:"tools"`
}

type ToolMetricBucket struct {
	Start         time.Time `json:"start"`
	Category      string    `json:"category"`
	Outcome       string    `json:"outcome"`
	Count         int64     `json:"count"`
	TimedCount    int64     `json:"timed_count"`
	DurationP95MS *float64  `json:"duration_p95_ms"`
}

type TurnMetricBucket struct {
	Start          time.Time `json:"start"`
	Status         string    `json:"status"`
	Count          int64     `json:"count"`
	QueueP95MS     *float64  `json:"queue_p95_ms"`
	ExecutionP95MS *float64  `json:"execution_p95_ms"`
}

const requestMetricSelect = `SELECT
  to_timestamp(floor(extract(epoch FROM bucket_start) / $3) * $3) AS bucket,
  route_family, outcome, sum(request_count)::bigint, sum(latency_sum_ms)::bigint,
  ARRAY[
    sum(latency_bucket_counts[1])::bigint, sum(latency_bucket_counts[2])::bigint,
    sum(latency_bucket_counts[3])::bigint, sum(latency_bucket_counts[4])::bigint,
    sum(latency_bucket_counts[5])::bigint, sum(latency_bucket_counts[6])::bigint,
    sum(latency_bucket_counts[7])::bigint, sum(latency_bucket_counts[8])::bigint,
    sum(latency_bucket_counts[9])::bigint, sum(latency_bucket_counts[10])::bigint,
    sum(latency_bucket_counts[11])::bigint
  ] AS latency_bucket_counts
FROM observability_request_minute_buckets
WHERE bucket_start >= $1 AND bucket_start < $2
GROUP BY bucket, route_family, outcome ORDER BY bucket, route_family, outcome`

const collectorMetricSelect = `SELECT
  to_timestamp(floor(extract(epoch FROM bucket_start) / $3) * $3) AS bucket,
  source, sum(attempted_count)::bigint, sum(observed_count)::bigint,
  sum(unavailable_count)::bigint, sum(timeout_count)::bigint,
  sum(dropped_count)::bigint, sum(export_failed_count)::bigint
FROM observability_collector_minute_buckets
WHERE bucket_start >= $1 AND bucket_start < $2
GROUP BY bucket, source ORDER BY bucket, source`

const turnMetricSelect = `SELECT
  to_timestamp(floor(extract(epoch FROM completed_at) / $3) * $3) AS bucket,
  status, count(*)::bigint,
  (percentile_disc(0.95) WITHIN GROUP (ORDER BY greatest(extract(epoch FROM started_at - created_at) * 1000, 0))
    FILTER (WHERE started_at IS NOT NULL))::double precision,
  (percentile_disc(0.95) WITHIN GROUP (ORDER BY greatest(extract(epoch FROM completed_at - started_at) * 1000, 0))
    FILTER (WHERE started_at IS NOT NULL))::double precision
FROM turns
WHERE completed_at >= $1 AND completed_at < $2
  AND status IN ('completed', 'failed', 'cancelled')
GROUP BY bucket, status ORDER BY bucket, status`

const toolMetricSelect = `SELECT
  to_timestamp(floor(extract(epoch FROM finished_at) / $3) * $3) AS bucket,
  tool_category, outcome, count(*)::bigint, count(duration_ms)::bigint,
  (percentile_disc(0.95) WITHIN GROUP (ORDER BY duration_ms)
    FILTER (WHERE duration_ms IS NOT NULL))::double precision
FROM observability_tool_attempts
WHERE finished_at >= $1 AND finished_at < $2
GROUP BY bucket, tool_category, outcome ORDER BY bucket, tool_category, outcome`

// ReadOperatorMetrics returns only bounded labels and aggregate measurements.
func (s *Store) ReadOperatorMetrics(ctx context.Context, start, end time.Time, step time.Duration) (OperatorMetrics, error) {
	result := OperatorMetrics{Requests: []RequestMetricBucket{}, Collector: []CollectorMetricBucket{}, Turns: []TurnMetricBucket{}, Tools: []ToolMetricBucket{}}
	seconds := int64(step / time.Second)
	rows, err := s.pool.Query(ctx, requestMetricSelect, start, end, seconds)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var row RequestMetricBucket
		if err := rows.Scan(&row.Start, &row.RouteFamily, &row.Outcome, &row.Count, &row.LatencySumMS, &row.LatencyCounts); err != nil {
			rows.Close()
			return result, err
		}
		result.Requests = append(result.Requests, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = s.pool.Query(ctx, collectorMetricSelect, start, end, seconds)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var row CollectorMetricBucket
		if err := rows.Scan(&row.Start, &row.Source, &row.AttemptedCount, &row.ObservedCount, &row.UnavailableCount,
			&row.TimeoutCount, &row.DroppedCount, &row.ExportFailedCount); err != nil {
			return result, err
		}
		result.Collector = append(result.Collector, row)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, turnMetricSelect, start, end, seconds)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var row TurnMetricBucket
		if err := rows.Scan(&row.Start, &row.Status, &row.Count, &row.QueueP95MS, &row.ExecutionP95MS); err != nil {
			return result, err
		}
		result.Turns = append(result.Turns, row)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, toolMetricSelect, start, end, seconds)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var row ToolMetricBucket
		if err := rows.Scan(&row.Start, &row.Category, &row.Outcome, &row.Count, &row.TimedCount, &row.DurationP95MS); err != nil {
			return result, err
		}
		result.Tools = append(result.Tools, row)
	}
	return result, rows.Err()
}
