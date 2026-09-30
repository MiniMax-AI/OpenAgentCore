package store_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestWorkerEnvironmentSharesCapacityThroughClaimAndCleanup(t *testing.T) {
	h := newDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	enableWorkerEnvironment(t, h)
	pending := map[string]store.EnvironmentInputReservation{}
	for range 2 {
		value := workerEnvironmentReservation(t, h)
		pending[value.SessionID] = value
	}
	runtimes := []*dispatchHarness{h}
	for _, runtime := range h.environments {
		runtimes = append(runtimes, runtime)
	}
	frames := workerFrames(t, runtimes...)
	ordinary := map[string]store.Session{}
	for _, key := range []string{"one", "two", "three"} {
		session := publicSession(t, h, key)
		h.session = session
		receipt := h.message(key, "ordinary")
		ordinary[receipt.TurnID] = session
	}
	_, stop := startEnvironmentExpiryWorker(t, h.db, h.d)
	var normal []proto.Envelope
	var preparing []proto.Envelope
	for range 4 {
		select {
		case frame := <-frames:
			switch frame.Type {
			case testExecutionRequest:
				normal = append(normal, frame)
			case proto.TypeExecutionPrepare:
				preparing = append(preparing, frame)
			default:
				t.Fatal("unexpected initial frame", frame.Type)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("mixed queues did not fill capacity")
		}
	}
	if len(normal) != 2 || len(preparing) != 2 {
		t.Fatal("mixed queues did not share capacity", len(normal), len(preparing))
	}
	first := preparing[0]
	firstRuntime := workerRuntimeForPreparation(t, h, first)
	handle := acknowledgePreparation(firstRuntime, first.ID)
	firstRuntime.write(first.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	frame := nextWorkerFrame(t, frames, proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if frame.ID != first.ID || frame.DecodePayload(&start) != nil || start.Handle != handle || inputTextForTest(t, start.Input) != "first" {
		t.Fatal("worker changed preparation at Start")
	}
	firstRuntime.write(first.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	second := preparing[1]
	secondRuntime := workerRuntimeForPreparation(t, h, second)
	secondHandle := acknowledgePreparation(secondRuntime, second.ID)
	var prepare proto.ExecutionPreparePayload
	if second.DecodePayload(&prepare) != nil {
		t.Fatal("invalid Prepare")
	}
	waiting := pending[strings.TrimPrefix(prepare.Configuration.AgentStateKey, "agents-api-")]
	if waiting.ID == "" {
		t.Fatal("wrong waiting Session")
	}
	select {
	case frame := <-frames:
		t.Fatal("claim released a slot or duplicated active preparation", frame.Type)
	case <-time.After(time.Second):
	}
	tenant, due := newEnvironmentExpiryReservation(t, h.s)
	makeEnvironmentExpiryDue(t, pool, &due)
	waitEnvironmentExpiry(t, h.s, tenant, due)
	if _, err := h.s.CancelEnvironmentInput(t.Context(), h.tenant, waiting.SessionID, waiting.ID); err != nil {
		t.Fatal(err)
	}
	// Distinct Runtime sockets do not promise cross-socket delivery order.
	var release proto.Envelope
	var resumed proto.Envelope
	for range 2 {
		select {
		case frame := <-frames:
			switch frame.Type {
			case proto.TypeExecutionRelease:
				if release.ID != "" {
					t.Fatal("duplicate preparation release")
				}
				release = frame
			case testExecutionRequest:
				if resumed.ID != "" {
					t.Fatal("cleanup freed more than one capacity slot")
				}
				resumed = frame
			default:
				t.Fatal("unexpected cleanup frame", frame.Type)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancel did not release preparation and resume queued work")
		}
	}
	var payload proto.ExecutionReleasePayload
	if release.ID != second.ID || release.DecodePayload(&payload) != nil || payload.Handle != secondHandle || resumed.ID == "" {
		t.Fatal("cancel released the wrong preparation or lost queued work")
	}
	normal = append(normal, resumed)
	for _, request := range normal {
		h.write(request.ID, proto.TypeDone, proto.DonePayload{Content: "ordinary complete"})
		h.session = ordinary[request.ID]
		waitTurn(t, h, request.ID, store.TurnCompleted)
	}
	firstRuntime.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "local complete"})
	completeEmptyArtifactExport(t, firstRuntime, frames)
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	stop()
	assertEnvironmentExpiryHasNoHistory(t, pool, waiting.SessionID)
	assertEnvironmentExpiryHasNoHistory(t, pool, due.SessionID)
}

func TestWorkerEnvironmentRetriesPendingWithoutExtendingDeadline(t *testing.T) {
	h := newDispatchHarness(t)
	enableWorkerEnvironment(t, h)
	pending := workerEnvironmentReservation(t, h)
	runtime := h.environments[pending.SessionID]
	frames := workerFrames(t, h, runtime)
	_, stop := startEnvironmentExpiryWorker(t, h.db, h.d)
	first := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	started := time.Now()
	handle := acknowledgePreparation(runtime, first.ID)
	runtime.write(first.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed"})
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	h.session = publicSession(t, h, "unrelated")
	receipt := h.message("ordinary", "make progress after preparation failure")
	request := nextWorkerFrame(t, frames, testExecutionRequest)
	if request.ID != receipt.TurnID {
		t.Fatal("preparation failure blocked ordinary work")
	}
	h.write(request.ID, proto.TypeDone, proto.DonePayload{Content: "complete"})
	waitTurn(t, h, request.ID, store.TurnCompleted)
	second := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	if elapsed := time.Since(started); elapsed < 750*time.Millisecond || elapsed > 3*time.Second || first.ID == second.ID {
		t.Fatal("pending preparation missed its next scan or reused a released owner")
	}
	acknowledgePreparation(runtime, second.ID)
	select {
	case frame := <-frames:
		t.Fatal("active preparation was duplicated", frame.Type)
	case <-time.After(time.Second):
	}
	stop()
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	stored, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, pending.SessionID, pending.ID)
	if err != nil || stored.State != store.EnvironmentInputPending || !stored.Deadline.Equal(pending.Deadline) || len(stored.Receipts) != 0 {
		t.Fatal("retry or shutdown changed the original reservation", stored, err)
	}
	_, stop = startEnvironmentExpiryWorker(t, h.db, h.d)
	third := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	handle = acknowledgePreparation(runtime, third.ID)
	runtime.write(third.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	request = nextWorkerFrame(t, frames, proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if request.ID != third.ID || json.Unmarshal(request.Payload, &start) != nil || start.Handle != handle {
		t.Fatal("restart changed retained preparation")
	}
	runtime.write(third.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	runtime.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "resumed"})
	completeEmptyArtifactExport(t, runtime, frames)
	run := awaitWorkerEnvironmentRun(t, t.Context(), h.s, h.tenant, pending)
	if run.Turn.Status != store.TurnCompleted || !run.Reservation.Deadline.Equal(pending.Deadline) {
		t.Fatal("restarted worker did not complete original work", run)
	}
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	stop()
}
