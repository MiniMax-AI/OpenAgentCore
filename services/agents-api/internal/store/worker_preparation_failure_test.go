package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestWorkerSettlesConfirmedPreparationFailureAndAcceptsNewInput(t *testing.T) {
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`), false)
	enableWorkerEnvironment(t, h)
	frames := workerFrames(t, h)
	pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "first", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, stop := startEnvironmentExpiryWorker(t, h.d)
	defer stop()
	prepare := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, prepare.ID)
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed", ErrorCode: "preparation_failed"})
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "failed reservation settlement", func() bool {
		current, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, pending.ID)
		return err == nil && current.State == store.EnvironmentInputFailed
	})
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.PendingInput || session.LastTurn != nil || session.EnvironmentInputActivity == nil || session.EnvironmentInputActivity.Failure != "runtime_preparation_failed" {
		t.Fatal("preparation did not release input with a safe failure", err)
	}
	changes, err := h.s.ListSessionEvents(t.Context(), h.tenant, h.session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	for _, change := range changes {
		if change.Event.Type == "agent.session.failed" {
			failures++
			if !change.Settled || change.EnvironmentInputActivity.Failure != "runtime_preparation_failed" {
				t.Fatal("failure event lost settlement")
			}
		}
	}
	if failures != 1 {
		t.Fatal("failure events", failures)
	}
	select {
	case frame := <-frames:
		t.Fatal("failed input retried", frame.Type)
	case <-time.After(1200 * time.Millisecond):
	}
	next, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "next", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"next"}`)}})
	if err != nil {
		t.Fatal("new input remained blocked", err)
	}
	prepare = nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	handle = acknowledgePreparation(h, prepare.ID)
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	startFrame := nextWorkerFrame(t, frames, proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if startFrame.DecodePayload(&start) != nil || start.RunID == "" || inputTextForTest(t, start.Input) != "next" {
		t.Fatal("new input was not admitted")
	}
	current, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, next.ID)
	if err != nil || current.State != store.EnvironmentInputAdmitted {
		t.Fatal("new input state", err)
	}
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "complete"})
}

func TestWorkerRetriesUncertainPreparationFailure(t *testing.T) {
	for _, code := range []string{"connection_closed", "executor_cleanup_unconfirmed"} {
		t.Run(code, func(t *testing.T) {
			h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`), false)
			enableWorkerEnvironment(t, h)
			frames := workerFrames(t, h)
			pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "retry", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"retry"}`)}})
			if err != nil {
				t.Fatal(err)
			}
			_, stop := startEnvironmentExpiryWorker(t, h.d)
			defer stop()
			prepare := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
			handle := acknowledgePreparation(h, prepare.ID)
			h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed", ErrorCode: code})
			nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
			nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
			current, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, pending.ID)
			if err != nil || current.State != store.EnvironmentInputPending || !current.Deadline.Equal(pending.Deadline) {
				t.Fatal("transient failure settled or extended input", err)
			}
			if _, err := h.s.CancelEnvironmentInput(t.Context(), h.tenant, h.session.ID, pending.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
