package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestExecutorCancellationRequiresNativeTerminalCleanup(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "confirmed"}[confirmed], func(t *testing.T) {
			e, root := executorFixture(t, "terminal-cleanup")
			allow := func() {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "frames.jsonl.allow-cleanup"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if confirmed {
				allow()
			}
			out := make(chan proto.Envelope, 20)
			turn, err := e.StartTurn(t.Context(), "cancel", proto.TextInput("hold"), out)
			if err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			if err := turn.Cancel(ctx); (err == nil) != confirmed {
				t.Fatalf("cancellation confirmed=%t: %v", confirmed, err)
			}
			settlement, err := turn.AwaitSettlement(ctx)
			if (err == nil) != confirmed || settlement.Reusable != confirmed {
				t.Fatal("cleanup changed cancellation confirmation", settlement, err)
			}
			for range out {
			}
			if !e.prepared.session.rpc.Alive() {
				t.Fatal("native owner lost before cleanup/reuse")
			}
			frames := preparationFrames(t, root)
			interrupted, terminated := false, false
			for _, frame := range frames {
				if frame.Method == "turn/interrupt" {
					interrupted = true
				}
				if frame.Method == "thread/backgroundTerminals/terminate" {
					if !interrupted {
						t.Fatal("cleanup preceded native interrupt")
					}
					terminated = true
				}
			}
			if !terminated {
				t.Fatal("native interrupted receipt skipped terminal cleanup")
			}
			if confirmed {
				nextOut := make(chan proto.Envelope, 20)
				next, err := e.StartTurn(ctx, "next", proto.TextInput("answer"), nextOut)
				if err != nil || !awaitExecutorTurn(t, next, nextOut).Reusable {
					t.Fatal("confirmed cleanup prevented reuse", err)
				}
			} else {
				if err := e.Close(ctx); err == nil || !e.prepared.session.rpc.Alive() {
					t.Fatal("failed cleanup released the owned native process", err)
				}
				allow()
				if err := e.Close(ctx); err != nil {
					t.Fatal("cleanup retry failed", err)
				}
				if err := turn.Cancel(ctx); err == nil {
					t.Fatal("resource cleanup fabricated native cancellation confirmation")
				}
			}
		})
	}
}

func TestExecutorCloseCleansNativeTerminalsAfterNormalCompletion(t *testing.T) {
	e, root := executorFixture(t, "terminal-cleanup")
	out := make(chan proto.Envelope, 20)
	turn, err := e.StartTurn(t.Context(), "normal", proto.TextInput("answer"), out)
	if err != nil {
		t.Fatal(err)
	}
	if !awaitExecutorTurn(t, turn, out).Reusable {
		t.Fatal("normal Turn did not retain its Executor")
	}
	for _, frame := range preparationFrames(t, root) {
		if frame.Method == "thread/backgroundTerminals/terminate" {
			t.Fatal("normal Turn prematurely killed its background terminal")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "frames.jsonl.allow-cleanup"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	terminated := false
	for _, frame := range preparationFrames(t, root) {
		if frame.Method == "thread/backgroundTerminals/terminate" {
			terminated = true
		}
	}
	if !terminated || e.prepared.session.rpc.Alive() {
		t.Fatal("Executor Close left native terminals or owner alive")
	}
}
