package codex

import (
	"context"
	"errors"
	"time"
)

// The existing Run owner interrupts child work while its native reader is still
// alive. A timeout is an uncertain cancellation, not a fabricated child terminal.
func (s *Session) cancelSubagentWork(ctx context.Context) error {
	o := s.subagents
	select {
	case <-o.done:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.cancelResult
	default:
	}
	o.cancelling.Store(true)
	select {
	case o.wake <- struct{}{}:
	default:
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case <-o.done:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.cancelResult
	case <-deadline.Done():
		return errors.New("codex: child cancellation could not be confirmed before native shutdown")
	}
}
