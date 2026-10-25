package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestDispatchStartedItem_Bash(t *testing.T) {
	envs, err := DispatchStartedItem("run-1", ThreadItem{
		Type: "commandExecution", ID: "c1", Command: "ls /tmp", Cwd: "/tmp",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("len envs = %d, want 1", len(envs))
	}
	if envs[0].Type != proto.TypeToolCall {
		t.Fatalf("type = %s, want tool_call", envs[0].Type)
	}
	var p proto.ToolCallPayload
	if err := envs[0].DecodePayload(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "Bash" || p.Stage != "before" {
		t.Fatalf("payload = %+v", p)
	}
	if cmd, _ := p.Args["command"].(string); cmd != "ls /tmp" {
		t.Fatalf("args.command = %v", p.Args["command"])
	}
}

func TestDispatchStartedItem_McpToolCall(t *testing.T) {
	envs, err := DispatchStartedItem("run-1", ThreadItem{
		Type: "mcpToolCall", ID: "m1", Server: "docs", Tool: "search",
		Arguments: map[string]any{"q": "hello"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("len envs = %d", len(envs))
	}
	var p proto.ToolCallPayload
	if err := envs[0].DecodePayload(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "mcp__docs__search" {
		t.Fatalf("tool name = %q", p.Name)
	}
	if got, _ := p.Args["q"].(string); got != "hello" {
		t.Fatalf("args.q = %v", p.Args["q"])
	}
}

func TestDispatchStartedItem_UnknownSilent(t *testing.T) {
	envs, err := DispatchStartedItem("run-1", ThreadItem{Type: "userMessage", ID: "u1"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if envs != nil {
		t.Fatalf("unknown / silent type must produce no envelopes, got %d", len(envs))
	}
}

func TestReasoningIsReportedOnlyWhenRequested(t *testing.T) {
	for _, requested := range []bool{true, false} {
		out := make(chan proto.Envelope, 16)
		s := &Session{runID: "run", out: out, cancelCtx: context.Background(), cfg: defaultSessionConfig(testStateRoot(t)),
			rpc: NewJSONRPCClient(JSONRPCConfig{}), bufs: NewItemBuffers(), reasoningSummary: requested}
		s.registerHandlers()
		s.setThreadID("root")
		scopeNotification(t, s, "turn/started", `{"threadId":"root","turn":{"id":"turn"}}`)
		scope := `{"threadId":"root","turnId":"turn",`
		scopeNotification(t, s, "item/reasoning/summaryTextDelta", scope+`"itemId":"r1","delta":"plan ","summaryIndex":0}`)
		scopeNotification(t, s, "item/reasoning/textDelta", scope+`"itemId":"r1","delta":"raw","contentIndex":0}`)
		scopeNotification(t, s, "item/reasoning/summaryTextDelta", scope+`"itemId":"r1","delta":"next","summaryIndex":1}`)
		scopeNotification(t, s, "item/completed", scope+`"item":{"type":"reasoning","id":"r1"}}`)
		scopeNotification(t, s, "item/completed", scope+`"item":{"type":"reasoning","id":"r2","content":["whole"]}}`)
		close(out)
		var got []proto.ReasoningPayload
		for env := range out {
			if env.Type == proto.TypeReasoning {
				var p proto.ReasoningPayload
				_ = env.DecodePayload(&p)
				got = append(got, p)
			}
		}
		want := []proto.ReasoningPayload{
			{ID: "r1", Status: "in_progress", Delta: "plan "}, {ID: "r1", Status: "in_progress", Index: 1, Delta: "next"}, {ID: "r1", Status: "completed"},
			{ID: "r2", Status: "in_progress", Delta: "whole"}, {ID: "r2", Status: "completed"},
		}
		if !requested {
			want = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("requested=%v: reasoning %+v, want %+v", requested, got, want)
		}
	}
}

func TestDispatchCompletedItem_AgentMessage_BufferIsFinalText(t *testing.T) {
	bufs := NewItemBuffers()
	FoldDeltaIntoBuffer(bufs, "a1", "Hello ")
	FoldDeltaIntoBuffer(bufs, "a1", "world")

	envs, text, err := DispatchCompletedItem("run-1", ThreadItem{
		Type: "agentMessage", ID: "a1", Text: "fallback",
	}, bufs)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(envs) != 0 {
		t.Fatalf("agentMessage must not produce envelopes (final text emits via Done), got %+v", envs)
	}
	if text != "Hello world" {
		// The buffered concatenation must win over item.Text fallback
		// when deltas were observed.
		t.Fatalf("agent text = %q, want buffered concatenation", text)
	}
}

func TestDispatchCompletedItem_AgentMessage_FallbackToItemText(t *testing.T) {
	// No deltas — agent text comes from item.text directly.
	bufs := NewItemBuffers()
	_, text, err := DispatchCompletedItem("run-1", ThreadItem{
		Type: "agentMessage", ID: "a1", Text: "no-delta body",
	}, bufs)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if text != "no-delta body" {
		t.Fatalf("fallback path = %q", text)
	}
}

func TestDispatchCompletedItem_ToolCallEmitsAfterStage(t *testing.T) {
	exit := 0
	envs, _, err := DispatchCompletedItem("run-1", ThreadItem{
		Type: "commandExecution", ID: "c1", Status: "completed", ExitCode: &exit,
	}, NewItemBuffers())
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(envs) != 1 || envs[0].Type != proto.TypeToolCall {
		t.Fatalf("envs = %+v", envs)
	}
	var p proto.ToolCallPayload
	_ = envs[0].DecodePayload(&p)
	if p.Stage != "after" {
		t.Fatalf("stage = %q, want after", p.Stage)
	}
	if status, _ := p.Result["status"].(string); status != "completed" {
		t.Fatalf("result.status = %v", p.Result["status"])
	}
}

func TestFoldDeltaIntoBuffer_AccumulatesPerItem(t *testing.T) {
	bufs := NewItemBuffers()
	if got := FoldDeltaIntoBuffer(bufs, "a1", "hi"); got != "hi" {
		t.Fatalf("fold returned %q", got)
	}
	if got := FoldDeltaIntoBuffer(bufs, "a1", " there"); got != "hi there" {
		t.Fatalf("fold accumulation = %q", got)
	}
	// Different itemId is a different accumulator.
	if got := FoldDeltaIntoBuffer(bufs, "a2", "other"); got != "other" {
		t.Fatalf("cross-item bleed: got %q", got)
	}
}

func TestArgMap_NonObjectValueWrapped(t *testing.T) {
	// dynamicToolCall.arguments arrives as any; arrays must not be
	// silently dropped.
	m := argMap([]any{"a", "b"})
	if _, ok := m["value"]; !ok {
		t.Fatalf("non-object args must surface under value: %+v", m)
	}
	raw, _ := json.Marshal(m["value"])
	if !strings.Contains(string(raw), `"a"`) || !strings.Contains(string(raw), `"b"`) {
		t.Fatalf("wrapped value lost array data: %s", raw)
	}
}
