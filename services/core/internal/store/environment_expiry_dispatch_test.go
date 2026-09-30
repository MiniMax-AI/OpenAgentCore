package store_test

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func enableEnvironmentExpiryDispatch(h *dispatchHarness) {
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
		Streaming: proto.CapabilitySupported, Steering: proto.CapabilitySupported, DurableTurns: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported, WebSearchControl: proto.CapabilitySupported, TextVerbosity: proto.CapabilitySupported, ExecutionControls: proto.CapabilitySupported, SubagentControl: proto.CapabilitySupported, ToolObservations: proto.CapabilitySupported,
		Preparation: proto.CapabilitySupported})}}})
}

func TestWorkerEnvironmentExpiryAtFullExecutionCapacity(t *testing.T) {
	h := newDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	enableEnvironmentExpiryDispatch(h)
	worker, stop := startEnvironmentExpiryWorker(t, h.d)
	var requests []proto.Envelope
	var sessions []store.Session
	for _, key := range []string{"one", "two", "three", "four"} {
		session := publicSession(t, h, key)
		if _, err := worker.SubmitInputs(t.Context(), h.tenant, session.ID, key, []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"remain active"}`)}}); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, h.read(testExecutionRequest))
		sessions = append(sessions, session)
	}
	tenant, due := newEnvironmentExpiryReservation(t, h.s)
	makeEnvironmentExpiryDue(t, pool, &due)
	waitEnvironmentExpiry(t, h.s, tenant, due)
	for i, request := range requests {
		turn, err := h.s.GetTurn(t.Context(), h.tenant, sessions[i].ID, request.ID)
		if err != nil || turn.Status != store.TurnInProgress {
			t.Fatal("expiry was not observed at full capacity", turn, err)
		}
	}
	for i, request := range requests {
		h.write(request.ID, proto.TypeDone, proto.DonePayload{Content: "finished"})
		h.session = sessions[i]
		waitTurn(t, h, request.ID, store.TurnCompleted)
	}
	stop()
	assertEnvironmentExpiryHasNoHistory(t, pool, due.SessionID)
}

func TestWorkerEnvironmentExpirySkipsBusySessionAndAllowsDispatch(t *testing.T) {
	h := newDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	enableEnvironmentExpiryDispatch(h)
	lockedTenant, locked := newEnvironmentExpiryReservation(t, h.s)
	otherTenant, other := newEnvironmentExpiryReservation(t, h.s)
	makeEnvironmentExpiryDue(t, pool, &locked)
	makeEnvironmentExpiryDue(t, pool, &other)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SELECT id FROM sessions WHERE id=$1 FOR UPDATE", locked.SessionID); err != nil {
		t.Fatal(err)
	}
	worker, stop := startEnvironmentExpiryWorker(t, h.d)
	h.session = publicSession(t, h, "unrelated")
	receipt, err := worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "work", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"make normal progress"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	waitEnvironmentExpiry(t, h.s, otherTenant, other)
	request := h.read(testExecutionRequest)
	if request.ID != receipt[0].TurnID {
		t.Fatal("unrelated dispatch mismatch", request.ID)
	}
	h.write(request.ID, proto.TypeDone, proto.DonePayload{Content: "finished"})
	waitTurn(t, h, request.ID, store.TurnCompleted)
	var state string
	if err := pool.QueryRow(t.Context(), "SELECT state FROM environment_input_reservations WHERE id=$1", locked.ID).Scan(&state); err != nil || state != store.EnvironmentInputPending {
		t.Fatal("sweep did not honor Session lock", state, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitEnvironmentExpiry(t, h.s, lockedTenant, locked)
	stop()
	assertEnvironmentExpiryHasNoHistory(t, pool, locked.SessionID)
	assertEnvironmentExpiryHasNoHistory(t, pool, other.SessionID)
}
