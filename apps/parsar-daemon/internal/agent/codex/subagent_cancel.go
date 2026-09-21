package codex

import (
	"context"
)

// The existing Run owner interrupts child work while its native reader is still
// alive. Callers bound their own wait without stopping this owner or fabricating
// a child terminal when the native cancellation takes longer than their deadline.
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
	select {
	case <-o.done:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.cancelResult
	case <-ctx.Done():
		return ctx.Err()
	}
}
