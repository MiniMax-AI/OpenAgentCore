package mcode

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// This opt-in test makes real model calls and uses an isolated native home.
// Provider options are read from a private file and never included in failures.
func TestNativeMCodeExecutorReuse(t *testing.T) {
	binary, options := os.Getenv("OAC_RUNTIME_MCODE_BIN"), os.Getenv("OAC_TEST_MCODE_REAL_OPTIONS")
	if binary == "" || options == "" {
		t.Skip("native executable and private provider options required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	if version, err := CheckCLIAvailable(ctx, binary); err != nil || version != SupportedVersion {
		t.Fatal("pinned native version verification failed")
	}
	raw, err := os.ReadFile(options)
	if err != nil {
		t.Fatal("private provider options unavailable")
	}
	req := executionRequest(t)
	req.ReleaseOnCompletion = false
	req.RunID, req.Input, req.ConversationID = "", nil, ""
	if json.Unmarshal(raw, &req.AgentOptions) != nil {
		t.Fatal("invalid private provider options")
	}
	value, err := NewExecutorFactory(nil)(ctx, req)
	if err != nil {
		t.Fatal("native Executor preparation failed")
	}
	e := value.(*executor)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if e.Close(cleanup) != nil {
			t.Error("native owner cleanup failed")
		}
	}()
	pid, nativeID := e.connection.process.Cmd.Process.Pid, e.nativeSession
	assertOwner := func() {
		t.Helper()
		if e.connection.process.Cmd.Process.Pid != pid || e.nativeSession != nativeID {
			t.Fatal("native owner changed")
		}
		select {
		case <-e.connection.exited:
			t.Fatal("native process exited")
		default:
		}
	}
	run := func(id, prompt string) agent.Turn {
		t.Helper()
		out := make(chan proto.Envelope, 256)
		turn, startErr := e.StartTurn(ctx, id, proto.TextInput(prompt), out)
		if startErr != nil || turn == nil {
			t.Fatal("native Turn admission failed")
		}
		content := ""
		doneCount := 0
		for event := range out {
			if event.ID != id {
				t.Fatal("event crossed Turn boundary")
			}
			switch event.Type {
			case proto.TypeError:
				t.Fatal("native Turn reported an error")
			case proto.TypeDone:
				var done proto.DonePayload
				if json.Unmarshal(event.Payload, &done) != nil {
					t.Fatal("invalid completion")
				}
				content = done.Content
				doneCount++
			}
		}
		settlement, settleErr := turn.AwaitSettlement(ctx)
		if settleErr != nil || !settlement.Reusable {
			t.Fatal("normal Turn did not establish reusable settlement")
		}
		if doneCount != 1 || !strings.Contains(content, "ORCHID-72-BLUE") {
			t.Fatal("native conversation did not retain the private test marker")
		}
		assertOwner()
		t.Logf("Verified marker recall and retained native owner for %s", id)
		return turn
	}
	first := run("first", "Remember the exact marker ORCHID-72-BLUE for this conversation. Reply with only that marker.")
	run("second", "What exact marker did I ask you to remember? Reply with only that marker.")
	out := make(chan proto.Envelope, 256)
	interrupted, err := e.StartTurn(ctx, "cancelled", proto.TextInput("Produce 2000 numbered lines now. Each line must contain a different sentence about rain. Start immediately at line 1 and continue without stopping or summarizing."), out)
	if err != nil || interrupted == nil {
		t.Fatal("cancellation Turn admission failed")
	}
	streaming := false
	for !streaming {
		select {
		case event, ok := <-out:
			if !ok || event.Type == proto.TypeDone || event.Type == proto.TypeError {
				t.Fatal("Turn ended before live cancellation could be tested")
			}
			if event.Type == proto.TypeDelta {
				streaming = true
			}
		case <-ctx.Done():
			t.Fatal("native stream did not begin before deadline")
		}
	}
	select {
	case <-interrupted.(*Session).finished:
		t.Fatal("cancellation was not in flight")
	default:
	}
	staleCtx, staleStop := context.WithTimeout(ctx, 5*time.Second)
	if first.Cancel(staleCtx) != nil {
		staleStop()
		t.Fatal("stale cancellation failed")
	}
	staleStop()
	interrupted.(*Session).mu.Lock()
	wasCancelled := interrupted.(*Session).cancelled
	interrupted.(*Session).mu.Unlock()
	if wasCancelled {
		t.Fatal("stale cancellation targeted current Turn")
	}
	stopCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	cancelErr := interrupted.Cancel(stopCtx)
	settlement, settleErr := interrupted.AwaitSettlement(stopCtx)
	stop()
	if settleErr != nil || cancelErr != nil {
		t.Fatal("native cancellation did not settle", cancelErr, settleErr)
	}
	if !settlement.Reusable {
		t.Log("Native cancellation cannot establish reusable settlement; successor must use recovery")
		successor := make(chan proto.Envelope, 1)
		next, nextErr := e.StartTurn(ctx, "unsafe-successor", proto.TextInput("must not execute"), successor)
		if next != nil || nextErr == nil {
			t.Fatal("unsettled native owner accepted a successor")
		}
		close(successor)
		cleanup, cleanupStop := context.WithTimeout(ctx, 15*time.Second)
		if e.Close(cleanup) != nil {
			cleanupStop()
			t.Fatal("invalid native owner did not close")
		}
		cleanupStop()
		req.AgentSessionID = nativeID
		recovered, recoverErr := NewExecutorFactory(nil)(ctx, req)
		if recoverErr != nil || recovered == nil {
			t.Fatal("exact native history recovery failed")
		}
		e = recovered.(*executor)
		if e.nativeSession != nativeID || e.connection.process.Cmd.Process.Pid == pid {
			t.Fatal("recovery did not replace the process while retaining native history")
		}
		pid = e.connection.process.Cmd.Process.Pid
		run("recovery", "What exact marker did I ask you to remember at the beginning? Reply with only that marker.")
		t.Log("Verified exact history continuation in a replacement native owner after nonreusable cancellation")
		return
	}
	if cancelErr != nil {
		t.Fatal("settled cancellation reported an error")
	}
	assertOwner()
	run("continuation", "What exact marker did I ask you to remember at the beginning? Reply with only that marker.")
	t.Log("Verified native process and session reuse across two normal Turns, active cancellation, and continuation")
}
