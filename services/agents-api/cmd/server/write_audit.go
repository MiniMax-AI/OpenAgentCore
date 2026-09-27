package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/coremetrics"
)

type writeAuditPruner interface {
	DeleteExpiredWriteOperations(context.Context, time.Time, int) (int64, error)
}

func writeAuditRetention() (time.Duration, error) {
	value := os.Getenv("OAC_WRITE_AUDIT_RETENTION")
	if value == "" {
		return 90 * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < time.Hour {
		return 0, errors.New("OAC_WRITE_AUDIT_RETENTION must be a duration of at least 1h")
	}
	return duration, nil
}

func runWriteAuditCleanup(ctx context.Context, s writeAuditPruner, retention time.Duration, metrics *coremetrics.Service) {
	if metrics != nil {
		defer metrics.StopJob("audit_cleanup")
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		pruneCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		count, err := s.DeleteExpiredWriteOperations(pruneCtx, time.Now().Add(-retention), 1000)
		cancel()
		reportCleanupResult(metrics, "audit_cleanup", count, err)
		if err != nil && ctx.Err() == nil {
			log.Ctx(ctx).Warn("Write audit retention cleanup failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
