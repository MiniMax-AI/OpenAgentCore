package codex

import (
	"context"
	"errors"
	"time"
)

func (s *Session) Cancel(ctx context.Context) error {
	s.cancelled.Store(true)
	s.cancelOnce.Do(func() {
		s.cancelReady = make(chan struct{})
		go s.cancelNativeWork()
	})
	// A caller deadline does not terminate the owner that is still collecting
	// native child cancellation facts. Another call can await the same cleanup.
	select {
	case <-s.cancelReady:
	default:
		select {
		case <-s.cancelReady:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Close already owns shutdown once and permits another wait for process exit.
	// Keep actual observation failures, but never cache a caller's wait timeout.
	closed := make(chan error, 1)
	go func() { closed <- s.rpc.Close() }()
	select {
	case err := <-closed:
		return errors.Join(s.cancelErr, err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) cancelNativeWork() {
	defer close(s.cancelReady)
	s.stopFunctionCalls()
	turnID, active := s.stopSteering()
	s.stopCodexInteractionTimers()
	// Best effort: a known Turn must use its native identity. An explicit
	// empty ID invokes native startup cancellation before turn/started.
	if threadID := s.currentThreadID(); threadID != "" && active {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = s.rpc.request(ctx, "turn/interrupt", TurnInterruptParams{ThreadID: threadID, TurnID: turnID}, func(frame any) error {
			return s.rpc.writeFrameContext(ctx, frame)
		})
	}
	if s.subagents != nil {
		s.cancelErr = s.cancelSubagentWork(context.Background())
	}
	s.cancelFn()
	_ = s.rpc.Close()
}
