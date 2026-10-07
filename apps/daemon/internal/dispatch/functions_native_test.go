package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/codex"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

type nativeFunctionSender chan proto.Envelope

func (s nativeFunctionSender) Send(ctx context.Context, e proto.Envelope) error {
	select {
	case s <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestNativeFunctionBridge(t *testing.T) {
	root := os.Getenv("OAC_TEST_NATIVE_PROOF_DIR")
	if root == "" {
		t.Skip("explicit native Codex binary and proof directory required")
	}
	home, err := os.MkdirTemp(root, "daemon-functions-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", home)
	var count atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		n := count.Add(1)
		raw, _ := json.MarshalIndent(body, "", "  ")
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("request-%d.json", n)), raw, 0600)
		var item map[string]any
		if n%2 == 1 {
			if !strings.Contains(string(raw), "lookup_ticket") {
				t.Error("tool was not registered")
			}
			item = map[string]any{"id": fmt.Sprintf("fc_%d", n), "type": "function_call", "call_id": fmt.Sprintf("call_%d", n), "name": "lookup_ticket", "arguments": `{"ticket":"42"}`, "status": "completed"}
		} else {

			var request struct {
				Input []struct {
					Type   string          `json:"type"`
					CallID string          `json:"call_id"`
					Output json.RawMessage `json:"output"`
				} `json:"input"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
			}
			found := false
			for _, entry := range request.Input {
				if entry.Type != "function_call_output" || entry.CallID != fmt.Sprintf("call_%d", n-1) {
					continue
				}
				found = true
				var parts []proto.InputContent
				if err := json.Unmarshal(entry.Output, &parts); err != nil {
					t.Error(err)
					continue
				}
				expected := functionResultContent("TICKET-RESULT")
				if !reflect.DeepEqual(parts, expected) {
					t.Errorf("native result lost text/image content or order: %s", entry.Output)
				}
			}
			if !found {
				t.Error("native model did not receive function result")
			}
			item = map[string]any{"id": fmt.Sprintf("msg_%d", n), "type": "message", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "FUNCTION-OK", "annotations": []any{}}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(kind string, data map[string]any) {
			data["type"] = kind
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
			w.(http.Flusher).Flush()
		}
		send("response.created", map[string]any{"response": map[string]any{"id": fmt.Sprintf("r_%d", n), "status": "in_progress", "output": []any{}}})
		send("response.output_item.added", map[string]any{"output_index": 0, "item": item})
		send("response.output_item.done", map[string]any{"output_index": 0, "item": item})
		send("response.completed", map[string]any{"response": map[string]any{"id": fmt.Sprintf("r_%d", n), "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": "gpt-5.5", "output": []any{item}}})
	}))
	defer model.Close()
	reg := agent.NewRegistry()
	registerExecutorKind(reg, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{FunctionTools: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported})}, codex.NewExecutorFactory())
	sender := make(nativeFunctionSender, 256)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	// answer is the text of the last completed assistant message.
	var answer string
	await := func(kind string) proto.Envelope {
		t.Helper()
		for {
			select {
			case env := <-sender:
				if env.Type == proto.TypeError {
					t.Fatalf("native error: %s", env.Payload)
				}
				var message proto.OutputMessagePayload
				if env.Type == proto.TypeOutputMessage && env.DecodePayload(&message) == nil && message.Text != nil {
					answer = *message.Text
				}
				if env.Type == kind {
					return env
				}
			case <-ctx.Done():
				t.Fatalf("waiting for %s; evidence %s", kind, home)
			}
		}
	}
	awaitReady := func(id string) proto.PreparationStatusPayload {
		t.Helper()
		for {
			env := await(proto.TypePreparationStatus)
			var status proto.PreparationStatusPayload
			if env.ID != id {
				continue
			}
			if err := env.DecodePayload(&status); err != nil {
				t.Fatal(env, err)
			}
			if status.State == "ready" {
				return status
			}
			if status.State != "preparing" {
				t.Fatalf("preparation %s: %+v", id, status)
			}
		}
	}
	const session = "native-functions"
	nativeID := ""
	// Each Run owns a fresh Router, so every resume starts a new native process.
	run := func(index int) {
		router, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := router.Shutdown(ctx); err != nil {
				t.Error(err)
			}
		}()
		run := fmt.Sprintf("run-%d", index)
		answer = ""
		assign(t, router, session, "")
		request := noEnvironmentPreparation(session, proto.PromptRequestPayload{AgentKind: "codex", AgentSessionID: nativeID,
			FunctionTools: []proto.FunctionTool{{Name: "lookup_ticket", Description: "Read a synthetic ticket", Parameters: json.RawMessage(`{"type":"object","properties":{"ticket":{"type":"string"}},"required":["ticket"],"additionalProperties":false}`)}}})
		request.Configuration.Model, request.Configuration.ModelProvider = "gpt-5.5", &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: model.URL + "/v1", APIKey: "synthetic-local-token"}
		if err := router.Handle(ctx, scoped(t, session, proto.TypeExecutionPrepare, run, request)); err != nil {
			t.Fatal(err)
		}
		ready := awaitReady(run)
		if err := router.Handle(ctx, scoped(t, session, proto.TypeExecutionStart, run, proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID, RunID: run, Input: proto.TextInput("Look up ticket 42.")})); err != nil {
			t.Fatal(err)
		}
		call := await(proto.TypeFunctionCall)
		var payload proto.FunctionCallPayload
		if err := call.DecodePayload(&payload); err != nil || call.ID != run || payload.Name != "lookup_ticket" {
			t.Fatal(call, err)
		}
		if index == 2 {
			if err := router.Handle(ctx, scoped(t, session, proto.TypePromptCancel, run, proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
				t.Fatal(err)
			}
			ack := await(proto.TypeInteractionDecisionAck)
			var receipt proto.InteractionDecisionAckPayload
			_ = ack.DecodePayload(&receipt)
			if !receipt.Applied {
				t.Fatal(receipt)
			}
			if err := router.Handle(ctx, scoped(t, session, proto.TypeFunctionResult, run, proto.FunctionResultPayload{CallID: payload.CallID, Success: true, Content: functionResultContent("late"), DeliveryID: "late"})); err != nil {
				t.Fatal(err)
			}
			_ = await(proto.TypeInteractionDecisionAck).DecodePayload(&receipt)
			if receipt.Applied || receipt.ErrorCode != "not_pending" {
				t.Fatal(receipt)
			}
			return
		}
		if err := router.Handle(ctx, scoped(t, session, proto.TypeFunctionResult, run, proto.FunctionResultPayload{CallID: payload.CallID, Success: index == 0, Content: functionResultContent("TICKET-RESULT"), DeliveryID: "result"})); err != nil {
			t.Fatal(err)
		}
		ack := await(proto.TypeInteractionDecisionAck)
		var receipt proto.InteractionDecisionAckPayload
		_ = ack.DecodePayload(&receipt)
		if !receipt.Applied {
			t.Fatal(receipt)
		}
		done := await(proto.TypeDone)
		var output proto.DonePayload
		_ = done.DecodePayload(&output)
		id, _ := output.Metadata[proto.DoneMetaAgentSessionID].(string)
		if answer != "FUNCTION-OK" || id == "" || (nativeID != "" && id != nativeID) {
			t.Fatal(output)
		}
		nativeID = id
	}
	for index := range 3 {
		run(index)
	}
	t.Logf("Native function success/failure, fresh-process resume, cancellation and late-result rejection passed; evidence %s", home)
}
