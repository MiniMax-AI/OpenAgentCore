//go:build linux

package claudesdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// TestLiveClaudeSDKNativeCombinations proves structured output beside multi_agent,
// a function tool and deferred tool search on the real SDK and provider.
func TestLiveClaudeSDKNativeCombinations(t *testing.T) {
	entrypoint, keyFile, proofRoot := os.Getenv("OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT"), os.Getenv("OAC_TEST_CLAUDE_SDK_MINIMAX_KEY_FILE"), os.Getenv("OAC_TEST_CLAUDE_SDK_PROOF_DIR")
	endpoint, model := os.Getenv("OAC_TEST_CLAUDE_SDK_BASE_URL"), os.Getenv("OAC_TEST_CLAUDE_SDK_MODEL")
	if entrypoint == "" || keyFile == "" || endpoint == "" || model == "" || !filepath.IsAbs(proofRoot) {
		t.Skip("real SDK/provider acceptance requires an entrypoint, a private key file, an endpoint, a model and an absolute proof directory")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(proofRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(proofRoot, "claude-combination-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: entrypoint, StateDir: filepath.Join(root, "state"), Env: []string{
		"ANTHROPIC_BASE_URL=" + endpoint, "ANTHROPIC_AUTH_TOKEN=" + strings.TrimSpace(string(key)),
		"ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN=",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=" + model, "ANTHROPIC_DEFAULT_OPUS_MODEL=" + model, "ANTHROPIC_DEFAULT_HAIKU_MODEL=" + model,
	}}
	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
	lookup := func(deferred bool) []proto.FunctionTool {
		return []proto.FunctionTool{{Name: "lookup", Description: "Return the synthetic verification value.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), DeferLoading: deferred}}
	}
	// A stateless JSON-RPC MCP server with one tool.
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
			result = `{"content":[{"type":"text","text":"synthetic-mcp-value"}]}`
		default:
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(rpc.ID) + `,"result":` + result + `}`))
	}))
	defer mcpServer.Close()
	mcp := func(req *proto.PromptRequestPayload) {
		req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "proof", ServerURL: mcpServer.URL}}
	}
	cases := map[string]func(*proto.PromptRequestPayload){
		"multi_agent": func(req *proto.PromptRequestPayload) {
			req.DisableSubagents, req.ObserveSubagentIdentities = false, true
		},
		"mcp": mcp,
		"mcp_tool_search": func(req *proto.PromptRequestPayload) {
			mcp(req)
			req.FunctionTools, req.ToolSearch = lookup(true), true
		},
		"function": func(req *proto.PromptRequestPayload) { req.FunctionTools = lookup(false) },
		"tool_search": func(req *proto.PromptRequestPayload) {
			req.FunctionTools, req.ToolSearch = lookup(true), true
		},
	}
	for name, configure := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			req := proto.PromptRequestPayload{RunID: uuid.NewString(), DisableExecutionEnvironment: true, DisableSubagents: true,
				Input:             proto.TextInput("Call the lookup tool if one is available and put its value in answer. Otherwise answer with the word ready."),
				ExecutionControls: &proto.ExecutionControls{TextVerbosity: "medium", OutputFormat: &proto.OutputFormat{Type: "json_schema", Schema: schema}},
				AgentOptions:      map[string]any{"model": model, "system_prompt": "Reply only through the structured output."}}
			configure(&req)
			out := make(chan proto.Envelope, 64)
			running, err := startSingleTurn(ctx, config, req, out)
			if err != nil {
				t.Fatal(err)
			}
			defer running.Cancel(context.Background())
			s := running.(*session)
			var done, called bool
			var content, failure string
			for event := range out {
				switch event.Type {
				case proto.TypeFunctionCall:
					var call proto.FunctionCallPayload
					if err := event.DecodePayload(&call); err != nil {
						t.Fatal(err)
					}
					called = true
					text := "synthetic-verification-value"
					if err := s.SubmitFunctionResult(ctx, proto.FunctionResultPayload{CallID: call.CallID, DeliveryID: uuid.NewString(), Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &text}}}); err != nil {
						t.Fatal(err)
					}
				case proto.TypeError:
					var payload proto.ErrorPayload
					_ = json.Unmarshal(event.Payload, &payload)
					failure = payload.Error
				case proto.TypeDone:
					var payload proto.DonePayload
					_ = json.Unmarshal(event.Payload, &payload)
					done, content = true, payload.Content
				}
			}
			var answer struct {
				Answer string `json:"answer"`
			}
			if !done || failure != "" || json.Unmarshal([]byte(content), &answer) != nil || answer.Answer == "" {
				t.Fatalf("structured output failed: done=%v failure=%q content=%q", done, failure, content)
			}
			t.Logf("%s: function_called=%v answer=%q", name, called, answer.Answer)
		})
	}
}
