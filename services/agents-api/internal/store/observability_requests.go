package store

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/observability"
)

const upsertRequestBucket = `INSERT INTO observability_request_minute_buckets AS current
    (bucket_start, route_family, method, outcome, request_count, latency_sum_ms, latency_bucket_counts)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (bucket_start, route_family, method, outcome) DO UPDATE SET
    request_count = current.request_count + EXCLUDED.request_count,
    latency_sum_ms = current.latency_sum_ms + EXCLUDED.latency_sum_ms,
    latency_bucket_counts = ARRAY[
        current.latency_bucket_counts[1] + EXCLUDED.latency_bucket_counts[1],
        current.latency_bucket_counts[2] + EXCLUDED.latency_bucket_counts[2],
        current.latency_bucket_counts[3] + EXCLUDED.latency_bucket_counts[3],
        current.latency_bucket_counts[4] + EXCLUDED.latency_bucket_counts[4],
        current.latency_bucket_counts[5] + EXCLUDED.latency_bucket_counts[5],
        current.latency_bucket_counts[6] + EXCLUDED.latency_bucket_counts[6],
        current.latency_bucket_counts[7] + EXCLUDED.latency_bucket_counts[7],
        current.latency_bucket_counts[8] + EXCLUDED.latency_bucket_counts[8],
        current.latency_bucket_counts[9] + EXCLUDED.latency_bucket_counts[9],
        current.latency_bucket_counts[10] + EXCLUDED.latency_bucket_counts[10],
        current.latency_bucket_counts[11] + EXCLUDED.latency_bucket_counts[11]
    ]`

// WriteRequestBuckets persists asynchronous HTTP aggregates and collector coverage
// together. It is never called by the request handler's response path.
func (s *Store) WriteRequestBuckets(ctx context.Context, buckets []observability.RequestBucket, dropped, failed int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var written int64
	for _, bucket := range buckets {
		if bucket.Count <= 0 {
			continue
		}
		_, err = tx.Exec(ctx, upsertRequestBucket, bucket.Start, bucket.RouteFamily, bucket.Method, bucket.Outcome,
			bucket.Count, bucket.LatencySumMS, bucket.LatencyCounts[:])
		if err != nil {
			return err
		}
		written += bucket.Count
	}
	_, err = tx.Exec(ctx, `INSERT INTO observability_collector_minute_buckets AS current
			(bucket_start, source, attempted_count, observed_count, dropped_count, export_failed_count)
			VALUES ($1, 'request', $2, $3, $4, $5)
			ON CONFLICT (bucket_start, source) DO UPDATE SET
			attempted_count = current.attempted_count + EXCLUDED.attempted_count,
			observed_count = current.observed_count + EXCLUDED.observed_count,
			dropped_count = current.dropped_count + EXCLUDED.dropped_count,
			export_failed_count = current.export_failed_count + EXCLUDED.export_failed_count`,
		time.Now().UTC().Truncate(time.Minute), written+dropped, written, dropped, failed)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
