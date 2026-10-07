package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func TestFailedResumeDoesNotStartFresh(t *testing.T) {
	client, server, cleanup := NewTestClient()
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := &Session{rpc: client.JSONRPCClient, cancelCtx: ctx, cfg: sessionConfig{logger: log.With("component", "resume-test")}}
	result := make(chan error, 1)
	go func() {
		result <- s.resolveThread(proto.PromptRequestPayload{AgentSessionID: "existing"}, SessionPlan{})
	}()
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	decoder := json.NewDecoder(server.FromClient)
	encoder := json.NewEncoder(server.ToClient)
	if err := decoder.Decode(&req); err != nil {
		t.Fatal(err)
	}
	if req.Method != "thread/resume" {
		t.Fatal(req.Method)
	}
	if err := encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32600, "message": "native history unavailable"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("failed resume started a fresh thread")
		}
	case <-ctx.Done():
		t.Fatal("thread resolution did not finish")
	}
}

func TestCancellationKeepsConsumedUsageAndIdentity(t *testing.T) {
	out := make(chan proto.Envelope, 8)
	s := &Session{runID: "run", out: out, cancelCtx: context.Background(), cfg: sessionConfig{logger: log.With("component", "cancel-test")}}
	s.setThreadID("native")
	s.onTurnStarted(json.RawMessage(`{"threadId":"native","turn":{"id":"turn"}}`))
	s.onTurnCompleted(json.RawMessage(`{"threadId":"native","turn":{"id":"turn","status":"interrupted","usage":{"inputTokens":31,"outputTokens":7}}}`))
	snapshot := s.CancellationOutcome()
	if snapshot.Usage.InputTokens != 31 || snapshot.Usage.OutputTokens != 7 || snapshot.Metadata[proto.DoneMetaAgentSessionID] != "native" {
		t.Fatalf("incomplete terminal snapshot: %+v", snapshot)
	}
}
