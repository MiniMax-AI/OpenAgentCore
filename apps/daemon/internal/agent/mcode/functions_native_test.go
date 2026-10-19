package mcode

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// This opt-in test makes real model calls: the model calls a public function,
// native receives the submitted result, and the Turn finishes with it.
func TestNativeMCodePublicFunction(t *testing.T) {
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
	req.FunctionTools = []proto.FunctionTool{{Name: "lookup_code", Description: "Returns the secret access code. Always call it when asked for the access code.", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}
	e, err := prepareExecutor(t, ctx, req)
	if err != nil {
		t.Fatal("native Executor preparation failed")
	}
	out := make(chan proto.Envelope, 256)
	turn, err := e.StartTurn(ctx, "turn", proto.TextInput("Call the lookup_code function to get the access code, then reply with only the code."), out)
	if err != nil {
		t.Fatal("native Turn admission failed")
	}
	var content string
	submitted, confirmed := make(chan error, 1), false
	for event := range out {
		switch event.Type {
		case proto.TypeError:
			t.Fatalf("native Turn reported an error: %s", event.Payload)
		case proto.TypeFunctionCall:
			var call proto.FunctionCallPayload
			_ = json.Unmarshal(event.Payload, &call)
			text := "ACCESS-CODE-4417"
			go func() {
				submitted <- turn.(*Session).SubmitFunctionResult(ctx, proto.FunctionResultPayload{DeliveryID: "delivery", CallID: call.CallID, Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &text}}})
			}()
		case proto.TypeToolCall:
			var call proto.ToolCallPayload
			confirmed = confirmed || (json.Unmarshal(event.Payload, &call) == nil && call.Stage == "after" && call.Observation != nil && call.Observation.Kind == "function" && call.Observation.Status == "completed")
		case proto.TypeDone:
			var done proto.DonePayload
			_ = json.Unmarshal(event.Payload, &done)
			content = done.Content
		}
	}
	if _, err := turn.AwaitSettlement(ctx); err != nil {
		t.Fatal("native Turn did not settle")
	}
	select {
	case err := <-submitted:
		if err != nil {
			t.Fatal("function result was not applied:", err)
		}
	default:
		t.Fatal("the model never called the function")
	}
	if !confirmed || !strings.Contains(content, "ACCESS-CODE-4417") {
		t.Fatalf("confirmed=%v content=%q", confirmed, content)
	}
}
