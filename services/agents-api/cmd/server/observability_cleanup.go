package main

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

type operatorMetricsPruner interface {
	PruneOperatorMetrics(context.Context, time.Time) error
}

func runOperatorMetricsCleanup(ctx context.Context, pruner operatorMetricsPruner) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		pruneCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := pruner.PruneOperatorMetrics(pruneCtx, time.Now().UTC())
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Ctx(ctx).Warn("Operator metrics retention cleanup failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
