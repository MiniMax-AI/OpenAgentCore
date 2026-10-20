package codex

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// This opt-in test runs the real pinned harness with a model that spawns a
// Subagent, which calls a function or an MCP tool of the Agent.
func TestSubagentToolsNative(t *testing.T) {
	binary, options := nativeAcceptanceConfig(t)
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &rpc)
		var result string
		switch rpc.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"proof","version":"0"}}`
		case "tools/list":
			result = `{"tools":[{"name":"echo","description":"Return the synthetic MCP value.","inputSchema":{"type":"object","properties":{}}}]}`
		case "tools/call":
			result = `{"content":[{"type":"text","text":"MCP-5521"}]}`
		default:
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(rpc.ID) + `,"result":` + result + `}`))
	}))
	defer mcpServer.Close()
	for name, prompt := range map[string]string{
		"function": "Call spawn_agent with fork_context true and the task: call the lookup function once and report the value it returns. Then wait for that Subagent and reply with its value. Do not call lookup yourself.",
		"mcp":      "Call spawn_agent with a task: call the echo tool of the proof MCP server once and report the value it returns. Then wait for that Subagent and reply with its value. Do not call echo yourself.",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := defaultSessionConfig(testStateRoot(t))
			cfg.codexBinary = binary
			cfg.logger = slog.New(slog.DiscardHandler)
			servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "proof", ServerURL: mcpServer.URL}}
			req := proto.PromptRequestPayload{
				AgentKind: "codex", AgentStateKey: "subagent-tools-" + name,
				DisableExecutionEnvironment: true, ObserveSubagentIdentities: true,
				AgentOptions:   options,
				FunctionTools:  []proto.FunctionTool{{Name: "lookup", Description: "Return the synthetic verification value.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}},
				MCPHTTPServers: &servers,
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			e, err := newExecutor(ctx, req, cfg)
			if err != nil {
				t.Fatalf("native prepare failed (%T)", err)
			}
			defer e.Close(context.Background())
			output := make(chan proto.Envelope, 512)
			turn, err := e.StartTurn(ctx, name, proto.TextInput(prompt), output)
			if err != nil {
				t.Fatal(err)
			}
			var calls []proto.FunctionCallPayload
			var childTools []string
			var answer string
			var answers sync.WaitGroup
			for event := range output {
				switch event.Type {
				case proto.TypeFunctionCall:
					var call proto.FunctionCallPayload
					_ = event.DecodePayload(&call)
					calls = append(calls, call)
					text := "FN-4729"
					answers.Add(1)
					go func() {
						defer answers.Done()
						if err := turn.(agent.FunctionResultSubmitter).SubmitFunctionResult(ctx, proto.FunctionResultPayload{CallID: call.CallID, Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &text}}}); err != nil {
							t.Errorf("function answer: %v", err)
						}
					}()
				case proto.TypeSubagentItem:
					var item proto.SubagentItemPayload
					_ = event.DecodePayload(&item)
					var tool struct {
						Stage       string `json:"stage"`
						Observation struct {
							Kind, Name, Server string
						} `json:"observation"`
					}
					if item.Kind == proto.TypeToolCall && json.Unmarshal(item.Payload, &tool) == nil && tool.Stage == "after" {
						childTools = append(childTools, tool.Observation.Kind+":"+tool.Observation.Server+tool.Observation.Name)
					}
				case proto.TypeDone:
					var done proto.DonePayload
					_ = event.DecodePayload(&done)
					answer = done.Content
				}
			}
			answers.Wait()
			if _, err := turn.AwaitSettlement(ctx); err != nil {
				t.Fatal(err)
			}
			t.Logf("function_calls=%+v child_tools=%v answer=%q", calls, childTools, answer)
			want := map[string]string{"function": "function:lookup", "mcp": "mcp:proofecho"}[name]
			seen := false
			for _, tool := range childTools {
				seen = seen || tool == want
			}
			if !seen {
				t.Fatalf("the Subagent did not run %s", want)
			}
			if name == "function" && (len(calls) != 1 || calls[0].NativeID == "" || calls[0].TurnID == "" || !strings.Contains(answer, "FN-4729")) {
				t.Fatal("the Subagent call did not reach the client with its child identity")
			}
			if name == "mcp" && (len(calls) != 0 || !strings.Contains(answer, "MCP-5521")) {
				t.Fatal("the MCP value did not reach the root")
			}
		})
	}
}
