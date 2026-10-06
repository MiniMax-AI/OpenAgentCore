package execution

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// A reservation survives observer disconnects and owner restarts. Its opaque
// identity anchors the execution trace; the submitting HTTP trace is linked in
// the reservation log rather than retained in a process-local map.
func reservationTraceID(reservation string) obslog.TraceID {
	digest := sha256.Sum256([]byte("oac.environment-input:" + reservation))
	var id obslog.TraceID
	copy(id[:], digest[:len(id)])
	return id
}

func reservationTrace(ctx context.Context, reservation string) context.Context {
	return obslog.WithTrace(ctx, obslog.Carrier{Trace: reservationTraceID(reservation), Span: obslog.NewSpanID()})
}

// Stage intervals use a local monotonic clock. Error prose and request content
// are deliberately excluded: provider errors may contain private configuration.
func observeExecutionStage(ctx context.Context, stage string, started time.Time, err error, attrs ...any) {
	status := "ok"
	switch {
	case errors.Is(err, context.Canceled):
		status = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		status = "timeout"
	case err != nil:
		status = "error"
	}
	fields := []any{"stage", stage, "duration_ms", float64(time.Since(started).Microseconds()) / 1000, "status", status}
	obslog.Info(ctx, "execution stage", append(fields, attrs...)...)
}

// Initial input is committed by Session creation. Joining its origin to the
// worker's is_initial reservation avoids an additional diagnostic store query.
func recordInitialInputOrigin(ctx context.Context, session string) {
	obslog.Info(ctx, "session initial input origin", "session_id", session)
}
