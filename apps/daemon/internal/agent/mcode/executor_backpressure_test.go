package mcode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestExecutorBlockedPromptWriteCanBeCancelledOrClosed(t *testing.T) {
	for _, operation := range []string{"cancel", "close"} {
		t.Run(operation, func(t *testing.T) {
			e, _ := executorFixture(t, "executor-backpressure", true)
			out := make(chan proto.Envelope, 32)
			turn, err := e.StartTurn(t.Context(), "blocked", proto.TextInput(strings.Repeat("x", 4*1024*1024)), out)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for e.connection.writeMu.TryLock() {
				e.connection.writeMu.Unlock()
				if time.Now().After(deadline) {
					t.Fatal("prompt did not reach native pipe")
				}
				time.Sleep(time.Millisecond)
			}
			// The native fixture acknowledged readiness but does not read stdin.
			// A multi-megabyte prompt therefore fills the actual child process pipe.
			result := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
				defer cancel()
				if operation == "cancel" {
					result <- turn.Cancel(ctx)
				} else {
					result <- e.Close(ctx)
				}
			}()
			select {
			case err := <-result:
				if operation == "cancel" && err == nil {
					t.Fatal("unacknowledged cancellation was reported applied")
				}
			case <-time.After(3 * time.Second):
				// Emergency cleanup avoids leaking the intentionally blocked fixture.
				e.connection.process.Cancel()
				t.Fatal(operation + " could not interrupt a blocked native write")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if err := e.Close(ctx); err != nil {
				t.Fatal("Close did not retain cleanup ownership", err)
			}
			settlement, err := turn.AwaitSettlement(ctx)
			if err == nil || settlement.Reusable {
				t.Fatal("lost native prompt produced successful settlement", settlement, err)
			}
			for range out {
			}
		})
	}
}

func TestExecutorUnknownSteeringOutcomeCannotBecomeAppliedCancellation(t *testing.T) {
	e, _ := executorFixture(t, "executor-steer-unknown", true)
	out := make(chan proto.Envelope, 32)
	turn, err := e.StartTurn(t.Context(), "unknown-input", proto.TextInput("wait"), out)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	select {
	case <-out:
	case <-ctx.Done():
		t.Fatal("native prompt did not start")
	}
	session := turn.(*Session)
	if err := session.Steer(ctx, proto.PromptSteerPayload{InputID: "input-unknown", Input: proto.TextInput("continue")}); err == nil {
		t.Fatal("unknown native receipt was accepted")
	}
	if err := turn.Cancel(ctx); err == nil {
		t.Fatal("unknown input became applied cancellation")
	}
	settlement, err := turn.AwaitSettlement(ctx)
	if err == nil || settlement.Reusable {
		t.Fatal("unknown input lost its settlement error", settlement, err)
	}
	if err := turn.Cancel(ctx); err == nil {
		t.Fatal("repeated cancel lost original uncertainty")
	}
	if err := e.Close(ctx); err != nil {
		t.Fatal("settlement failure prevented resource cleanup", err)
	}
	for range out {
	}
}
