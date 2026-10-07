package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Opt in with the installed native CLI; the default test gate uses protocol fixtures.
func TestNativeMCodeACP(t *testing.T) {
	binary := os.Getenv("OAC_TEST_MCODE_INTEGRATION_BIN")
	if binary == "" {
		t.Skip("set OAC_TEST_MCODE_INTEGRATION_BIN to run native ACP smoke test")
	}
	req := testRequest(t)
	t.Setenv("OAC_RUNTIME_MCODE_BIN", binary)
	var mu sync.Mutex
	var requests []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		raw, _ := json.Marshal(body)
		mu.Lock()
		requests = append(requests, string(raw))
		mu.Unlock()
		writeNativeResponse(w)
	}))
	defer model.Close()
	req.ModelProvider = &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: model.URL, APIKey: "fixture-only", ContextWindow: 64000, MaxOutputTokens: 4096}
	req.SystemPrompt = "SP-MCODE-672: reply concisely."
	runID, input := "run-1", proto.TextInput("Reply OAC-MCODE-OK.")
	run := func() proto.DonePayload {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		t.Cleanup(cancel)
		out := make(chan proto.Envelope, 64)
		if _, err := startTurn(t, ctx, req, runID, input, out); err != nil {
			t.Fatal(err)
		}
		var done proto.DonePayload
		var message proto.OutputMessagePayload
		for event := range out {
			switch event.Type {
			case proto.TypeError:
				t.Fatalf("native ACP failure: %s", event.Payload)
			case proto.TypeOutputMessage:
				_ = json.Unmarshal(event.Payload, &message)
			case proto.TypeDone:
				_ = json.Unmarshal(event.Payload, &done)
			}
		}
		if message.ID == "" || message.Status != "completed" || message.Text == nil || *message.Text != "OAC-MCODE-OK" {
			t.Fatalf("native output=%+v", message)
		}
		if _, ok := done.Metadata[proto.DoneMetaAgentSessionID].(string); !ok {
			t.Fatal("native session ID missing")
		}
		return done
	}
	done := run()
	mu.Lock()
	firstRequests := strings.Join(requests, "\n")
	requests = nil
	mu.Unlock()
	if !strings.Contains(firstRequests, "SP-MCODE-672") {
		t.Fatal("native instructions missing")
	}
	runID = "run-2"
	req.AgentSessionID = done.Metadata[proto.DoneMetaAgentSessionID].(string)
	req.SystemPrompt = "SP-MCODE-NEW: reply concisely."
	req.Model = "fixture-new"
	input = proto.TextInput("Now reply OAC-MCODE-OK.")
	run()
	mu.Lock()
	resumed := strings.Join(requests, "\n")
	mu.Unlock()
	if !strings.Contains(resumed, "SP-MCODE-NEW") {
		t.Fatal("resume retained stale instructions")
	}
	if !strings.Contains(resumed, `"model":"fixture-new"`) {
		t.Fatal("resume did not use the updated model")
	}
	t.Log("native new/resume, model and instructions verified")
}

func writeNativeResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(kind string, payload any) {
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
	}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_qa", "type": "message", "role": "assistant", "model": "fixture", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 20, "output_tokens": 0}}})
	send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
	send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "OAC-MCODE-OK"}})
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 8}})
	send("message_stop", map[string]string{"type": "message_stop"})
}
