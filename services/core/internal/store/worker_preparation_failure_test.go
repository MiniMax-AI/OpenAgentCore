package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestWorkerSettlesConfirmedPreparationFailureAndAcceptsNewInput(t *testing.T) {
	for _, code := range []string{"preparation_failed", "invalid_configuration", "unsupported_configuration", "unsupported_preparation"} {
		t.Run(code, func(t *testing.T) {
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
			if code == "preparation_failed" {
				handle := acknowledgePreparation(h, prepare.ID)
				h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed", ErrorCode: code})
				nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
			} else {
				h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{State: "rejected", Operation: proto.TypeExecutionPrepare, ErrorCode: code})
			}
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
			handle := acknowledgePreparation(h, prepare.ID)
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
		})
	}
}

func TestWorkerRetriesUncertainPreparationFailure(t *testing.T) {
	for _, response := range []struct {
		name, state, operation, code, runID string
	}{
		{"connection_closed", "failed", "", "connection_closed", ""},
		{"cleanup_failed", "failed", "", "executor_cleanup_unconfirmed", ""},
		{"capacity", "rejected", proto.TypeExecutionPrepare, "preparation_capacity", ""},
		{"executor_capacity", "rejected", proto.TypeExecutionPrepare, "executor_capacity", ""},
		{"busy", "rejected", proto.TypeExecutionPrepare, "resource_unavailable", ""},
		{"cleanup_rejected", "rejected", proto.TypeExecutionPrepare, "executor_cleanup_unconfirmed", ""},
		{"unknown", "rejected", proto.TypeExecutionPrepare, "unknown_rejection", ""},
		{"wrong_operation", "rejected", proto.TypeExecutionStart, "unsupported_configuration", ""},
		{"unknown_operation", "rejected", "", "unsupported_configuration", ""},
		{"run_present", "rejected", proto.TypeExecutionPrepare, "unsupported_configuration", "unconfirmed-run"},
	} {
		t.Run(response.name, func(t *testing.T) {
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
			status := proto.PreparationStatusPayload{State: response.state, Operation: response.operation, ErrorCode: response.code, RunID: response.runID}
			if response.state == "failed" {
				status.Handle = acknowledgePreparation(h, prepare.ID)
				status.Revision = 2
			}
			h.write(prepare.ID, proto.TypePreparationStatus, status)
			if response.state == "failed" {
				nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
			}
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

func TestWorkerPreparationRejectionPreservesCancellationAndNewerInput(t *testing.T) {
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`), false)
	enableWorkerEnvironment(t, h)
	frames := workerFrames(t, h)
	first, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "first", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, stop := startEnvironmentExpiryWorker(t, h.d)
	defer stop()
	old := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	if _, err := h.s.CancelEnvironmentInput(t.Context(), h.tenant, h.session.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	next, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "next", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"next"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	rejection := proto.PreparationStatusPayload{State: "rejected", Operation: proto.TypeExecutionPrepare, ErrorCode: "unsupported_configuration"}
	h.write(old.ID, proto.TypePreparationStatus, rejection)
	prepare := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	for id, state := range map[string]string{first.ID: store.EnvironmentInputCancelled, next.ID: store.EnvironmentInputPending} {
		current, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, id)
		if err != nil || current.State != state {
			t.Fatal("late rejection changed cancellation or newer input", err)
		}
		if id == next.ID && !current.Deadline.Equal(next.Deadline) {
			t.Fatal("late rejection changed the newer input deadline")
		}
	}
	// A delayed duplicate belongs to the old request, even on the same connection.
	h.write(old.ID, proto.TypePreparationStatus, rejection)
	handle := acknowledgePreparation(h, prepare.ID)
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	startFrame := nextWorkerFrame(t, frames, proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if startFrame.DecodePayload(&start) != nil || start.RunID == "" || inputTextForTest(t, start.Input) != "next" {
		t.Fatal("stale rejection prevented the newer input from starting")
	}
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "complete"})
}
