package store_test

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func readyExecutorAttempt(t *testing.T, h *dispatchHarness) (proto.Envelope, proto.ExecutionStartPayload) {
	t.Helper()
	prepare := h.read(proto.TypeExecutionPrepare)
	var configuration proto.ExecutionPreparePayload
	if prepare.DecodePayload(&configuration) != nil || configuration.SessionID != h.session.ID || len(configuration.Configuration.Input) != 0 {
		t.Fatal("invalid Executor preparation identity")
	}
	handle, executor := uuid.NewString(), uuid.NewString()
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, ExecutorID: executor, Revision: 1, State: "ready"})
	frame := h.read(proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if frame.ID != prepare.ID || frame.DecodePayload(&start) != nil || start.Handle != handle || start.ExecutorID != executor {
		t.Fatal("Start lost its admission or Executor identity")
	}
	return frame, start
}

func TestExecutorRecoveryRetriesOnlyConfirmedUnsubmittedInput(t *testing.T) {
	for _, code := range []string{"executor_unavailable", "executor_cleanup_unconfirmed", "start_failed"} {
		t.Run(code, func(t *testing.T) {
			h := newDispatchHarness(t)
			receipt := h.message("input", "execute once")
			result := h.run(t.Context(), receipt.TurnID)
			first, start := readyExecutorAttempt(t, h)
			h.write(first.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{State: "rejected", Operation: proto.TypeExecutionStart, ErrorCode: code})
			if code != "executor_unavailable" {
				h.finished(result, sessions.TurnFailed)
				return
			}
			next, replacement := readyExecutorAttempt(t, h)
			if next.ID == first.ID || replacement.ExecutorID == start.ExecutorID || replacement.RunID != start.RunID || inputTextForTest(t, replacement.Input) != "execute once" {
				t.Fatal("recovery changed input or reused retired control ownership")
			}
			h.write(next.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: replacement.Handle, ExecutorID: replacement.ExecutorID, Revision: 2, State: "started", RunID: replacement.RunID})
			h.write(replacement.RunID, proto.TypeDone, proto.DonePayload{Content: "once"})
			h.finished(result, sessions.TurnCompleted)
		})
	}
}

func TestExecutorReadinessRejectsChangedOwner(t *testing.T) {
	h := newDispatchHarness(t)
	receipt := h.message("input", "execute once")
	result := h.run(t.Context(), receipt.TurnID)
	frame, start := readyExecutorAttempt(t, h)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: start.Handle, ExecutorID: uuid.NewString(), Revision: 2, State: "started", RunID: start.RunID})
	h.finished(result, sessions.TurnFailed)
}
