package store

import (
	"context"
	"time"
)

// PruneOperatorMetrics deletes small batches so retention never holds a long lock.
func (s *Store) PruneOperatorMetrics(ctx context.Context, now time.Time) error {
	cutoffs := []struct {
		query  string
		before time.Time
	}{
		{`DELETE FROM observability_request_minute_buckets WHERE ctid IN
			(SELECT ctid FROM observability_request_minute_buckets WHERE bucket_start < $1 LIMIT 256 FOR UPDATE SKIP LOCKED)`, now.Add(-30 * 24 * time.Hour)},
		{`DELETE FROM observability_collector_minute_buckets WHERE ctid IN
			(SELECT ctid FROM observability_collector_minute_buckets WHERE bucket_start < $1 LIMIT 256 FOR UPDATE SKIP LOCKED)`, now.Add(-30 * 24 * time.Hour)},
		{`DELETE FROM observability_model_attempts WHERE id IN
			(SELECT id FROM observability_model_attempts WHERE started_at < $1 LIMIT 256 FOR UPDATE SKIP LOCKED)`, now.Add(-7 * 24 * time.Hour)},
		{`DELETE FROM observability_tool_attempts WHERE id IN
			(SELECT id FROM observability_tool_attempts WHERE finished_at < $1 LIMIT 256 FOR UPDATE SKIP LOCKED)`, now.Add(-7 * 24 * time.Hour)},
	}
	for _, cutoff := range cutoffs {
		if _, err := s.pool.Exec(ctx, cutoff.query, cutoff.before); err != nil {
			return err
		}
	}
	return nil
}
