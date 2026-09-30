package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func toolSnapshotFixtures() []string {
	return []string{
		`{"type":"commandExecution","id":"cmd","command":"printf result","cwd":"/tmp","status":"completed","aggregatedOutput":"result\n","exitCode":0,"durationMs":37}`,
		`{"type":"commandExecution","id":"fail","command":"exit 2","status":"failed","aggregatedOutput":"failure details","exitCode":2,"durationMs":12}`,
		`{"type":"mcpToolCall","id":"mcp","server":"reference","tool":"lookup","arguments":{"id":"record"},"status":"completed","result":{"content":[{"type":"text","text":"answer"}],"structuredContent":{"version":9007199254740993}},"error":null}`,
		`{"type":"mcpToolCall","id":"mcp-error","server":"reference","tool":"lookup","arguments":{},"status":"failed","result":null,"error":{"message":"lookup failed"}}`,
		`{"type":"dynamicToolCall","id":"dynamic","tool":"lookup","namespace":"reference","arguments":["a","b"],"status":"completed","contentItems":[{"type":"inputText","text":"dynamic output"}],"success":true}`,
		`{"type":"fileChange","id":"edit","status":"completed","changes":[{"path":"/tmp/example","kind":{"type":"update","move_path":null},"diff":"-before\n+after"}]}`,
		`{"type":"webSearch","id":"search","query":"reference","action":{"type":"search","query":"reference","queries":["reference"]}}`,
	}
}

func TestToolObservationsOnlyWhenRequested(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, item := range toolSnapshotFixtures() {
			var source struct{ ID string }
			if err := json.Unmarshal([]byte(item), &source); err != nil {
				t.Fatal(err)
			}
			t.Run(source.ID, func(t *testing.T) {
				out := make(chan proto.Envelope, 4)
				s := &Session{runID: "run", observeToolObservations: enabled, out: out, cancelCtx: context.Background(), bufs: NewItemBuffers(), cfg: defaultSessionConfig()}
				s.setThreadID("private-thread")
				s.onTurnStarted(json.RawMessage(`{"threadId":"private-thread","turn":{"id":"private-turn"}}`))
				raw := json.RawMessage(`{"threadId":"private-thread","turnId":"private-turn","item":` + item + `}`)
				s.onItemStarted(raw)
				s.onItemCompleted(raw)
				if len(out) != 2 {
					t.Fatalf("tool event count changed: %d", len(out))
				}
				for _, stage := range []string{"before", "after"} {
					event := <-out
					var tool proto.ToolCallPayload
					if event.DecodePayload(&tool) != nil || event.Type != proto.TypeToolCall || tool.ID != source.ID || tool.Stage != stage {
						t.Fatal(event)
					}
					if bytes.Contains(event.Payload, []byte("native_item")) {
						t.Fatal("engine-specific snapshot escaped adapter")
					}
					if !enabled {
						if tool.Observation != nil {
							t.Fatal("unrequested observation")
						}
						continue
					}
					if tool.Observation == nil || stage == "before" && tool.Observation.Status != "in_progress" {
						t.Fatal(tool.Observation)
					}
					if source.ID == "mcp" && !bytes.Contains(tool.Observation.Output, []byte("9007199254740993")) {
						t.Fatal("structured output precision lost")
					}
				}
			})
		}
	}
}
