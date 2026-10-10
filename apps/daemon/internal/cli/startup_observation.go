package cli

import (
	"context"
	"errors"
	"log/slog"
	"time"

	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func observeRuntimeStartup(ctx context.Context, stage string, started time.Time, err error) {
	status := "ok"
	if err != nil {
		status = "error"
		if errors.Is(err, context.Canceled) {
			status = "cancelled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timeout"
		}
	}
	obslog.Ctx(ctx).Info("runtime startup stage", "stage", stage,
		"duration_ms", float64(time.Since(started))/float64(time.Millisecond), "status", status)
}

func initializeRuntimeObservations(rc *runContext) {
	obslog.Init(obslog.Config{Format: "text", Level: slog.LevelInfo, Out: rc.stderr})
	obslog.Bg().Info("runtime process starting", "daemon_version", Version)
}
