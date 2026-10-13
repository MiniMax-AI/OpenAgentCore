// Package coremetricspg reads the Core metrics that PostgreSQL holds: the
// deployment's root Turn queue and its history, and the database size.
package coremetricspg

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads Core metrics from Core's database.
type Store struct {
	units *pgunit.Pool
}

// New builds the Core metrics reader on units.
func New(units *pgunit.Pool) *Store { return &Store{units: units} }

// ReadExecutionSnapshot counts persisted root Turns across the deployment.
// WaitingForDaemon is the queued subset whose Session binding is absent from the
// supplied live registry IDs; callers must distinguish an unavailable registry
// from an observed empty one before using this method. Age is in seconds, and is
// nil when the queue is empty. No Session deletion filter hides operational state.
func (s *Store) ReadExecutionSnapshot(ctx context.Context, now time.Time, connectedDeviceIDs []string) (coremetrics.ExecutionSnapshot, error) {
	ids := make([]pgtype.UUID, 0, len(connectedDeviceIDs))
	for _, value := range connectedDeviceIDs {
		id, err := pgunit.ParseID(value)
		if err != nil {
			return coremetrics.ExecutionSnapshot{}, err
		}
		ids = append(ids, id)
	}
	row, err := s.units.Queries().CoreExecutionSnapshot(ctx, sqlc.CoreExecutionSnapshotParams{
		ConnectedDeviceIds: ids, ObservedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return coremetrics.ExecutionSnapshot{}, err
	}
	result := coremetrics.ExecutionSnapshot{QueuedTurns: row.QueuedTurns, WaitingForDaemon: row.WaitingForDaemon, InProgressTurns: row.InProgressTurns}
	if row.QueuedTurns > 0 {
		result.OldestQueuedSeconds = &row.OldestQueuedSeconds
	}
	return result, nil
}

// ReadExecutionHistory reads a bounded, repeatable read-only snapshot of root
// Turn history. The interval is [start,end), with complete epoch-aligned buckets
// keyed by their UTC start. Interruptions use failed Turns' completed_at and
// execution_interrupted error code. Queue waits use started_at-created_at in
// milliseconds, grouped by started_at, including retained history of deleted
// Sessions. Counts of an empty interval are zero; percentile values without
// observations are nil. Read errors invalidate the entire result and must not be
// presented as measured zeros.
func (s *Store) ReadExecutionHistory(ctx context.Context, start, end time.Time, resolution time.Duration) (coremetrics.History, error) {
	span := end.Sub(start)
	if resolution < time.Second || resolution%time.Second != 0 || span <= 0 || span > 7*24*time.Hour ||
		span%resolution != 0 || span/resolution > 1008 || start.Nanosecond() != 0 || end.Nanosecond() != 0 ||
		start.Unix()%int64(resolution/time.Second) != 0 || end.Unix()%int64(resolution/time.Second) != 0 {
		return coremetrics.History{}, coremetrics.ErrInvalidRange
	}
	buckets := make([]time.Time, int(span/resolution))
	result := coremetrics.History{Buckets: make(map[time.Time]*float64, len(buckets))}
	for i := range buckets {
		buckets[i] = start.Add(time.Duration(i) * resolution).UTC()
		result.Buckets[buckets[i]] = nil
	}
	first, last := pgtype.Timestamptz{Time: start, Valid: true}, pgtype.Timestamptz{Time: end, Valid: true}
	err := s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
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
			result.QueueWaitMS = coremetrics.Latency{P50: &wait.P50Ms, P95: &wait.P95Ms}
		}
		rows, err := q.CoreQueueWaitBuckets(ctx, sqlc.CoreQueueWaitBucketsParams{RangeStart: first, RangeEnd: last, ResolutionSeconds: int32(resolution / time.Second)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Samples > 0 {
				result.Buckets[buckets[row.BucketNumber]] = &row.P95Ms
			}
		}
		return nil
	})
	if err != nil {
		return coremetrics.History{}, err
	}
	return result, nil
}

// ReadDatabaseSize measures the current PostgreSQL database in bytes.
func (s *Store) ReadDatabaseSize(ctx context.Context) (int64, error) {
	return s.units.Queries().CoreDatabaseSize(ctx)
}
