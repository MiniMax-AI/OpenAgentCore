package main

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/coremetricspg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Release builders set this full source commit with -ldflags.
var buildRevision string
var processStartedAt = time.Now().UTC()

type coreMetricsSource struct {
	store    *coremetricspg.Store
	pool     *pgxpool.Pool
	worker   *execution.Worker
	registry *runtimegateway.Registry
}

func metricPtr[T any](value T) *T { return &value }
func (s *coreMetricsSource) Live() coremetrics.Live {
	stat := s.pool.Stat()
	w := s.worker.MetricsSnapshot()
	return coremetrics.Live{Pool: coremetrics.Pool{InUse: metricPtr(int64(stat.AcquiredConns())), Idle: metricPtr(int64(stat.IdleConns())), Max: metricPtr(int64(stat.MaxConns()))},
		Scheduler:      coremetrics.Job{ID: "scheduler", Status: w.Scheduler.Status, LastRunAt: w.Scheduler.LastRunAt, Processed: w.Scheduler.Processed, Failed: w.Scheduler.Failed},
		ExecutionOwner: w.ExecutionOwner, SlotsTotal: w.SlotsTotal, SlotsInUse: w.SlotsInUse,
		ConnectedDaemons: metricPtr(int64(len(s.registry.Devices())))}
}
func (s *coreMetricsSource) Sample(ctx context.Context) coremetrics.Sample {
	sample := coremetrics.Sample{Healthy: true, PoolInUse: metricPtr(int64(s.pool.Stat().AcquiredConns()))}
	start := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, time.Second)
	err := s.pool.Ping(pingCtx)
	cancel()
	if err == nil {
		sample.PingMS = metricPtr(float64(time.Since(start)) / float64(time.Millisecond))
	} else {
		sample.Healthy = false
	}
	counts, err := s.store.ReadExecutionSnapshot(ctx, time.Now(), s.registry.Devices())
	if err != nil {
		sample.Healthy = false
	} else {
		sample.Queued, sample.InProgress = metricPtr(counts.QueuedTurns), metricPtr(counts.InProgressTurns)
		sample.OldestQueuedSeconds, sample.WaitingForDaemon = counts.OldestQueuedSeconds, metricPtr(counts.WaitingForDaemon)
	}
	size, err := s.store.ReadDatabaseSize(ctx)
	if err != nil {
		sample.Healthy = false
	} else {
		sample.DatabaseSize = &size
	}

	return sample
}
func (s *coreMetricsSource) History(ctx context.Context, start, end time.Time, step time.Duration) (coremetrics.History, error) {
	return s.store.ReadExecutionHistory(ctx, start, end, step)
}

// prune makes a retention pass, bounded by timeout, a job that runs every
// minute. A failed pass counts no rows and one failure.
func prune(id string, timeout time.Duration, run func(context.Context) (int64, error)) coremetrics.Periodic {
	return coremetrics.Periodic{ID: id, Every: time.Minute, Run: func(ctx context.Context) (*int64, int64, error) {
		pass, cancel := context.WithTimeout(ctx, timeout)
		count, err := run(pass)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				log.Ctx(ctx).Warn("Retention cleanup failed", "job", id)
			}
			return nil, 1, err
		}
		return &count, 0, nil
	}}
}
