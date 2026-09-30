package codex

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Close waits for the common owner to settle the native process and descendants.
func (c *JSONRPCClient) Close() error {
	defer func() { _ = c.drainPending(errors.New("codex rpc: client closed")) }()
	c.closeOnce.Do(func() {
		c.mu.Lock()
		process := c.process
		stdin := c.stdin
		c.alive = false
		c.mu.Unlock()
		if stdin != nil {
			_ = stdin.Close()
		}
		if process != nil {
			grace := time.NewTimer(250 * time.Millisecond)
			defer grace.Stop()
			select {
			case <-c.doneCh:
			case <-grace.C:
				process.Cancel()
			}
		}
	})
	c.mu.Lock()
	process := c.process
	c.mu.Unlock()
	if process == nil {
		return nil
	}
	wait := time.NewTimer(rpcKillTimeout)
	defer wait.Stop()
	select {
	case <-c.doneCh:
		return nil
	case <-wait.C:
		select {
		case <-c.doneCh:
			return nil
		default:
			c.cfg.Logger.Warn("codex rpc child did not exit after kill", "tag", c.cfg.LogTag)
			return fmt.Errorf("codex rpc: waiting for child exit: %w", context.DeadlineExceeded)
		}
	}
}
