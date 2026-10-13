package log

import (
	"context"
	"log/slog"
)

// Warn logs via slog.Default with ctx attached so ContextHandler can
// inject trace_id/span_id from ctx.
func Warn(ctx context.Context, msg string, args ...any) {
	slog.Default().WarnContext(ctx, msg, args...)
}

// Bg returns slog.Default for ctx-less startup/init/shutdown sites.
// Using Bg() in any handler-path code is a bug — it bypasses trace
// attribution silently.
func Bg() *slog.Logger {
	return slog.Default()
}

// With binds attrs to a child logger that still routes through
// ContextHandler, so InfoContext etc. still pick up trace_id from ctx.
func With(args ...any) *slog.Logger {
	return slog.Default().With(args...)
}
