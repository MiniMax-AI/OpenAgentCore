package codex

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func newFunctionTestSession(rpc *JSONRPCClient) (*Session, <-chan proto.Envelope) {
	out := make(chan proto.Envelope, 1)
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{runID: "run-test", out: out, rpc: rpc, cancelCtx: ctx, cancelFn: cancel}
	s.registerHandlers()
	return s, out
}

func TestFunctionCallWaitsAndRepliesOnce(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[success], func(t *testing.T) {
			tc, srv, cleanup := NewTestClient()
			defer cleanup()
			s, out := newFunctionTestSession(tc.JSONRPCClient)
			var err error
			s.functions, err = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}})
			if err != nil {
				t.Fatal(err)
			}
			s.setThreadID("thread")
			s.startSteering("thread", "turn")
			params := map[string]any{"threadId": "thread", "turnId": "turn", "callId": "call", "tool": "lookup", "arguments": map[string]string{"ticket": "42"}}
			if err := SendServerRequest(srv, "rpc-call", "item/tool/call", params); err != nil {
				t.Fatal(err)
			}
			select {
			case env := <-out:
				var call proto.FunctionCallPayload
				if err := env.DecodePayload(&call); err != nil || env.Type != proto.TypeFunctionCall || env.ID != "run-test" || call.CallID != "call" || call.Name != "lookup" {
					t.Fatal(env, err)
				}
			case <-time.After(time.Second):
				t.Fatal("missing function call")
			}
			text, image, empty := "answer", "https://example.com/result.png", ""
			content := []proto.InputContent{{Type: "input_text", Text: &text}, {Type: "input_image", ImageURL: &image}, {Type: "input_text", Text: &empty}}
			if err := s.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{CallID: "call", Content: []proto.InputContent{{Type: "input_audio"}}}); err == nil {
				t.Fatal("invalid result consumed the pending call")
			}
			finished := make(chan error, 1)
			go func() {
				finished <- s.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{CallID: "call", Success: success, Content: content})
			}()
			var reply struct {
				ID     string          `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.NewDecoder(srv.FromClient).Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.ID != "rpc-call" {
				t.Fatal(reply.ID)
			}
			status := "completed"
			if !success {
				status = "failed"
			}
			var observed map[string]any
			if err := json.Unmarshal(reply.Result, &observed); err != nil {
				t.Fatal(err)
			}
			observed["type"], observed["id"], observed["tool"], observed["status"] = "dynamicToolCall", "call", "lookup", status
			native, _ := json.Marshal(map[string]any{"threadId": "thread", "turnId": "turn", "item": observed})
			s.onItemCompleted(native)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			var result struct {
				Success bool              `json:"success"`
				Content []functionContent `json:"contentItems"`
			}
			if err := json.Unmarshal(reply.Result, &result); err != nil || result.Success != success || !reflect.DeepEqual(result.Content, []functionContent{{Type: "inputText", Text: &text}, {Type: "inputImage", ImageURL: &image}, {Type: "inputText", Text: &empty}}) {
				t.Fatal(string(reply.Result), err)
			}
			if err := s.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{CallID: "call", Content: content}); !errors.Is(err, agent.ErrUnknownFunctionCall) {
				t.Fatal(err)
			}
		})
	}
}

func TestFunctionCallRejectsUnregisteredAndClosedRuns(t *testing.T) {
	tc, _, cleanup := NewTestClient()
	defer cleanup()
	s, _ := newFunctionTestSession(tc.JSONRPCClient)
	s.setThreadID("thread")
	s.startSteering("thread", "turn")
	s.functions, _ = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{}`)}})
	for _, params := range []string{
		`{"threadId":"other","turnId":"turn","callId":"a","tool":"lookup","arguments":{}}`,
		`{"threadId":"thread","turnId":"turn","callId":"a","tool":"unknown","arguments":{}}`,
		`{"threadId":"thread","turnId":"turn","callId":"a","tool":"lookup","namespace":"foreign","arguments":{}}`,
	} {
		if _, err := s.handleFunctionCall(json.RawMessage(params), "rpc"); err == nil {
			t.Fatal("unexpected call accepted", params)
		}
	}
	s.stopFunctionCalls(false)
	if _, err := s.handleFunctionCall(json.RawMessage(`{"threadId":"thread","turnId":"turn","callId":"a","tool":"lookup","arguments":{}}`), "rpc"); err == nil {
		t.Fatal("closed run accepted call")
	}
	if err := s.SubmitFunctionResult(context.Background(), proto.FunctionResultPayload{CallID: "a", Content: []proto.InputContent{}}); !errors.Is(err, agent.ErrUnknownFunctionCall) {
		t.Fatal(err)
	}
}

func TestSubagentFunctionCallWaitsForChildTurnAndOutlivesRoot(t *testing.T) {
	tc, srv, cleanup := NewTestClient()
	defer cleanup()
	s, out := newFunctionTestSession(tc.JSONRPCClient)
	s.subagents = &subagentObservations{wake: make(chan struct{}, 1), sent: map[string]string{}}
	s.functions, _ = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}})
	s.setThreadID("root")
	s.startSteering("root", "turn")
	if err := SendServerRequest(srv, "rpc-call", "item/tool/call", map[string]any{"threadId": "child", "turnId": "child-turn", "callId": "call", "tool": "lookup", "arguments": map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.subagents.wake:
	case <-time.After(time.Second):
		t.Fatal("child call did not wake the observer")
	}
	// The root Turn ends while the child still waits for its answer.
	s.terminal.Store(true)
	s.stopFunctionCalls(true)
	s.sendHeldFunctionCalls(t.Context())
	select {
	case env := <-out:
		t.Fatal("call sent before its child Turn fact", env)
	default:
	}
	s.subagents.sent[proto.TypeSubagentTurn+":child:child-turn"] = "{}"
	s.sendHeldFunctionCalls(t.Context())
	var call proto.FunctionCallPayload
	if env := <-out; env.DecodePayload(&call) != nil || call.CallID != "call" || call.NativeID != "child" || call.TurnID != "child-turn" {
		t.Fatal(env)
	}
	text := "answer"
	finished := make(chan error, 1)
	go func() {
		finished <- s.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{CallID: "call", Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &text}}})
	}()
	var reply struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(srv.FromClient).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	reply.Result["type"], reply.Result["id"], reply.Result["tool"], reply.Result["status"] = "dynamicToolCall", "call", "lookup", "completed"
	native, _ := json.Marshal(map[string]any{"threadId": "child", "turnId": "child-turn", "item": reply.Result})
	s.onItemCompleted(native)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestFunctionToolDefinitionsRejectAmbiguousInput(t *testing.T) {
	for _, tools := range [][]proto.FunctionTool{
		{{Name: "", Parameters: json.RawMessage(`{}`)}},
		{{Name: "lookup", Parameters: json.RawMessage(`[]`)}},
		{{Name: "lookup", Parameters: json.RawMessage(`null`)}},
		{{Name: "lookup", Parameters: json.RawMessage(`{}`)}, {Name: "lookup", Parameters: json.RawMessage(`{}`)}},
	} {
		if _, err := prepareFunctionTools(tools); err == nil {
			t.Fatal("invalid function accepted", tools)
		}
	}
}

func TestEndedChildTurnWithdrawsItsPendingCall(t *testing.T) {
	tc, srv, cleanup := NewTestClient()
	defer cleanup()
	s, out := newFunctionTestSession(tc.JSONRPCClient)
	s.subagents = &subagentObservations{wake: make(chan struct{}, 1), sent: map[string]string{proto.TypeSubagentTurn + ":child:child-turn": "{}"}}
	s.functions, _ = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}})
	s.setThreadID("root")
	s.startSteering("root", "turn")
	if err := SendServerRequest(srv, "rpc-call", "item/tool/call", map[string]any{"threadId": "child", "turnId": "child-turn", "callId": "call", "tool": "lookup", "arguments": map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	<-s.subagents.wake
	s.sendHeldFunctionCalls(t.Context())
	<-out
	if err := s.withdrawChildCalls(t.Context(), "child", "child-turn"); err != nil {
		t.Fatal(err)
	}
	var withdrawn proto.FunctionCallWithdrawnPayload
	if env := <-out; env.Type != proto.TypeFunctionCallWithdrawn || env.DecodePayload(&withdrawn) != nil || withdrawn != (proto.FunctionCallWithdrawnPayload{CallID: "call", NativeID: "child", TurnID: "child-turn"}) {
		t.Fatal(env)
	}
	if err := s.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{CallID: "call", Success: true, Content: []proto.InputContent{}}); !errors.Is(err, agent.ErrUnknownFunctionCall) {
		t.Fatal(err)
	}
	select {
	case <-s.rpc.Done():
		t.Fatal("a late result closed the native process")
	default:
	}
}
