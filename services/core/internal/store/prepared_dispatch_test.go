package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type preparedDispatchResult struct {
	run execution.EnvironmentRun
	err error
}

func preparedDispatchHarness(t *testing.T) (*dispatchHarness, store.EnvironmentInputReservation) {
	t.Helper()
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model","instructions":"Keep this instruction."},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`), true)
	assertNoRuntimeAllocation(t, h)
	lease, err := h.s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	h.d.Store = lease.Store()
	enableWorkerEnvironment(t, h)
	pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "pending", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}, {Kind: "message", Payload: json.RawMessage(`{"text":"second"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return h, pending
}

func runPreparedDispatch(h *dispatchHarness, ctx context.Context, pending store.EnvironmentInputReservation) <-chan preparedDispatchResult {
	out := make(chan preparedDispatchResult, 1)
	go func() {
		result, err := h.d.RunEnvironmentInput(ctx, h.tenant, h.session.ID, pending.ID)
		out <- preparedDispatchResult{result, err}
	}()
	return out
}

func awaitPreparedDispatch(t *testing.T, result <-chan preparedDispatchResult) preparedDispatchResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("prepared dispatcher did not finish")
		return preparedDispatchResult{}
	}
}

func acknowledgePreparation(h *dispatchHarness, request string) string {
	handle := uuid.NewString()
	h.write(request, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 1, State: "preparing", ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli()})
	return handle
}

func readyPreparedDispatch(t *testing.T, h *dispatchHarness, request, handle string) proto.ExecutionStartPayload {
	t.Helper()
	h.write(request, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready", ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli()})
	frame := h.read(proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if frame.ID != request || frame.DecodePayload(&start) != nil || start.Handle != handle || start.RunID == "" || inputTextForTest(t, start.Input) != "first\n\nsecond" {
		t.Fatal("Start changed preparation or original batch", frame.ID, start)
	}
	turn, err := h.s.GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
	if err != nil || turn.Status != store.TurnInProgress {
		t.Fatal("Start preceded atomic claim", turn, err)
	}
	return start
}

func TestPreparedDispatchPromotesOriginalBatchAndPersistsCompletion(t *testing.T) {
	h, pending := preparedDispatchHarness(t)
	result := runPreparedDispatch(h, t.Context(), pending)
	frame := h.read(proto.TypeExecutionPrepare)
	var prepare proto.ExecutionPreparePayload
	if frame.DecodePayload(&prepare) != nil || len(prepare.Configuration.Input) != 0 || prepare.Configuration.RunID != "" || prepare.Configuration.ConversationID != "" || prepare.Configuration.LocalEnvironment == nil || prepare.Configuration.LocalEnvironment.ID != h.device.EnvironmentID || prepare.Configuration.DisableExecutionEnvironment {
		t.Fatal("invalid preparation configuration", prepare)
	}
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil {
		t.Fatal("preparation created work before readiness", session, err)
	}
	items, err := h.s.ListItems(t.Context(), h.tenant, h.session.ID, "", 100, true)
	if err != nil || len(items.Items) != 0 {
		t.Fatal("preparation published input history", items, err)
	}
	handle := acknowledgePreparation(h, frame.ID)
	start := readyPreparedDispatch(t, h, frame.ID, handle)
	late := h.message("later", "third")
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	steering := h.read(proto.TypePromptSteer)
	var steer proto.PromptSteerPayload
	if steering.ID != start.RunID || steering.DecodePayload(&steer) != nil || inputTextForTest(t, steer.Input) != "third" || !steer.DurableReceipt {
		t.Fatal("later input bypassed ordinary steering", steer)
	}
	h.write(start.RunID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: steer.InputID, Accepted: true})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "answer", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "retained-prepared-native"}})
	completeEmptyArtifactExport(t, h)
	got := awaitPreparedDispatch(t, result)
	if got.err != nil || got.run.Turn.Status != store.TurnCompleted || len(got.run.Reservation.Receipts) != 2 || got.run.Reservation.Receipts[0].Replayed || got.run.Reservation.Receipts[1].Sequence >= late.Sequence {
		t.Fatal("prepared completion", got)
	}
	assertPreparationReleased(t, h, frame.ID, handle)
	bound, err := h.s.GetSessionExecutionBinding(t.Context(), h.tenant, h.session.ID)
	if err != nil || bound.NativeSessionID != "retained-prepared-native" {
		t.Fatal("native identity was not committed", bound, err)
	}
	retry, err := h.d.RunEnvironmentInput(t.Context(), h.tenant, h.session.ID, pending.ID)
	if err != nil || len(retry.Reservation.Receipts) != 2 || !retry.Reservation.Receipts[0].Replayed || retry.Reservation.Receipts[0].TurnID != start.RunID || retry.Turn.ID != "" {
		t.Fatal("replay executed again", retry, err)
	}
}

func TestPreparedDispatchOwnerOutlivesReservationDeadline(t *testing.T) {
	h, pending := preparedDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := runPreparedDispatch(h, parent, pending)
	frame := h.read(proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, frame.ID)
	start := readyPreparedDispatch(t, h, frame.ID, handle)
	if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := h.d.Store.ExpireEnvironmentInput(t.Context(), h.tenant, h.session.ID, pending.ID)
	if err != nil || stored.State != store.EnvironmentInputAdmitted {
		t.Fatal("admitted execution lost its owner to the pending-input deadline", err)
	}
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "completed after the reservation deadline"})
	completeEmptyArtifactExport(t, h)
	got := awaitPreparedDispatch(t, result)
	if got.err != nil || got.run.Turn.Status != store.TurnCompleted {
		t.Fatal("completion did not settle the execution owner", got)
	}
	assertPreparationReleased(t, h, frame.ID, handle)
}

// A self-hosted Session's frozen provider travels only in the preparation sent
// to the executor bound to that Session, not to another executor of the tenant.
func TestSelfHostedProviderReachesOnlyBoundExecutor(t *testing.T) {
	h, pending := preparedDispatchHarness(t)
	other, err := h.s.CreateSession(t.Context(), h.tenant, store.WithFixtureModelProvider(store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)}))
	if err != nil {
		t.Fatal(err)
	}
	bystander := connectFixtureRuntime(t, h, other)
	ctx, cancel := context.WithCancel(t.Context())
	result := runPreparedDispatch(h, ctx, pending)
	frame := h.read(proto.TypeExecutionPrepare)
	var prepare proto.ExecutionPreparePayload
	if frame.DecodePayload(&prepare) != nil {
		t.Fatal("invalid preparation")
	}
	provider, _ := prepare.Configuration.AgentOptions["model_provider"].(map[string]any)
	fixture := store.FixtureModelProvider("codex")
	if provider["base_url"] != fixture.BaseURL || provider["api_key"] != fixture.APIKey {
		t.Fatal("bound executor did not receive the frozen provider", prepare.Configuration.AgentOptions)
	}
	_ = bystander.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		var raw json.RawMessage
		if bystander.conn.ReadJSON(&raw) != nil {
			break
		}
		if strings.Contains(string(raw), fixture.APIKey) || strings.Contains(string(raw), proto.TypeExecutionPrepare) {
			t.Fatal("another executor received the provider")
		}
	}
	cancel()
	awaitPreparedDispatch(t, result)
}
