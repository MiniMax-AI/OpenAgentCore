package mcode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func observationSession() (*Session, chan proto.Envelope) {
	out := make(chan proto.Envelope, 8)
	return &Session{ctx: context.Background(), outputContext: context.Background(), req: proto.PromptRequestPayload{RunID: "run"}, opts: launchOptions{Dir: "/environment/workspace"},
		out: out, tools: map[string]toolUpdate{}, completedTools: map[string]bool{}}, out
}

func TestNativeBashBecomesCommandObservation(t *testing.T) {
	for _, test := range []struct {
		status string
		output map[string]any
		exit   *int64
		text   string
	}{
		{"completed", map[string]any{"content": []any{map[string]any{"type": "text", "text": "/environment/workspace"}},
			"details": map[string]any{"processOutput": map[string]any{"exitCode": json.Number("0")}}}, ptr(0), "/environment/workspace"},
		// A failed command reports only prose, which is not parsed for an exit code.
		{"failed", map[string]any{"content": []any{map[string]any{"type": "text", "text": "Command exited with code 3"}}}, nil, "Command exited with code 3"},
	} {
		t.Run(test.status, func(t *testing.T) {
			s, out := observationSession()
			_ = s.emitTool(toolUpdate{ID: "call", Name: "bash"})
			if len(out) != 0 {
				t.Fatal("command item emitted before native arguments")
			}
			_ = s.emitTool(toolUpdate{ID: "call", RawInput: map[string]any{"command": "pwd"}})
			_ = s.emitTool(toolUpdate{ID: "call", Status: test.status, RawOutput: test.output})
			if len(out) != 2 {
				t.Fatalf("events=%d", len(out))
			}
			var before, after proto.ToolCallPayload
			_ = json.Unmarshal((<-out).Payload, &before)
			_ = json.Unmarshal((<-out).Payload, &after)
			if before.Observation == nil || before.Observation.Kind != "command" || before.Observation.Status != "in_progress" {
				t.Fatal(before)
			}
			n := after.Observation
			output, _ := json.Marshal(test.text)
			if n == nil || n.Command != "pwd" || n.Cwd == nil || *n.Cwd != "/environment/workspace" || n.Status != test.status || string(n.Output) != string(output) {
				t.Fatal(after)
			}
			if (n.ExitCode == nil) != (test.exit == nil) || (n.ExitCode != nil && *n.ExitCode != *test.exit) || n.DurationMS != nil {
				t.Fatal("exit code", n.ExitCode)
			}
		})
	}
}

func TestNativeWebFetchBecomesOpenPageObservation(t *testing.T) {
	s, out := observationSession()
	_ = s.emitTool(toolUpdate{ID: "call", Name: "web_fetch", RawInput: map[string]any{"url": "https://example.test/doc"}})
	_ = s.emitTool(toolUpdate{ID: "call", Status: "failed", RawOutput: map[string]any{}})
	var calls [2]proto.ToolCallPayload
	for i := range calls {
		_ = json.Unmarshal((<-out).Payload, &calls[i])
	}
	for i, status := range []string{"in_progress", "failed"} {
		n := calls[i].Observation
		if n == nil || n.Kind != "web_search" || n.Status != status || n.Action == nil || n.Action.Type != "open_page" || n.Action.URL == nil || *n.Action.URL != "https://example.test/doc" {
			t.Fatal(calls[i])
		}
	}
}

func TestOtherNativeToolsAreNotInventedPublicItems(t *testing.T) {
	s, _ := observationSession()
	for _, name := range []string{"read", "write", "edit", "grep", "glob", "todowrite", "skill", "task_query", "task_output", "task_stop"} {
		if s.nativeToolObservation(toolUpdate{Name: name, RawInput: map[string]any{"command": "x", "url": "x"}}, "before") != nil {
			t.Fatal(name)
		}
	}
}

func ptr(value int64) *int64 { return &value }
