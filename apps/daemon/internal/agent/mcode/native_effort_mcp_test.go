package mcode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// This opt-in test makes real model calls. It sends the native model traffic
// through a local recording proxy and a text-placement HTTP MCP fixture, then
// checks the effort and the final user text at the model and the MCP call.
func TestNativeMCodeEffortMCPAndPlainText(t *testing.T) {
	binary, options := os.Getenv("OAC_RUNTIME_MCODE_BIN"), os.Getenv("OAC_TEST_MCODE_REAL_OPTIONS")
	if binary == "" || options == "" {
		t.Skip("native executable and private provider options required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	raw, err := os.ReadFile(options)
	if err != nil {
		t.Fatal("private provider options unavailable")
	}
	req := testRequest(t)
	req.RunID, req.Input = "", nil
	if json.Unmarshal(raw, &req.AgentOptions) != nil {
		t.Fatal("invalid private provider options")
	}
	provider := req.AgentOptions["model_provider"].(map[string]any)
	upstream, err := url.Parse(provider["base_url"].(string))
	if err != nil {
		t.Fatal("invalid provider base URL")
	}
	var mu sync.Mutex
	var bodies [][]byte
	proxy := httptest.NewServer(&httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			if body, err := io.ReadAll(r.In.Body); err == nil {
				mu.Lock()
				bodies = append(bodies, body)
				mu.Unlock()
				r.Out.Body = io.NopCloser(bytes.NewReader(body))
			}
		},
	})
	defer proxy.Close()
	provider["base_url"] = proxy.URL

	var called sync.Map
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		if len(rpc.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch rpc.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "lookup_code", "description": "Returns the secret access code. Always call it when asked for the access code.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			called.Store(rpc.Params.Name, true)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "ACCESS-CODE-4417"}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	}))
	defer mcp.Close()

	req.ExecutionControls.ReasoningEffort = "high"
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "fixture", ServerURL: mcp.URL + "/mcp"}}
	value, err := NewExecutorFactory(testStateRoot(t), nil)(ctx, req)
	if err != nil {
		t.Fatal("native Executor preparation failed")
	}
	e := value.(*executor)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		_ = e.Close(cleanup)
	}()
	out := make(chan proto.Envelope, 256)
	turn, err := e.StartTurn(ctx, "turn", proto.TextInput("Use the MCP tool to get the access code, then reply with only the code."), out)
	if err != nil {
		t.Fatal("native Turn admission failed")
	}
	var content string
	observed := false
	for event := range out {
		switch event.Type {
		case proto.TypeError:
			t.Fatal("native Turn reported an error")
		case proto.TypeToolCall:
			var call proto.ToolCallPayload
			if json.Unmarshal(event.Payload, &call) == nil && call.Observation != nil && call.Observation.Kind == "mcp" && call.Observation.Server == "fixture" && call.Observation.Name == "lookup_code" {
				observed = true
			}
		case proto.TypeDone:
			var done proto.DonePayload
			_ = json.Unmarshal(event.Payload, &done)
			content = done.Content
		}
	}
	if _, err := turn.AwaitSettlement(ctx); err != nil {
		t.Fatal("native Turn did not settle")
	}
	if _, ok := called.Load("lookup_code"); !ok || !observed || !strings.Contains(content, "ACCESS-CODE-4417") {
		t.Fatalf("MCP tool was not listed, called and observed: called=%v observed=%v", ok, observed)
	}
	mu.Lock()
	defer mu.Unlock()
	effort, plain := false, false
	for _, body := range bodies {
		var request struct {
			OutputConfig map[string]any `json:"output_config"`
			Messages     []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(body, &request) != nil {
			continue
		}
		effort = effort || request.OutputConfig["effort"] == "high"
		if len(request.Messages) > 0 && request.Messages[0].Role == "user" {
			if blocks, ok := request.Messages[0].Content.([]any); ok {
				for _, block := range blocks {
					if text, _ := block.(map[string]any)["text"].(string); strings.HasSuffix(text, "reply with only the code.") {
						plain = true
					}
				}
			} else if text, _ := request.Messages[0].Content.(string); strings.HasSuffix(text, "reply with only the code.") {
				plain = true
			}
		}
	}
	t.Logf("model requests=%d effort high on wire=%v user text ends without newlines=%v", len(bodies), effort, plain)
	if !effort || !plain {
		t.Fatal("model request did not carry the effort and the unmodified user text")
	}
}
