package codex

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func executorFixture(t *testing.T, mode string) (*Executor, string) {
	t.Helper()
	req, cfg, root := preparationFixture(t)
	t.Setenv("OAC_TEST_EXECUTOR_MODE", mode)
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	t.Cleanup(cancelOwner)
	e, err := newExecutor(ownerCtx, req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return e, root
}
func awaitExecutorTurn(t *testing.T, turn agent.Turn, out <-chan proto.Envelope) agent.TurnSettlement {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	settlement, err := turn.AwaitSettlement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for frame := range out {
		if frame.Type == proto.TypeDone {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("terminal count %d", count)
	}
	return settlement
}
func TestExecutorNormalTurnsKeepProcessAndThread(t *testing.T) {
	e, root := executorFixture(t, "complete")
	var previous agent.Turn
	for _, id := range []string{"one", "two"} {
		out := make(chan proto.Envelope, 20)
		turn, err := e.StartTurn(t.Context(), id, proto.TextInput("answer"), out)
		if err != nil {
			t.Fatal(err)
		}
		if !awaitExecutorTurn(t, turn, out).Reusable {
			t.Fatal("healthy turn was not reusable")
		}
		if previous != nil {
			if err := previous.Cancel(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		previous = turn
	}
	counts := map[string]int{}
	pids := map[int]bool{}
	for _, f := range preparationFrames(t, root) {
		counts[f.Method]++
		pids[f.PID] = true
	}
	if counts["initialize"] != 1 || counts["thread/start"] != 1 || counts["turn/start"] != 2 || counts["turn/interrupt"] != 0 || len(pids) != 1 {
		t.Fatal(counts, pids)
	}
	if !e.prepared.session.rpc.Alive() {
		t.Fatal("normal completion closed executor")
	}
}
func TestExecutorCancellationSettlesThenReuses(t *testing.T) {
	e, root := executorFixture(t, "complete")
	out := make(chan proto.Envelope, 20)
	first, err := e.StartTurn(t.Context(), "cancel", proto.TextInput("hold"), out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartTurn(t.Context(), "overlap", proto.TextInput("answer"), make(chan proto.Envelope, 20)); err == nil {
		t.Fatal("overlapping turn admitted")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := first.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if !awaitExecutorTurn(t, first, out).Reusable {
		t.Fatal("cancelled healthy executor unavailable")
	}
	secondOut := make(chan proto.Envelope, 20)
	second, err := e.StartTurn(t.Context(), "next", proto.TextInput("hold"), secondOut)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	// A stale handle cannot interrupt its successor.
	interrupts := 0
	for _, f := range preparationFrames(t, root) {
		if f.Method == "turn/interrupt" {
			interrupts++
		}
	}
	if interrupts != 1 {
		t.Fatalf("stale cancellation sent %d interrupts", interrupts)
	}
	if err := second.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if !awaitExecutorTurn(t, second, secondOut).Reusable {
		t.Fatal("second cancellation did not settle")
	}
}
func TestExecutorStartErrorsRetainExactOwnership(t *testing.T) {
	for _, mode := range []string{"start-error", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			e, _ := executorFixture(t, mode)
			out := make(chan proto.Envelope, 20)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			turn, err := e.StartTurn(ctx, "before", proto.TextInput("answer"), out)
			if turn != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(turn, err)
			}
			select {
			case <-out:
				t.Fatal("pre-admission output was retained or closed")
			default:
			}
			turn, err = e.StartTurn(t.Context(), "after", proto.TextInput("answer"), out)
			if turn == nil || err == nil {
				t.Fatal("uncertain submission lost owner", err)
			}
			settlement, settleErr := turn.AwaitSettlement(t.Context())
			if settleErr == nil || settlement.Reusable {
				t.Fatal("failed submission fabricated native settlement", settlement, settleErr)
			}
			terminalCount := 0
			for frame := range out {
				if frame.Type == proto.TypeDone {
					terminalCount++
				}
			}
			if terminalCount != 1 {
				t.Fatalf("failed submission terminal count = %d", terminalCount)
			}
		})
	}
}

func TestExecutorCloseRetainsPlanUntilReaped(t *testing.T) {
	process, err := clirunner.Start(clirunner.StartOptions{Parent: t.Context(), Binary: os.Args[0], Args: []string{"-test.run=^TestJSONRPCClientFakeCodexProcess$", "--"}, Env: append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1", "GORACE=atexit_sleep_ms=0"), NeedStdin: true, OwnProcessGroup: true})
	if err != nil {
		t.Fatal(err)
	}
	rpc := NewJSONRPCClient(JSONRPCConfig{})
	rpc.process, rpc.cmd, rpc.stdin, rpc.alive = process, process.Cmd, process.Stdin, true

	var reap sync.Once
	t.Cleanup(func() { process.Cancel(); reap.Do(rpc.waitChild) })
	_, cancel := context.WithCancel(t.Context())
	var cleaned atomic.Bool
	e := &Executor{prepared: &Prepared{session: &Session{rpc: rpc, cancelFn: cancel}, plan: SessionPlan{Cleanup: func() { cleaned.Store(true) }}}}
	ctx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	if err = e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unreaped close", err)
	}
	if cleaned.Load() {
		t.Fatal("plan released before cleanup settled")
	}
	reap.Do(rpc.waitChild)
	if err = e.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !cleaned.Load() {
		t.Fatal("cleanup retry did not release plan")
	}
}

func TestExecutorCloseSeparatesTurnFailureFromResourceCleanup(t *testing.T) {
	e, root := executorFixture(t, "interrupt-error")
	out := make(chan proto.Envelope, 20)
	turn, err := e.StartTurn(t.Context(), "cancel-failure", proto.TextInput("hold"), out)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := turn.Cancel(ctx); err == nil {
		t.Fatal("native interrupt failure was hidden")
	}
	if _, err := turn.AwaitSettlement(ctx); err == nil {
		t.Fatal("failed Turn unexpectedly settled successfully")
	}
	if err := e.Close(ctx); err != nil {
		t.Fatal("failed Turn prevented confirmed resource cleanup", err)
	}
	if e.prepared.session.rpc.Alive() || len(preparedCatalogs(t, root)) != 0 {
		t.Fatal("Close did not release native process and plan")
	}
	if _, err := turn.AwaitSettlement(ctx); err == nil {
		t.Fatal("resource cleanup fabricated successful Turn settlement")
	}
}

func TestExecutorCloseTerminatesAfterMissingCancellationTerminal(t *testing.T) {
	e, root := executorFixture(t, "interrupt-no-terminal")
	out := make(chan proto.Envelope, 20)
	turn, err := e.StartTurn(t.Context(), "missing-terminal", proto.TextInput("hold"), out)
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, stopCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer stopCancel()
	cancelled := make(chan error, 1)
	go func() { cancelled <- turn.Cancel(cancelCtx) }()
	waitPreparationMethod(t, root, "turn/interrupt")
	closeCtx, stopClose := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stopClose()
	if err := e.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("missing receipt did not reach the cleanup deadline", err)
	}
	// Retrying waits on the same owner; the expired observation must not skip
	// the transport close that can actually stop an unresponsive native Turn.
	retry, stopRetry := context.WithTimeout(t.Context(), 2*time.Second)
	defer stopRetry()
	if err := e.Close(retry); err != nil {
		t.Fatal("Close never retired the native process", err)
	}
	if e.prepared.session.rpc.Alive() || len(preparedCatalogs(t, root)) != 0 {
		t.Fatal("unresponsive native process or plan still owned after Close")
	}
	select {
	case cancelErr := <-cancelled:
		if cancelErr == nil {
			t.Fatal("resource cleanup fabricated native cancellation confirmation")
		}
	case <-retry.Done():
		t.Fatal("original cancellation waiter was abandoned")
	}
}
