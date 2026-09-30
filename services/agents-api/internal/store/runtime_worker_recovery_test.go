package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertWorkerRuntimeAllocation(t *testing.T, pool *pgxpool.Pool, h *dispatchHarness, phase string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key,state,create_settled,compute_phase,compute_retained_until,deployment_generation)
		VALUES($1,$2,$3,$4,'running',true,$5,clock_timestamp()+interval '1 hour',(SELECT generation FROM runtime_deployment))`, uuid.NewString(), h.device.EnvironmentID, h.device.ID, uuid.NewString(), phase)
	if err != nil {
		t.Fatal(err)
	}
}

func runtimeWorkerHarness(t *testing.T) (*dispatchHarness, *pgxpool.Pool) {
	t.Helper()
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"enabled"}}}`), true)
	enableWorkerEnvironment(t, h)
	_, pool := store.NewTestStore(t)
	insertWorkerRuntimeAllocation(t, pool, h, "waking")
	return h, pool
}

func TestPreparedDispatchKeepsPendingReservationAfterComputeConflict(t *testing.T) {
	h, _ := runtimeWorkerHarness(t)
	lease, err := h.s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	h.d.Store = lease.Store()
	pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "pending", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	result := runPreparedDispatch(h, t.Context(), pending)
	frame := h.read(proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	got := awaitPreparedDispatch(t, result)
	if !errors.Is(got.err, store.ErrTurnConflict) || got.run.Reservation.ID != pending.ID || got.run.Reservation.State != store.EnvironmentInputPending {
		t.Fatal("rejected promotion lost its pending owner", got)
	}
	assertPreparationReleased(t, h, frame.ID, handle)
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil {
		t.Fatal("blocked promotion started a Turn", session, err)
	}
}

func TestWorkerWaitsForComputeAndSurvivesPromotionConflict(t *testing.T) {
	h, pool := runtimeWorkerHarness(t)
	pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "pending", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := execution.StartWorker(t.Context(), h.d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
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
	frames := workerFrames(t, h)
	environment, err := h.s.GetSessionEnvironment(t.Context(), h.tenant, h.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// This enters the same binding gate as scheduled input and returns without
	// a native preparation, despite the socket already advertising capabilities.
	if _, err := worker.ReadEnvironmentDirectory(t.Context(), environment, ""); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("waking connection was treated as work-ready", err)
	}
	select {
	case frame := <-frames:
		t.Fatal("work preceded lifecycle resume", frame.Type)
	default:
	}
	setPhase := func(phase string) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase=$2 WHERE environment_id=$1`, environment.ID, phase); err != nil {
			t.Fatal(err)
		}
	}
	setPhase("running")
	prepare := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, prepare.ID)
	// Force the final authoritative Store fence after the readiness observation.
	setPhase("waking")
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	release := nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	if release.ID != prepare.ID {
		t.Fatal("conflicted preparation was not released")
	}
	stored, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, pending.ID)
	if err != nil || stored.State != store.EnvironmentInputPending || len(stored.Receipts) != 0 {
		t.Fatal("conflict consumed queued input", stored, err)
	}
	setPhase("running")
	prepare = nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	handle = acknowledgePreparation(h, prepare.ID)
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	frame := nextWorkerFrame(t, frames, proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if frame.DecodePayload(&start) != nil || start.Handle != handle || inputTextForTest(t, start.Input) != "first" {
		t.Fatal("retry changed pending work", start)
	}
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "completed once"})
	completeEmptyArtifactExport(t, h, frames)
	if release := nextWorkerFrame(t, frames, proto.TypeExecutionRelease); release.ID != prepare.ID {
		t.Fatal("completed preparation was not released")
	}
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "original input completed once", func() bool {
		turn, err := h.s.GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
		return err == nil && turn.Status == store.TurnCompleted
	})
	var turns int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM turns WHERE session_id=$1`, h.session.ID).Scan(&turns); err != nil || turns != 1 {
		t.Fatal("retry duplicated the Turn", turns, err)
	}
	select {
	case err := <-done:
		done <- err
		t.Fatal("recoverable promotion conflict stopped the worker", err)
	default:
	}
}

func TestWorkerRestartPreservesQueuedTurnWhileComputeWakes(t *testing.T) {
	h, pool := runtimeWorkerHarness(t)
	turn := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO turns(id,session_id,status) VALUES($1,$2,'queued')`, turn, h.session.ID); err != nil {
		t.Fatal(err)
	}
	worker, err := execution.StartWorker(t.Context(), h.d)
	if err != nil {
		t.Fatal("queued wake blocked Core startup", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := worker.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	got, err := h.s.GetTurn(t.Context(), h.tenant, h.session.ID, turn)
	if err != nil || got.Status != store.TurnQueued || !got.StartedAt.IsZero() {
		t.Fatal("startup consumed queued work before restore", got, err)
	}
}
