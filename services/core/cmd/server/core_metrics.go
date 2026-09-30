package main

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Release builders set this full source commit with -ldflags.
var buildRevision string
var processStartedAt = time.Now().UTC()

type coreMetricsSource struct {
	store    *store.Store
	pool     *pgxpool.Pool
	worker   *execution.Worker
	registry *gateway.Registry
}

func metricPtr[T any](value T) *T { return &value }
func (s *coreMetricsSource) Live() coremetrics.Live {
	stat := s.pool.Stat()
	live := coremetrics.Live{Pool: coremetrics.Pool{InUse: metricPtr(int64(stat.AcquiredConns())), Idle: metricPtr(int64(stat.IdleConns())), Max: metricPtr(int64(stat.MaxConns()))}, Scheduler: coremetrics.Job{ID: "scheduler", Status: "stopped"}, ExecutionOwner: metricPtr(false), SlotsTotal: metricPtr(int64(0)), SlotsInUse: metricPtr(int64(0))}
	if s.registry != nil {
		live.ConnectedDaemons = metricPtr(int64(len(s.registry.Devices())))
	}
	if s.worker != nil {
		w := s.worker.MetricsSnapshot()
		live.SlotsInUse, live.SlotsTotal, live.ExecutionOwner = w.SlotsInUse, w.SlotsTotal, w.ExecutionOwner
		live.Scheduler = coremetrics.Job{ID: "scheduler", Status: w.Scheduler.Status, LastRunAt: w.Scheduler.LastRunAt, Processed: w.Scheduler.Processed, Failed: w.Scheduler.Failed}
	}
	return live
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
	devices := []string{}
	if s.registry != nil {
		devices = s.registry.Devices()
	}
	counts, err := s.store.ReadCoreExecutionSnapshot(ctx, time.Now(), devices)
	if err != nil {
		sample.Healthy = false
	} else {
		sample.Queued, sample.InProgress = metricPtr(counts.QueuedTurns), metricPtr(counts.InProgressTurns)
		sample.OldestQueuedSeconds = counts.OldestQueuedSeconds
		if s.registry != nil {
			sample.WaitingForDaemon = metricPtr(counts.WaitingForDaemon)
		}
	}
	size, err := s.store.ReadCoreDatabaseSize(ctx)
	if err != nil {
		sample.Healthy = false
	} else {
		sample.DatabaseSize = &size
	}

	return sample
}
func (s *coreMetricsSource) History(ctx context.Context, start, end time.Time, step time.Duration) (coremetrics.History, error) {
	value, err := s.store.ReadCoreExecutionHistory(ctx, start, end, step)
	if err != nil {
		return coremetrics.History{}, err
	}
	result := coremetrics.History{Interrupted: value.Interrupted, QueueWaitMS: coremetrics.Latency{P50: value.QueueWaitMS.P50, P95: value.QueueWaitMS.P95}, Buckets: map[time.Time]*float64{}}
	for _, bucket := range value.Buckets {
		result.Buckets[bucket.Start.UTC()] = bucket.P95MS
	}
	return result, nil
}

func reportCleanupResult(metrics *coremetrics.Service, job string, count int64, err error) {
	if metrics == nil {
		return
	}
	processed, failed := &count, int64(0)
	if err != nil {
		processed = nil
		failed = 1
	}
	metrics.ReportJob(job, time.Now(), processed, &failed, err)
}
