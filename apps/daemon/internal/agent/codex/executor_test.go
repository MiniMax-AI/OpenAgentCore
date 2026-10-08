package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/contracttest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func executorFixture(t *testing.T, mode string) (*Executor, string) {
	t.Helper()
	req, cfg, root := preparationFixture(t)
	e, err := testExecutor(t, mode, req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e, root
}

// testExecutor prepares through the production factory and closes the owner at cleanup.
func testExecutor(t *testing.T, mode string, req agent.PrepareRequest, cfg sessionConfig) (*Executor, error) {
	t.Helper()
	t.Setenv("OAC_TEST_EXECUTOR_MODE", mode)
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	t.Cleanup(cancelOwner)
	e, err := newExecutor(ownerCtx, req, cfg)
	if e != nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.Close(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	return e, err
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

// settledFrames waits for a Turn to settle and returns its complete output.
func settledFrames(t *testing.T, turn agent.Turn, out <-chan proto.Envelope) []proto.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := turn.AwaitSettlement(ctx); errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Turn did not settle")
	}
	var frames []proto.Envelope
	for frame := range out {
		frames = append(frames, frame)
	}
	return frames
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
	if !e.base.rpc.Alive() {
		t.Fatal("normal completion closed executor")
	}
}

func TestExecutorTurnStartUsesDefaultRequestBudget(t *testing.T) {
	t.Setenv("OAC_TEST_EXECUTOR_START_DELAY", "11s")
	e, root := executorFixture(t, "complete")
	out := make(chan proto.Envelope, 20)
	turn, err := e.StartTurn(t.Context(), "delayed-ack", proto.TextInput("answer"), out)
	if err != nil {
		t.Fatalf("valid turn/start acknowledgement after the former 10s limit: %v", err)
	}
	if !awaitExecutorTurn(t, turn, out).Reusable {
		t.Fatal("delayed acknowledgement did not retain a reusable Turn")
	}
	starts := 0
	for _, frame := range preparationFrames(t, root) {
		if frame.Method == "turn/start" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("input submitted %d times", starts)
	}
}

func TestExecutorTurnStartBudgetFailuresRetainOwnership(t *testing.T) {
	for _, mode := range []string{"request-timeout", "parent-cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("OAC_TEST_EXECUTOR_START_DELAY", "1s")
			e, root := executorFixture(t, "complete")
			if mode == "request-timeout" {
				e.base.rpc.cfg.RequestTimeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			out := make(chan proto.Envelope, 20)
			type result struct {
				turn agent.Turn
				err  error
			}
			done := make(chan result, 1)
			go func() {
				turn, err := e.StartTurn(ctx, "uncertain-ack", proto.TextInput("answer"), out)
				done <- result{turn, err}
			}()
			waitPreparationMethod(t, root, "turn/start")
			if mode == "parent-cancel" {
				cancel()
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("turn/start ignored its request budget or parent cancellation")
			}
			if got.turn == nil || got.turn != e.active || got.err == nil {
				t.Fatal("uncertain submission lost exact Turn ownership", got.err)
			}
			if mode == "parent-cancel" && !errors.Is(got.err, context.Canceled) {
				t.Fatal("parent cancellation was not preserved", got.err)
			}
			if mode == "request-timeout" && (ctx.Err() != nil || !strings.Contains(got.err.Error(), "turn/start timed out after 100ms")) {
				t.Fatal("configured RPC budget was not preserved", got.err, ctx.Err())
			}
			settleCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			settlement, err := got.turn.AwaitSettlement(settleCtx)
			if err == nil || errors.Is(err, context.DeadlineExceeded) || settlement.Reusable {
				t.Fatal("uncertain native start fabricated settlement", settlement, err)
			}
			terminals := 0
			for frame := range out {
				if frame.Type == proto.TypeDone {
					terminals++
				}
			}
			if terminals != 1 {
				t.Fatalf("terminal count %d", terminals)
			}
			if turn, err := e.StartTurn(t.Context(), "no-replay", proto.TextInput("answer"), make(chan proto.Envelope, 20)); turn != nil || err == nil {
				t.Fatal("uncertain executor admitted another input", err)
			}
			if err := e.Close(settleCtx); err != nil {
				t.Fatal(err)
			}
			waitExecutorRelease(t, e, root)
			starts := 0
			for _, frame := range preparationFrames(t, root) {
				if frame.Method == "turn/start" {
					starts++
				}
			}
			if starts != 1 {
				t.Fatalf("input submitted %d times", starts)
			}
		})
	}
}

func TestExecutorFreezesPreparedConfiguration(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "resumed"}[resume], func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			expectedThread := "thread/start"
			if resume {
				req.AgentSessionID, expectedThread = "fixture-native-thread", "thread/resume"
			}
			e, err := testExecutor(t, "complete", req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			assertPreparationOnly(t, root)
			cwd := e.plan.Cwd
			// Caller-owned data cannot revise the prepared native configuration.
			req.Model = "different-model"
			req.AgentSessionID = "different-thread"
			copy(req.FunctionTools[0].Parameters, strings.ReplaceAll(string(req.FunctionTools[0].Parameters), "integer", "boolean"))
			out := make(chan proto.Envelope, 20)
			turn, err := e.StartTurn(t.Context(), "actual-run", proto.TextInput("actual prompt"), out)
			if err != nil {
				t.Fatal(err)
			}
			awaitExecutorTurn(t, turn, out)
			counts := map[string]int{}
			for _, frame := range preparationFrames(t, root) {
				counts[frame.Method]++
				var params struct {
					Model        string                `json:"model"`
					ThreadID     string                `json:"threadId"`
					DynamicTools []dynamicFunctionTool `json:"dynamicTools"`
					Cwd          string                `json:"cwd"`
					Environments json.RawMessage       `json:"environments"`
				}
				if err := json.Unmarshal(frame.Params, &params); err != nil {
					t.Fatal(err)
				}
				if frame.Method == "thread/start" && (params.Model != "fixture-model" || len(params.DynamicTools) != 1 ||
					!strings.Contains(string(params.DynamicTools[0].InputSchema), "integer") || params.Cwd != cwd) {
					t.Fatal("prepared configuration changed", string(frame.Params))
				}
				if frame.Method == "thread/resume" && params.ThreadID != "fixture-native-thread" {
					t.Fatal("prepared resume changed")
				}
				if len(params.Environments) != 0 {
					t.Fatal("prepared environment changed")
				}
			}
			if counts["initialize"] != 1 || counts["environment/status"] != 2 || counts[expectedThread] != 1 || counts["turn/start"] != 1 {
				t.Fatal("unexpected native setup/start count", counts)
			}
		})
	}
}

func TestExecutorUnavailableOwnerStartsNoTurn(t *testing.T) {
	for _, reason := range []string{"closed", "owner cancelled", "rpc exited"} {
		t.Run(reason, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			owner, cancelOwner := context.WithCancel(context.Background())
			defer cancelOwner()
			e, err := newExecutor(owner, req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close(context.Background())
			switch reason {
			case "closed":
				err = e.Close(t.Context())
			case "owner cancelled":
				cancelOwner()
			case "rpc exited":
				err = e.base.rpc.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			out := make(chan proto.Envelope, 1)
			if turn, err := e.StartTurn(t.Context(), "late-run", proto.TextInput("must not start"), out); turn != nil || err == nil {
				t.Fatal("unavailable executor started a Turn", err)
			}
			if len(out) != 0 {
				t.Fatal("rejected start emitted output")
			}
			assertPreparationOnly(t, root)
		})
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
	process, err := clirunner.Start(clirunner.StartOptions{Parent: t.Context(), Binary: os.Args[0], Args: []string{"-test.run=^TestJSONRPCClientFakeCodexProcess$", "--"}, Env: append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1", "GORACE=atexit_sleep_ms=0"), NeedStdin: true})
	if err != nil {
		t.Fatal(err)
	}
	rpc := NewJSONRPCClient(JSONRPCConfig{})
	rpc.process, rpc.stdin, rpc.alive = process, process.Stdin, true

	var reap sync.Once
	t.Cleanup(func() { process.Cancel(); reap.Do(rpc.waitChild) })
	_, cancel := context.WithCancel(t.Context())
	var cleaned atomic.Bool
	e := &Executor{base: &Session{rpc: rpc, cancelFn: cancel}, plan: SessionPlan{Cleanup: func() { cleaned.Store(true) }}}
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
	if e.base.rpc.Alive() || len(preparedCatalogs(t, root)) != 0 {
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
	if e.base.rpc.Alive() || len(preparedCatalogs(t, root)) != 0 {
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

func TestSharedTextLifecycle(t *testing.T) {
	e, _ := executorFixture(t, "complete")
	contracttest.TextLifecycle(t, contracttest.TextFixture{
		Executor:      e,
		CompleteInput: proto.TextInput("answer"), ActiveInput: proto.TextInput("hold"), SteeringInput: proto.TextInput("continue"),
		Ready:       func(e proto.Envelope) bool { return e.Type == proto.TypeDelta },
		NativeOwner: func() string { return fmt.Sprint(e.base.rpc.process.Cmd.Process.Pid) },
	})
}
