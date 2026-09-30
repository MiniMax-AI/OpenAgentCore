package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestSubagentCancelDeadlineRetainsOwnerAndRetry(t *testing.T) {
	s, f, out := observationSession(t, "inProgress")
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	defer release()
	f.mu.Lock()
	f.interruptGate = gate
	f.mu.Unlock()
	s.emitDoneAt("frozen", nil, nil)
	for range 4 {
		select {
		case e := <-out:
			if e.Type == proto.TypeDone {
				t.Fatal("root released active child")
			}
		case <-time.After(time.Second):
			t.Fatal("child facts missing")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := s.Cancel(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first cancellation did not honor caller deadline: %v", err)
	}
	if !s.rpc.Alive() || s.cancelCtx.Err() != nil {
		t.Fatal("caller deadline killed the native observation owner")
	}
	select {
	case e := <-out:
		t.Fatalf("invented terminal before native receipt: %s", e.Type)
	default:
	}
	result := make(chan error, 4)
	for range cap(result) {
		go func() { result <- s.Cancel(t.Context()) }()
	}
	release()
	for range cap(result) {
		select {
		case err := <-result:
			if err != nil {
				t.Fatal("settled cancellation retry failed", err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("cancellation retry did not settle")
		}
	}
	events := collectObserved(t, out)
	childTerminal, rootTerminal := -1, -1
	for i, event := range events {
		if event.Type == proto.TypeSubagentTurn {
			var turn proto.SubagentTurnPayload
			_ = event.DecodePayload(&turn)
			if turn.Status == "cancelled" {
				childTerminal = i
			}
		}
		if event.Type == proto.TypeDone {
			rootTerminal = i
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.interrupted != 1 || childTerminal < 0 || rootTerminal <= childTerminal {
		t.Fatalf("interrupts=%d child=%d root=%d", f.interrupted, childTerminal, rootTerminal)
	}
}

func TestSubagentCancelRetainsActualObservationFailure(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid history", true: "observer stopped"}[interrupted], func(t *testing.T) {
			s, f, out := observationSession(t, "completed")
			if interrupted {
				s.subagents.cancel()
			} else {
				if err := os.WriteFile(filepath.Join(f.home, "sessions", "root.jsonl"), []byte("invalid\n"), 0600); err != nil {
					t.Fatal(err)
				}
				s.emitDoneAt("frozen", nil, nil)
			}
			select {
			case <-s.subagents.done:
			case <-time.After(5 * time.Second):
				t.Fatal("observer did not settle")
			}
			for range 2 {
				if err := s.Cancel(t.Context()); err == nil {
					t.Fatal("lost an actual observation failure")
				}
			}
			s.closeOut()
			for _, event := range collectObserved(t, out) {
				if event.Type == proto.TypeSubagentTurn {
					var turn proto.SubagentTurnPayload
					_ = json.Unmarshal(event.Payload, &turn)
					if turn.Status != "in_progress" {
						t.Fatal("invented child terminal after failed observation")
					}
				}
			}
		})
	}
}
