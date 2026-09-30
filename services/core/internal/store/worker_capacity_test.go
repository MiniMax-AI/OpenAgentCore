package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Consume the actual controls so capacity rejection is exercised before any
// input, without the ordinary fixture's automatic preparation acknowledgement.
func capacityWorkerFrames(t *testing.T, h *dispatchHarness) <-chan proto.Envelope {
	t.Helper()
	frames := make(chan proto.Envelope, 64)
	go func() {
		defer close(frames)
		for {
			var frame proto.Envelope
			if h.conn.ReadJSON(&frame) != nil {
				return
			}
			select {
			case frames <- frame:
			case <-t.Context().Done():
				return
			}
		}
	}()
	return frames
}

func TestWorkerDefersPreparationCapacityUntilCleanupReleasesSlot(t *testing.T) {
	h := newDispatchHarness(t)
	enableWorkerEnvironment(t, h)
	h.d.MaxConcurrentExecutions = 5
	sessions := make(map[string]string)
	for i := range 5 {
		h.session = publicSession(t, h, fmt.Sprintf("capacity-%d", i))
		receipt := h.message("work", "once")
		sessions[h.session.ID] = receipt.TurnID
	}
	frames := capacityWorkerFrames(t, h)
	_, stop := startEnvironmentExpiryWorker(t, h.d)
	defer stop()
	admissions := make(map[string]string)
	started := make(map[string]int)
	var blocked, first string
	capacityReleased := false
	rejections := 0
	process := func(frame proto.Envelope) {
		switch frame.Type {
		case proto.TypeExecutionPrepare:
			var prepare proto.ExecutionPreparePayload
			if frame.DecodePayload(&prepare) != nil || sessions[prepare.SessionID] == "" || len(prepare.Configuration.Input) != 0 || prepare.Configuration.RunID != "" {
				t.Fatal("invalid input-free preparation", prepare)
			}
			if blocked == "" && len(admissions) == 4 {
				blocked = prepare.SessionID
			}
			if prepare.SessionID == blocked && !capacityReleased {
				rejections++
				h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{State: "rejected", Operation: proto.TypeExecutionPrepare, ErrorCode: "preparation_capacity"})
				return
			}
			admissions[frame.ID] = prepare.SessionID
			h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: frame.ID, ExecutorID: "executor-" + prepare.SessionID, Revision: 1, State: "ready"})
		case proto.TypeExecutionStart:
			var start proto.ExecutionStartPayload
			session := admissions[frame.ID]
			if frame.DecodePayload(&start) != nil || session == "" || start.RunID != sessions[session] || inputTextForTest(t, start.Input) != "once" {
				t.Fatal("Start changed the original input or Turn", start)
			}
			started[session]++
			if started[session] != 1 || (session == blocked && !capacityReleased) {
				t.Fatal("capacity rejection replayed or prematurely started input")
			}
			if first == "" {
				first = session
			}
			h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: start.Handle, ExecutorID: start.ExecutorID, Revision: 2, State: "started", RunID: start.RunID})
		case proto.TypeExecutionRelease:
		default:
			t.Fatal("unexpected worker frame", frame.Type)
		}
	}
	next := func() {
		t.Helper()
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatal("Runtime connection closed")
			}
			process(frame)
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not send preparation or Start")
		}
	}
	for len(started) != 4 || rejections == 0 {
		next()
	}
	assertQueued := func() {
		t.Helper()
		turn, err := h.s.GetTurn(t.Context(), h.tenant, blocked, sessions[blocked])
		if err != nil || turn.Status != store.TurnQueued {
			t.Fatal("capacity rejection failed queued work", turn, err)
		}
	}
	// Observe multiple scheduler ticks with four active native owners. The
	// bounded attempt count detects a completion-triggered busy retry loop.
	observeCapacity := func() {
		t.Helper()
		before := rejections
		timer := time.NewTimer(800 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case frame := <-frames:
				process(frame)
				if rejections-before > 6 {
					t.Fatal("capacity rejection caused a busy retry loop")
				}
			case <-timer.C:
				if rejections == before {
					t.Fatal("queued work was not retried by scheduler")
				}
				assertQueued()
				return
			}
		}
	}
	observeCapacity()
	// After completion, Runtime can retire the idle Executor. Its cleanup
	// still owns a slot and reports the same capacity rejection.
	h.write(sessions[first], proto.TypeDone, proto.DonePayload{Content: "done"})
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "first completed Turn", func() bool {
		turn, err := h.s.GetTurn(t.Context(), h.tenant, first, sessions[first])
		return err == nil && turn.Status == store.TurnCompleted
	})
	observeCapacity()
	capacityReleased = true
	for started[blocked] == 0 {
		next()
	}
	for session, turn := range sessions {
		if session != first {
			h.write(turn, proto.TypeDone, proto.DonePayload{Content: "done"})
		}
	}
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "all five Turns completed", func() bool {
		for session, id := range sessions {
			turn, err := h.s.GetTurn(t.Context(), h.tenant, session, id)
			if err != nil || turn.Status != store.TurnCompleted {
				return false
			}
		}
		return true
	})
	if len(started) != 5 || started[blocked] != 1 {
		t.Fatal("queued Turn did not start exactly once", started)
	}
}

func TestWorkerDoesNotDeferOtherPreparationOrStartRejections(t *testing.T) {
	for _, test := range []struct{ name, operation, code string }{
		{"invalid_configuration", proto.TypeExecutionPrepare, "invalid_configuration"},
		{"resource_unavailable", proto.TypeExecutionPrepare, "resource_unavailable"},
		{"unknown_operation", "", "preparation_capacity"},
		{"start_capacity", proto.TypeExecutionStart, "preparation_capacity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newDispatchHarness(t)
			enableWorkerEnvironment(t, h)
			h.session = publicSession(t, h, test.name)
			receipt := h.message("work", "once")
			frames := capacityWorkerFrames(t, h)
			_, stop := startEnvironmentExpiryWorker(t, h.d)
			defer stop()
			prepare := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
			if test.operation == proto.TypeExecutionStart {
				h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: prepare.ID, Revision: 1, State: "ready"})
				nextWorkerFrame(t, frames, proto.TypeExecutionStart)
			}
			h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{State: "rejected", Operation: test.operation, ErrorCode: test.code})
			waitTurn(t, h, receipt.TurnID, store.TurnFailed)
		})
	}
}
