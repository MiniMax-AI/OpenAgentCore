package cli

import (
	"context"
	"time"

	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// A wait deadline does not revoke native ownership. Keep retrying the same
// Router until it confirms cleanup; reconnect and process exit both wait here.
// Router.Shutdown serializes attempts and retains resources after a failure.
func shutdownRouterUntilConfirmed(shutdown func(context.Context) error, retryDelay time.Duration) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := shutdown(ctx)
		cancel()
		if err == nil {
			return
		}
		obslog.Bg().Warn("router cleanup unconfirmed; reconnect remains blocked", "err", err)
		time.Sleep(retryDelay)
	}
}
