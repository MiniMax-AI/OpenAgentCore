package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestFunctionResultWaitsForNativeCompletion(t *testing.T) {
	client, server, cleanup := NewTestClient()
	defer cleanup()
	session, output := newInteractionTestSession(client.JSONRPCClient)
	session.setThreadID("thread")
	session.startSteering("thread", "turn")
	var err error
	session.functions, err = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.handleFunctionCall(json.RawMessage(`{"threadId":"thread","turnId":"turn","callId":"call","tool":"lookup","arguments":{}}`), "rpc-call"); err != nil {
		t.Fatal(err)
	}
	<-output
	text := "caller-owned result"
	finished := make(chan error, 1)
	go func() {
		finished <- session.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{DeliveryID: "delivery", CallID: "call", Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &text}}})
	}()
	var response JsonRpcResponse
	if err := json.NewDecoder(server.FromClient).Decode(&response); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		t.Fatalf("submission settled before any native receipt: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	session.onItemCompleted(json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"dynamicToolCall","id":"call","tool":"lookup","arguments":{},"status":"completed","success":true,"contentItems":[{"type":"inputText","text":"caller-owned result"}]}}`))
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("matching native completion did not settle submission")
	}
}

func pendingReceipt(t *testing.T) (*Session, ServerSide, <-chan error, proto.FunctionResultPayload) {
	return pendingReceiptContext(t, t.Context())
}

func pendingReceiptContext(t *testing.T, ctx context.Context) (*Session, ServerSide, <-chan error, proto.FunctionResultPayload) {
	t.Helper()
	client, server, cleanup := NewTestClient()
	t.Cleanup(cleanup)
	s, out := newInteractionTestSession(client.JSONRPCClient)
	s.cfg.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	s.setThreadID("thread")
	s.startSteering("thread", "turn")
	s.functions, _ = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}})
	if _, err := s.handleFunctionCall(json.RawMessage(`{"threadId":"thread","turnId":"turn","callId":"call","tool":"lookup","arguments":{}}`), "rpc-call"); err != nil {
		t.Fatal(err)
	}
	<-out
	value := "unpredictable caller result"
	result := proto.FunctionResultPayload{CallID: "call", DeliveryID: "delivery", Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &value}}}
	done := make(chan error, 1)
	go func() { done <- s.SubmitFunctionResult(ctx, result) }()
	var frame JsonRpcResponse
	if err := json.NewDecoder(server.FromClient).Decode(&frame); err != nil {
		t.Fatal(err)
	}
	return s, server, done, result
}

func nativeFunctionReceipt(thread, turn, call, name, success, status, content string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"threadId":%q,"turnId":%q,"item":{"type":"dynamicToolCall","id":%q,"tool":%q,"status":%q,"success":%s,"contentItems":%s}}`, thread, turn, call, name, status, success, content))
}

func TestFunctionReceiptIdentityAndContent(t *testing.T) {
	validContent := `[{"type":"inputText","text":"unpredictable caller result"}]`
	for _, tc := range []struct {
		name, thread, turn, call, tool, success, status, content string
		ignore                                                   bool
	}{
		{"foreign thread", "other", "turn", "call", "lookup", "true", "completed", validContent, true},
		{"foreign turn", "thread", "other", "call", "lookup", "true", "completed", validContent, true},
		{"foreign call", "thread", "turn", "other", "lookup", "true", "completed", validContent, true},
		{"different tool", "thread", "turn", "call", "other", "true", "completed", validContent, false},
		{"missing success", "thread", "turn", "call", "lookup", "null", "completed", validContent, false},
		{"changed success", "thread", "turn", "call", "lookup", "false", "failed", validContent, false},
		{"unfinished", "thread", "turn", "call", "lookup", "true", "inProgress", validContent, false},
		{"missing content", "thread", "turn", "call", "lookup", "true", "completed", "null", false},
		{"changed content", "thread", "turn", "call", "lookup", "true", "completed", `[{"type":"inputText","text":"different"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, done, _ := pendingReceipt(t)
			s.confirmFunctionResult(nativeFunctionReceipt(tc.thread, tc.turn, tc.call, tc.tool, tc.success, tc.status, tc.content))
			if tc.ignore {
				select {
				case err := <-done:
					t.Fatalf("foreign receipt settled submission: %v", err)
				case <-time.After(10 * time.Millisecond):
				}
				s.confirmFunctionResult(nativeFunctionReceipt("thread", "turn", "call", "lookup", "true", "completed", validContent))
			}
			select {
			case err := <-done:
				if (err == nil) != tc.ignore {
					t.Fatalf("unexpected receipt result: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("receipt did not settle")
			}
		})
	}
}

func TestFunctionReceiptResultIsFrozenAndSubmittedOnce(t *testing.T) {
	s, _, done, result := pendingReceipt(t)
	*result.Content[0].Text = "caller changed it"
	if err := s.SubmitFunctionResult(t.Context(), result); err == nil {
		t.Fatal("concurrent duplicate admitted")
	}
	s.confirmFunctionResult(nativeFunctionReceipt("thread", "turn", "call", "lookup", "true", "completed", `[{"type":"inputText","text":"unpredictable caller result"}]`))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.SubmitFunctionResult(t.Context(), result); err == nil {
		t.Fatal("settled result admitted again")
	}
	s.stopFunctionCalls()
}

func TestFunctionReceiptShutdownDoesNotConfirmApplication(t *testing.T) {
	for _, cause := range []string{"native exit", "run cancellation", "terminal"} {
		t.Run(cause, func(t *testing.T) {
			s, _, done, _ := pendingReceipt(t)
			switch cause {
			case "native exit":
				// The pipe fixture has no child Wait goroutine to publish process exit.
				close(s.rpc.doneCh)
			case "run cancellation":
				s.cancelFn()
			case "terminal":
				s.stopFunctionCalls()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unconfirmed result reported applied")
				}
			case <-time.After(time.Second):
				t.Fatal("shutdown did not settle receipt")
			}
			s.stopFunctionCalls()
		})
	}
}

func TestFunctionReceiptDeadlineEndsUncertainExecution(t *testing.T) {
	client, server, cleanup := NewTestClient()
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
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.SubmitFunctionResult(ctx, proto.FunctionResultPayload{CallID: "call", Success: true, Content: []proto.InputContent{}})
	}()
	var frame JsonRpcResponse
	if err := json.NewDecoder(server.FromClient).Decode(&frame); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing receipt reported applied")
		}
	case <-time.After(time.Second):
		t.Fatal("deadline did not settle")
	}
	if s.cancelCtx.Err() == nil {
		t.Fatal("uncertain native execution remains active")
	}
	s.stopFunctionCalls()
}

// The receipt and deadline are both ready when the waiter resumes. Either select
// branch must preserve the native confirmation and its continuing execution.
func TestFunctionReceiptWinsSimultaneousDeadline(t *testing.T) {
	for range 100 {
		client, _, cleanup := NewTestClient()
		s, _ := newInteractionTestSession(client.JSONRPCClient)
		s.setThreadID("thread")
		s.startSteering("thread", "turn")
		s.functions, _ = prepareFunctionTools(nil)
		pending := &pendingFunction{turnID: "turn", name: "lookup", reply: &functionReply{Success: true, ContentItems: []functionContent{}}, receipt: make(chan error, 1)}
		s.functions.pending["call"] = pending
		s.confirmFunctionResult(nativeFunctionReceipt("thread", "turn", "call", "lookup", "true", "completed", "[]"))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := s.waitFunctionResult(ctx, "call", pending, nil)
		cancelled := s.cancelCtx.Err()
		cleanup()
		if err != nil || cancelled != nil {
			t.Fatalf("confirmed result lost to deadline: result=%v execution=%v", err, cancelled)
		}
	}
}

func TestFunctionReceiptSurvivesObservationBackpressure(t *testing.T) {
	for _, cause := range []string{"deadline", "cancel", "terminal"} {
		t.Run(cause, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s, _, receipt, _ := pendingReceiptContext(t, ctx)
			out := make(chan proto.Envelope, 1)
			s.out = out
			out <- proto.Envelope{Type: "occupied"}
			published := make(chan struct{})
			go func() {
				defer close(published)
				s.onItemCompleted(nativeFunctionReceipt("thread", "turn", "call", "lookup", "true", "completed", `[{"type":"inputText","text":"unpredictable caller result"}]`))
			}()
			// A held output lock proves the native event passed identity checks and is
			// now blocked publishing into the deliberately full observation channel.
			deadline := time.Now().Add(time.Second)
			for s.outMu.TryLock() {
				s.outMu.Unlock()
				if time.Now().After(deadline) {
					t.Fatal("completion did not reach blocked publication")
				}
				runtime.Gosched()
			}
			switch cause {
			case "deadline":
				cancel()
			case "cancel":
				s.cancelled.Store(true)
				s.stopFunctionCalls()
				s.cancelFn()
			case "terminal":
				s.terminal.Store(true)
				s.stopFunctionCalls()
			}
			select {
			case err := <-receipt:
				if err != nil {
					t.Fatalf("known native result lost during %s: %v", cause, err)
				}
			case <-time.After(time.Second):
				t.Fatal("receipt waited for observation publication")
			}
			<-out
			select {
			case <-published:
			case <-time.After(time.Second):
				t.Fatal("observation publisher did not settle")
			}
		})
	}
}
