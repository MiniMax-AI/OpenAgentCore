package store

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type CoreExecutionSnapshot struct {
	QueuedTurns, WaitingForDaemon, InProgressTurns int64
	OldestQueuedSeconds                            *float64
}

type CoreQueueWait struct{ P50, P95 *float64 }

type CoreQueueWaitBucket struct {
	Start time.Time
	P95MS *float64
}

type CoreExecutionHistory struct {
	Interrupted int64
	QueueWaitMS CoreQueueWait
	Buckets     []CoreQueueWaitBucket
}

// ReadCoreExecutionSnapshot counts persisted root Turns across the deployment.
// WaitingForDaemon is the queued subset whose Session binding is absent from the
// supplied live registry IDs; callers must distinguish an unavailable registry
// from an observed empty one before using this method. Age is in seconds, and is
// nil when the queue is empty. No Session deletion filter hides operational state.
func (s *Store) ReadCoreExecutionSnapshot(ctx context.Context, now time.Time, connectedDeviceIDs []string) (CoreExecutionSnapshot, error) {
	ids := make([]pgtype.UUID, 0, len(connectedDeviceIDs))
	for _, value := range connectedDeviceIDs {
		id, err := parseID(value)
		if err != nil {
			return CoreExecutionSnapshot{}, err
		}
		ids = append(ids, id)
	}
	row, err := s.queries.CoreExecutionSnapshot(ctx, sqlc.CoreExecutionSnapshotParams{
		ConnectedDeviceIds: ids, ObservedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return CoreExecutionSnapshot{}, err
	}
	result := CoreExecutionSnapshot{QueuedTurns: row.QueuedTurns, WaitingForDaemon: row.WaitingForDaemon, InProgressTurns: row.InProgressTurns}
	if row.QueuedTurns > 0 {
		result.OldestQueuedSeconds = &row.OldestQueuedSeconds
	}
	return result, nil
}

// ReadCoreExecutionHistory reads a bounded, repeatable read-only snapshot of root
// Turn history. The interval is [start,end), with complete epoch-aligned buckets.
// Interruptions use failed Turns' completed_at and execution_interrupted error
// code. Queue waits use started_at-created_at in milliseconds, grouped by
// started_at, including retained history of deleted Sessions. Counts of an empty
// interval are zero; percentile values without observations are nil. Read errors
// invalidate the entire result and must not be presented as measured zeros.
func (s *Store) ReadCoreExecutionHistory(ctx context.Context, start, end time.Time, resolution time.Duration) (CoreExecutionHistory, error) {
	span := end.Sub(start)
	if resolution < time.Second || resolution%time.Second != 0 || span <= 0 || span > 7*24*time.Hour ||
		span%resolution != 0 || span/resolution > 1008 || start.Nanosecond() != 0 || end.Nanosecond() != 0 ||
		start.Unix()%int64(resolution/time.Second) != 0 || end.Unix()%int64(resolution/time.Second) != 0 {
		return CoreExecutionHistory{}, sessions.ErrInvalidInput
	}
	result := CoreExecutionHistory{Buckets: make([]CoreQueueWaitBucket, int(span/resolution))}
	for i := range result.Buckets {
		result.Buckets[i].Start = start.Add(time.Duration(i) * resolution).UTC()
	}
	first, last := pgtype.Timestamptz{Time: start, Valid: true}, pgtype.Timestamptz{Time: end, Valid: true}
	err := s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		result.Interrupted, err = q.CoreInterruptedTurns(ctx, sqlc.CoreInterruptedTurnsParams{RangeStart: first, RangeEnd: last})
		if err != nil {
			return err
		}
		wait, err := q.CoreQueueWaitSummary(ctx, sqlc.CoreQueueWaitSummaryParams{RangeStart: first, RangeEnd: last})
		if err != nil {
			return err
		}
		if wait.Samples > 0 {
			result.QueueWaitMS = CoreQueueWait{P50: &wait.P50Ms, P95: &wait.P95Ms}
		}
		rows, err := q.CoreQueueWaitBuckets(ctx, sqlc.CoreQueueWaitBucketsParams{RangeStart: first, RangeEnd: last, ResolutionSeconds: int32(resolution / time.Second)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Samples > 0 {
				result.Buckets[row.BucketNumber].P95MS = &row.P95Ms
			}
		}
		return nil
	})
	if err != nil {
		return CoreExecutionHistory{}, err
	}
	return result, nil
}

// ReadCoreDatabaseSize measures the current PostgreSQL database in bytes.
func (s *Store) ReadCoreDatabaseSize(ctx context.Context) (int64, error) {
	return s.queries.CoreDatabaseSize(ctx)
}
