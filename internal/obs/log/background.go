package log

import (
	"context"
)

// StartBackgroundTrace returns a child ctx carrying a fresh Carrier
// so any log under it picks up a stable trace_id. Use at the top of
// every non-HTTP logical-request entrypoint (sweeper tick, WS envelope
// handler, CLI command).
func StartBackgroundTrace(parent context.Context) (context.Context, Carrier) {
	if parent == nil {
		parent = context.Background()
	}
	c := NewCarrier()
	return WithTrace(parent, c), c
}
