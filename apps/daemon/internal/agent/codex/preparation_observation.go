package codex

import (
	"context"
	"time"

	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// Callers supply fixed stage names. Never include native error text, environment,
// catalog contents or command output; the owner context carries trace correlation.
func observePreparationStage(ctx context.Context, stage string, started time.Time, err error) {
	observePreparationInterval(ctx, stage, started, time.Now(), err)
}

func observePreparationInterval(ctx context.Context, stage string, started, ended time.Time, err error) {
	obslog.Ctx(ctx).Info("codex preparation stage", "stage", stage,
		"duration_ms", float64(ended.Sub(started))/float64(time.Millisecond), "success", err == nil)
}
