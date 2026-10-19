package mcode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
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
	update("tool_call_update", map[string]any{"toolCallId": "t1", "status": "completed", "rawOutput": map[string]any{"details": map[string]any{"mcp": result}}})
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

// probeFunction is the native side of one call: it calls the tool over MCP and
// returns what native received.
func probeFunction(t *testing.T, functions *functionServer) <-chan *mcp.CallToolResult {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "1"}, nil)
	connection, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: functions.url, HTTPClient: &http.Client{Transport: bearerTransport{"Bearer " + functions.token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := connection.CallTool(t.Context(), &mcp.CallToolParams{Name: "lookup", Arguments: map[string]any{}})
		received <- result
	}()
	return received
}

func submitText(session *Session, callID, text string) error {
	return session.SubmitFunctionResult(context.Background(), proto.FunctionResultPayload{DeliveryID: "d", CallID: callID, Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &text}}})
}

func nativeEnd(session *Session, status string, callID string) {
	output := map[string]any{}
	if callID != "" {
		output["details"] = map[string]any{"mcp": map[string]any{"_meta": map[string]any{functionCallMeta: callID}}}
	}
	session.confirmFunction(toolUpdate{mcp: &mcpToolIdentity{server: "functions", tool: "lookup"}, Status: status, RawOutput: output})
}

// Calls of the same function run one after another; each gets its own result
// and its own receipt.
func TestFunctionCallsInSequence(t *testing.T) {
	session, out, _ := functionTurn(t, "cancel-wait")
	functions := session.executor.opts.Functions
	for _, answer := range []string{"first", "second"} {
		received := probeFunction(t, functions)
		call := waitFunctionCall(t, out)
		submitted := make(chan error, 1)
		go func() { submitted <- submitText(session, call.CallID, answer) }()
		result := <-received
		if got := result.Content[0].(*mcp.TextContent).Text; got != answer {
			t.Fatalf("native received %q, want %q", got, answer)
		}
		nativeEnd(session, "completed", call.CallID)
		if err := <-submitted; err != nil {
			t.Fatal(err)
		}
	}
}

// A call that native fails, such as at its call timeout, is withdrawn: a late
// result is refused at once and the next call still works.
func TestFunctionCallFailedByNativeIsWithdrawn(t *testing.T) {
	session, out, _ := functionTurn(t, "cancel-wait")
	functions := session.executor.opts.Functions
	received := probeFunction(t, functions)
	late := waitFunctionCall(t, out)
	nativeEnd(session, "failed", "")
	for event := range out {
		if event.Type == proto.TypeFunctionCallWithdrawn {
			var withdrawn proto.FunctionCallWithdrawnPayload
			if json.Unmarshal(event.Payload, &withdrawn) != nil || withdrawn.CallID != late.CallID {
				t.Fatalf("withdrawn = %s", event.Payload)
			}
			break
		}
	}
	if result := <-received; result != nil {
		t.Fatal("released call returned a result")
	}
	if err := submitText(session, late.CallID, "late"); !errors.Is(err, agent.ErrUnknownFunctionCall) {
		t.Fatalf("late result: %v", err)
	}
	received = probeFunction(t, functions)
	next := waitFunctionCall(t, out)
	submitted := make(chan error, 1)
	go func() { submitted <- submitText(session, next.CallID, "next") }()
	if result := <-received; result.Content[0].(*mcp.TextContent).Text != "next" {
		t.Fatal("next call did not get its result")
	}
	nativeEnd(session, "completed", next.CallID)
	if err := <-submitted; err != nil {
		t.Fatal(err)
	}
}

// A call still pending when the Turn ends is reported to native and refuses a result.
func TestFunctionCallPendingAtTurnEnd(t *testing.T) {
	session, out, _ := functionTurn(t, "cancel-wait")
	received := probeFunction(t, session.executor.opts.Functions)
	call := waitFunctionCall(t, out)
	session.stopFunctions()
	if result := <-received; result != nil {
		t.Fatal("pending call returned a result")
	}
	if err := submitText(session, call.CallID, "late"); !errors.Is(err, agent.ErrUnknownFunctionCall) {
		t.Fatalf("result after Turn end: %v", err)
	}
}
