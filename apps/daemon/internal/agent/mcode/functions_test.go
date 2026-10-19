package mcode

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type bearerTransport struct{ header string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", b.header)
	return http.DefaultTransport.RoundTrip(r)
}

// fakeFunctionTurn is the native side of a public function call: it reads the
// ACP Session's functions server, calls the tool and records what native got.
func fakeFunctionTurn(params json.RawMessage, update func(string, map[string]any), send func(any), promptID json.RawMessage) {
	var session struct {
		MCP []struct {
			Name    string `json:"name"`
			URL     string `json:"url"`
			Headers []struct{ Name, Value string }
		} `json:"mcpServers"`
	}
	if json.Unmarshal(params, &session) != nil || len(session.MCP) != 1 || session.MCP[0].Name != "functions" || writeMCPRegistry(os.Getenv("MINIMAX_DATA_DIR"), mcpRegistryEntry("functions", "functions", "lookup", "lookup")) != nil {
		os.Exit(20)
	}
	server := session.MCP[0]
	update("tool_call", map[string]any{"toolCallId": "t1", "name": "mcp__functions__lookup", "status": "in_progress", "rawInput": map[string]any{"q": "x"}})
	client := mcp.NewClient(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	connection, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerTransport{server.Headers[0].Value}}}, nil)
	if err != nil {
		os.Exit(21)
	}
	result, err := connection.CallTool(context.Background(), &mcp.CallToolParams{Name: "lookup", Arguments: map[string]any{"q": "x"}})
	text := "error"
	if err == nil {
		text = result.Content[0].(*mcp.TextContent).Text
	}
	if os.WriteFile(os.Getenv("OAC_TEST_FN_RESULT"), []byte(text), 0600) != nil {
		return
	}
	update("tool_call_update", map[string]any{"toolCallId": "t1", "status": "completed", "rawOutput": "ignored"})
	raw, _ := json.Marshal(map[string]string{"stopReason": "end_turn"})
	send(rpcFrame{JSONRPC: "2.0", ID: promptID, Result: raw})
}

func functionTurn(t *testing.T, scenario string) (*Session, <-chan proto.Envelope, string) {
	t.Helper()
	req := helperRequest(t, scenario, false)
	req.FunctionTools = []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	record := t.TempDir() + "/result"
	t.Setenv("OAC_TEST_FN_RESULT", record)
	out := make(chan proto.Envelope, 32)
	session, err := startTurn(t, t.Context(), req, out)
	if err != nil {
		t.Fatal(err)
	}
	return session, out, record
}
func waitFunctionCall(t *testing.T, out <-chan proto.Envelope) proto.FunctionCallPayload {
	t.Helper()
	for event := range out {
		if event.Type == proto.TypeFunctionCall {
			var call proto.FunctionCallPayload
			_ = json.Unmarshal(event.Payload, &call)
			return call
		}
	}
	t.Fatal("no function call")
	return proto.FunctionCallPayload{}
}

// Native calls the served tool, blocks until the result arrives, receives it,
// and the receipt is confirmed by the native tool result.
func TestFunctionCallRoundTrip(t *testing.T) {
	session, out, record := functionTurn(t, "functions")
	call := waitFunctionCall(t, out)
	if call.Name != "lookup" || string(call.Arguments) != `{"q":"x"}` {
		t.Fatalf("call = %+v", call)
	}
	functions := session.executor.opts.Functions
	if response, err := http.Post(functions.url, "application/json", strings.NewReader("{}")); err != nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: %v", err)
	}
	text := "answer"
	if err := session.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{DeliveryID: "d", CallID: call.CallID, Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &text}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(record); string(got) != "answer" {
		t.Fatalf("native received %q", got)
	}
	confirmed := false
	for event := range out {
		var payload proto.ToolCallPayload
		if event.Type == proto.TypeError {
			t.Fatalf("error: %s", event.Payload)
		}
		if event.Type == proto.TypeToolCall && json.Unmarshal(event.Payload, &payload) == nil && payload.Stage == "after" && payload.Observation.Kind == "function" && payload.Observation.Status == "completed" {
			confirmed = true
		}
	}
	if !confirmed {
		t.Fatal("function receipt was not observed")
	}
}

// A pending call fails when the Turn is cancelled, and closing the Session
// leaves no listener behind.
func TestFunctionCallCancellationReleasesPendingCall(t *testing.T) {
	session, out, _ := functionTurn(t, "cancel-wait")
	functions := session.executor.opts.Functions
	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "1"}, nil)
	connection, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: functions.url, HTTPClient: &http.Client{Transport: bearerTransport{"Bearer " + functions.token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		_, err := connection.CallTool(t.Context(), &mcp.CallToolParams{Name: "lookup", Arguments: map[string]any{}})
		released <- err
	}()
	call := waitFunctionCall(t, out)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := session.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-released:
		if err == nil {
			t.Fatal("pending native call succeeded")
		}
	case <-ctx.Done():
		t.Fatal("pending native call was not released")
	}
	if err := session.SubmitFunctionResult(ctx, proto.FunctionResultPayload{DeliveryID: "d", CallID: call.CallID, Content: []proto.InputContent{}}); err == nil {
		t.Fatal("result accepted after cancellation")
	}
	if err := session.executor.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-functions.done:
	default:
		t.Fatal("function listener outlived the Session")
	}
}
