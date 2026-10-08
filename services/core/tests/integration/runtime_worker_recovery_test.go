package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setWorkerComputePhase puts the harness's allocation in phase, with
// compute retained for an hour.
func setWorkerComputePhase(t *testing.T, pool *pgxpool.Pool, h *dispatchHarness, phase string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase = $2, compute_retained_until = clock_timestamp() + interval '1 hour' WHERE id = $1`, h.resource.ID, phase); err != nil {
		t.Fatal(err)
	}
}

func runtimeWorkerHarness(t *testing.T) (*dispatchHarness, *pgxpool.Pool) {
	t.Helper()
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"enabled"}}}`))
	enableWorkerEnvironment(t, h)
	setWorkerComputePhase(t, h.s.pool, h, "waking")
	return h, h.s.pool
}

func TestPreparedDispatchKeepsPendingReservationAfterComputeConflict(t *testing.T) {
	h, _ := runtimeWorkerHarness(t)
	h.d, h.lease = h.bound(), h.owner().Lease
	pending, err := sessionService(t, h.s).ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "pending", []sessions.Input{messageInput("first")})
	if err != nil {
		t.Fatal(err)
	}
	result := runPreparedDispatch(h, t.Context(), pending)
	frame := h.read(proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	got := awaitPreparedDispatch(t, result)
	if !errors.Is(got.err, sessions.ErrTurnConflict) || got.run.Reservation.ID != pending.ID || got.run.Reservation.State != sessions.EnvironmentInputPending {
		t.Fatal("rejected promotion lost its pending owner", got)
	}
	assertPreparationReleased(t, h, frame.ID, handle)
	session, err := sessionAdapter(h.s).GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil {
		t.Fatal("blocked promotion started a Turn", session, err)
	}
}

func TestWorkerWaitsForComputeAndSurvivesPromotionConflict(t *testing.T) {
	h, pool := runtimeWorkerHarness(t)
	pending, err := sessionService(t, h.s).ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "pending", []sessions.Input{messageInput("first")})
	if err != nil {
		t.Fatal(err)
	}
	worker := startWorker(t, t.Context(), h.s, h.d)
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
	environment, err := sessionAdapter(h.s).GetSessionEnvironment(t.Context(), h.tenant, h.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The read waits for the compute to wake and gives up at its deadline
	// without a native preparation, despite the socket already advertising
	// capabilities.
	read, stop := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer stop()
	if _, err := worker.ReadEnvironmentDirectory(read, environment, ""); !errors.Is(err, execution.ErrExecutionUnavailable) {
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
	stored, err := sessionAdapter(h.s).GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, pending.ID)
	if err != nil || stored.State != sessions.EnvironmentInputPending || len(stored.Receipts) != 0 {
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
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{})
	completeEmptyArtifactExport(t, h, frames)
	if release := nextWorkerFrame(t, frames, proto.TypeExecutionRelease); release.ID != prepare.ID {
		t.Fatal("completed preparation was not released")
	}
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "original input completed once", func() bool {
		turn, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
		return err == nil && turn.Status == sessions.TurnCompleted
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
	worker, err := startWorkerErr(t, t.Context(), h.s, h.d)
	if err != nil {
		t.Fatal("queued wake blocked Core startup", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := worker.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	got, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, turn)
	if err != nil || got.Status != sessions.TurnQueued || !got.StartedAt.IsZero() {
		t.Fatal("startup consumed queued work before restore", got, err)
	}
}
