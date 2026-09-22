package codex

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type confirmedResultWriter struct {
	s        *Session
	cancel   context.CancelFunc
	closed   chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (w *confirmedResultWriter) Write(p []byte) (int, error) {
	defer close(w.finished)
	// The peer has consumed the full frame and its completion is processed before
	// the writer goroutine gets to publish write completion.
	w.s.confirmFunctionResult(nativeFunctionReceipt("thread", "turn", "call", "lookup", "true", "completed", "[]"))
	w.cancel()
	<-w.closed
	return len(p), nil
}
func (w *confirmedResultWriter) Close() error { w.once.Do(func() { close(w.closed) }); return nil }
func TestFunctionConfirmedReceiptDuringWriteDeadline(t *testing.T) {
	client, _, cleanup := NewTestClient()
	defer cleanup()
	s, out := newInteractionTestSession(client.JSONRPCClient)
	s.setThreadID("thread")
	s.startSteering("thread", "turn")
	s.functions, _ = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{}`)}})
	if _, err := s.handleFunctionCall(json.RawMessage(`{"threadId":"thread","turnId":"turn","callId":"call","tool":"lookup","arguments":{}}`), "rpc-call"); err != nil {
		t.Fatal(err)
	}
	<-out
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &confirmedResultWriter{s: s, cancel: cancel, closed: make(chan struct{}), finished: make(chan struct{})}
	defer func() {
		_ = w.Close()
		select {
		case <-w.finished:
		case <-time.After(time.Second):
			t.Error("confirmed writer did not finish after release")
		}
	}()
	client.mu.Lock()
	client.stdin = w
	client.mu.Unlock()
	if err := s.SubmitFunctionResult(ctx, proto.FunctionResultPayload{CallID: "call", Success: true, Content: []proto.InputContent{}}); err != nil {
		t.Fatalf("known receipt lost: %v", err)
	}
	if !client.Alive() {
		t.Fatal("confirmed native result returned success but write deadline closed continuing execution")
	}
}

func TestFunctionUnconfirmedWriteDeadlineClosesNative(t *testing.T) {
	client, _, cleanup := NewTestClient()
	defer cleanup()
	s, out := newInteractionTestSession(client.JSONRPCClient)
	s.setThreadID("thread")
	s.startSteering("thread", "turn")
	s.functions, _ = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{}`)}})
	_, err := s.handleFunctionCall(json.RawMessage(`{"threadId":"thread","turnId":"turn","callId":"call","tool":"lookup","arguments":{}}`), "rpc-call")
	if err != nil {
		t.Fatal(err)
	}
	<-out
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := s.SubmitFunctionResult(ctx, proto.FunctionResultPayload{CallID: "call", Success: true, Content: []proto.InputContent{}}); err == nil {
		t.Fatal("blocked write reported applied")
	}
	if client.Alive() || s.cancelCtx.Err() == nil {
		t.Fatal("unconfirmed blocked writer retained native execution")
	}
}
