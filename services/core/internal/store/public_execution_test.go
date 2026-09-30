package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func publicSession(t *testing.T, h *dispatchHarness, key string) sessions.Session {
	t.Helper()
	value, err := h.s.CreateSession(context.Background(), h.tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: key, Configuration: json.RawMessage(`{"agent":{"id":"agent_test","model":"test-model","instructions":"Keep this."},"environment":{"type":"none"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestExecutionWorkerAdmissionBindingAndRecovery(t *testing.T) {
	h := newDispatchHarness(t)
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{Streaming: proto.CapabilitySupported, Steering: proto.CapabilitySupported, DurableTurns: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported, WebSearchControl: proto.CapabilitySupported, TextVerbosity: proto.CapabilitySupported, ExecutionControls: proto.CapabilitySupported, SubagentControl: proto.CapabilitySupported, ToolObservations: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported, Preparation: proto.CapabilitySupported})}}})
	h.session = publicSession(t, h, "public")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := startWorker(t, ctx, h.db, h.d)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
	})
	if second, err := startWorkerErr(ctx, h.db, h.d); err == nil {
		cancel()
		go second.Run(ctx)
		t.Fatal("second service acquired database")
	}
	inputs := []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"First"}]}]}`)}, {Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"Second"}]}]}`)}}
	receipts, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "batch", inputs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.SubmitInputs(ctx, uuid.NewString(), h.session.ID, "foreign", inputs); err == nil {
		t.Fatal("foreign tenant admitted")
	}
	retry, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "batch", inputs)
	if err != nil || !retry[0].Replayed || retry[0].TurnID != receipts[0].TurnID {
		t.Fatal(retry, err)
	}
	request := h.read(testExecutionRequest)
	var prompt proto.PromptRequestPayload
	if err := request.DecodePayload(&prompt); err != nil {
		t.Fatal(err)
	}
	if inputTextForTest(t, prompt.Input) != "First\n\nSecond" || !prompt.DisableExecutionEnvironment || !prompt.DisableSubagents || prompt.ExecutionControls == nil || *prompt.ExecutionControls != (proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}) || prompt.AgentOptions["web_search"] != nil || prompt.AgentOptions["model_verbosity"] != nil {
		t.Fatal(prompt)
	}
	bound, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
	if err != nil || bound.ID != h.device.ID {
		t.Fatal(bound, err)
	}
	active, err := h.s.GetSession(ctx, h.tenant, h.session.ID)
	if err != nil || active.LastTurn == nil || active.LastTurn.Status != sessions.TurnInProgress {
		t.Fatal(active, err)
	}
	h.write(request.ID, proto.TypeDone, proto.DonePayload{Content: "Answer", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "worker-native"}})
	waitTurn(t, h, receipts[0].TurnID, sessions.TurnCompleted)
	items, err := sessionReads(h.db.pool).ListItems(ctx, h.tenant, h.session.ID, "", 100, true)
	if err != nil || len(items.Items) != 3 {
		t.Fatal(items, err)
	}
	next, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "next", inputs[:1])
	if err != nil {
		t.Fatal(err)
	}
	request = h.read(testExecutionRequest)
	_ = request.DecodePayload(&prompt)
	if prompt.AgentSessionID != "worker-native" {
		t.Fatal(prompt)
	}
	cancel()
	waitTurn(t, h, next[0].TurnID, sessions.TurnFailed)
}

func waitTurn(t *testing.T, h *dispatchHarness, id, status string) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := h.s.GetTurn(context.Background(), h.tenant, h.session.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if turn.Status == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("turn %s did not become %s", id, status)
}

func TestWorkerRestartReconcilesClaimedButPreservesQueuedWork(t *testing.T) {
	h := newDispatchHarness(t)
	h.session = publicSession(t, h, "interrupted")
	first := h.message("first", "Already sent")
	ctx := context.Background()
	if _, err := h.s.TransitionTurn(ctx, h.tenant, h.session.ID, first.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	// A native measurement committed before process loss must survive startup
	// reconciliation even when no Done frame can be recovered.
	usage := json.RawMessage(`{"tokens":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":2,"total_tokens":13}}`)
	if err := h.s.AppendTurnEvents(ctx, h.tenant, h.session.ID, first.TurnID, 1, []sessions.ExecutionEvent{{Kind: proto.TypeUsage, Payload: usage}}); err != nil {
		t.Fatal(err)
	}
	checkMeasurement := func(ended bool) {
		t.Helper()
		turn, err := h.s.GetTurn(ctx, h.tenant, h.session.ID, first.TurnID)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			TotalTokens int64 `json:"total_tokens"`
		}
		if json.Unmarshal(turn.Usage, &got) != nil || got.TotalTokens != 13 {
			t.Fatalf("lost observed usage: %s", turn.Usage)
		}
		// Session usage stays unknown until every root Turn has ended (ST-03).
		want := turn.Usage
		if !ended {
			want = nil
		}
		session, err := h.s.GetSession(ctx, h.tenant, h.session.ID)
		if err != nil || string(session.Usage) != string(want) {
			t.Fatalf("Session and Turn measurement differ: %+v %v", session, err)
		}
	}
	checkMeasurement(false)
	queued := publicSession(t, h, "queued")
	if _, err := h.s.SubmitMessage(ctx, h.tenant, queued.ID, "first", json.RawMessage(`{"text":"Not sent"}`)); err != nil {
		t.Fatal(err)
	}
	worker := startWorker(t, ctx, h.db, h.d)
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if err := worker.Run(stopped); err != context.Canceled {
		t.Fatal(err)
	}
	interrupted, err := h.s.GetSession(ctx, h.tenant, h.session.ID)
	if err != nil || interrupted.LastTurn.Status != sessions.TurnFailed {
		t.Fatal(interrupted, err)
	}
	pending, err := h.s.GetSession(ctx, h.tenant, queued.ID)
	if err != nil || pending.LastTurn.Status != sessions.TurnQueued {
		t.Fatal(pending, err)
	}
	if _, err := h.s.RequestCancel(ctx, h.tenant, queued.ID, "stop-before-dispatch"); err != nil {
		t.Fatal(err)
	}
	pending, err = h.s.GetSession(ctx, h.tenant, queued.ID)
	if err != nil || pending.LastTurn.Status != sessions.TurnCancelled {
		t.Fatal(pending, err)
	}
	restarted, err := startWorkerErr(ctx, h.db, h.d)
	if err != nil {
		t.Fatal("lease not released", err)
	}
	if err := restarted.Run(stopped); err != context.Canceled {
		t.Fatal(err)
	}
	checkMeasurement(true)
}
