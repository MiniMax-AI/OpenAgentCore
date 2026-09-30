package store_test

import (
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestWorkerEnvironmentRetriesNewlyReadyAtNextScan(t *testing.T) {
	h := newDispatchHarness(t)
	enableWorkerEnvironment(t, h)
	pending := workerEnvironmentReservation(t, h)
	runtime := h.environments[pending.SessionID]
	caps := workerEnvironmentCapabilities()
	caps.Preparation = proto.CapabilityUnsupported
	awaitFixtureCapabilities(t, runtime, caps)
	frames := workerFrames(t, h, runtime)

	h.session = publicSession(t, h, "scan-barrier")
	receipt := h.message("barrier", "ordinary work")
	scanned := time.Now()
	_, stop := startEnvironmentExpiryWorker(t, h.db, h.d)
	// Dispatch starts only after selectWork has examined the pending input on
	// the same pass, while its exact Runtime is still incapable of preparation.
	barrier := nextWorkerFrame(t, frames, testExecutionRequest)
	if barrier.ID != receipt.TurnID {
		t.Fatal("unexpected scan barrier")
	}
	awaitFixtureCapabilities(t, runtime, workerEnvironmentCapabilities())
	h.write(barrier.ID, proto.TypeDone, proto.DonePayload{Content: "complete"})
	waitTurn(t, h, barrier.ID, store.TurnCompleted)

	prepare := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	if elapsed := time.Since(scanned); elapsed < 750*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("readiness retry must use the next scan, without an empty scan interval", elapsed)
	}
	if workerRuntimeForPreparation(t, h, prepare) != runtime {
		t.Fatal("readiness retry moved Runtime ownership")
	}
	handle := acknowledgePreparation(runtime, prepare.ID)
	runtime.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed"})
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	stop()
	stored, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, pending.SessionID, pending.ID)
	if err != nil || stored.State != store.EnvironmentInputPending || !stored.Deadline.Equal(pending.Deadline) || len(stored.Receipts) != 0 {
		t.Fatal("readiness retry changed pending identity or admitted work", stored, err)
	}
}

func TestWorkerEnvironmentPaginationReachesReadyTail(t *testing.T) {
	h := newDispatchHarness(t)
	enableWorkerEnvironment(t, h)
	var last store.EnvironmentInputReservation
	for range 101 {
		pending := unboundWorkerEnvironmentReservation(t, h)
		if pending.ID > last.ID {
			last = pending
		}
	}
	session, err := h.s.GetSession(t.Context(), h.tenant, last.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := connectFixtureRuntime(t, h, session)
	h.environments[last.SessionID] = runtime
	frames := workerFrames(t, h, runtime)
	h.session = publicSession(t, h, "page-barrier")
	receipt := h.message("barrier", "ordinary work")
	scanned := time.Now()
	_, stop := startEnvironmentExpiryWorker(t, h.db, h.d)
	barrier := nextWorkerFrame(t, frames, testExecutionRequest)
	if barrier.ID != receipt.TurnID {
		t.Fatal("unexpected page barrier")
	}
	h.write(barrier.ID, proto.TypeDone, proto.DonePayload{Content: "complete"})
	waitTurn(t, h, barrier.ID, store.TurnCompleted)
	// The first 100 unbound inputs must not pin the cursor, and the ready
	// tail must wait for its own bounded page rather than an unbounded drain.
	prepare := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	if elapsed := time.Since(scanned); elapsed < 750*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("pagination lost the scan bound or starved the ready tail", elapsed)
	}
	if workerRuntimeForPreparation(t, h, prepare) != runtime {
		t.Fatal("pagination selected the wrong Runtime")
	}
	handle := acknowledgePreparation(runtime, prepare.ID)
	runtime.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed"})
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	stop()
}
