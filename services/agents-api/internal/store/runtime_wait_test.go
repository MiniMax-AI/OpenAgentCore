package store_test

import (
	"context"
	"testing"
	"time"
)

func awaitDaemonRemoteCondition(t *testing.T, ctx context.Context, timeout time.Duration, label string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(label, "context expired")
		case <-deadline.C:
			t.Fatal(label, "timed out")
		case <-tick.C:
		}
	}
}
