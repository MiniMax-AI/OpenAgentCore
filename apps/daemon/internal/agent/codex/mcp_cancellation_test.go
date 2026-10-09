package codex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

func cancellationMCPBindings(labels ...string) []agent.MCPBinding {
	var bindings []agent.MCPBinding
	for i, label := range labels {
		bindings = append(bindings, agent.MCPBinding{ServerLabel: label, ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: "stdio", Stdio: &agent.EnvironmentMCP{Server: agentplugin.MCPServer{Name: label, Type: "stdio", Command: agent.ViewAlias(i)}}})
	}
	return bindings
}

func TestMCPCancellationCapturesUnconfirmedNativeCalls(t *testing.T) {
	s := &Session{threadID: "root", cfg: sessionConfig{view: &viewLaunch{ViewSession: agent.ViewSession{MCP: cancellationMCPBindings("active", "failed", "late", "done", "child")}}}}
	s.steering.id = "turn"
	observe := func(thread, turn, id, label string, started bool, result any) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"type": "mcpToolCall", "id": id, "server": label, "status": "failed", "result": result, "error": map[string]string{"message": "local failure"}}})
		s.observeRootMCP(raw, started)
	}
	observe("root", "turn", "1", "active", true, nil)
	observe("root", "turn", "2", "failed", true, nil)
	observe("root", "turn", "2", "failed", false, nil)
	observe("root", "turn", "3", "done", true, nil)
	observe("root", "turn", "3", "done", false, map[string]any{"content": []any{}})
	observe("foreign", "turn", "4", "done", true, nil)
	observe("root", "old", "4", "done", true, nil)
	observe("root", "turn", "4", "http-or-unknown", true, nil)
	s.observeChildMCP("child-thread", "child-turn", []json.RawMessage{json.RawMessage(`{"type":"mcpToolCall","id":"1","server":"child","status":"inProgress"}`)})
	s.latchMCPCancellation()
	s.terminal.Store(true)
	observe("root", "turn", "1", "active", false, map[string]any{"content": []any{}})
	observe("root", "turn", "5", "late", true, nil)
	s.observeChildMCP("child-thread", "child-turn", []json.RawMessage{json.RawMessage(`{"type":"mcpToolCall","id":"1","server":"child","result":{"content":[]}}`)})
	if got := s.cancelledMCPLabels(); !slices.Equal(got, []string{"active", "child", "failed", "late"}) {
		t.Fatal(got)
	}
}

func TestMCPChildRealReplyBeforeCancellationReleasesOnlyItsCall(t *testing.T) {
	s := &Session{cfg: sessionConfig{view: &viewLaunch{ViewSession: agent.ViewSession{MCP: cancellationMCPBindings("child")}}}}
	s.observeChildMCP("child", "turn", []json.RawMessage{json.RawMessage(`{"type":"mcpToolCall","id":"call","server":"child"}`)})
	s.observeChildMCP("child", "turn", []json.RawMessage{json.RawMessage(`{"type":"mcpToolCall","id":"call","server":"child","result":{"content":[]}}`)})
	s.latchMCPCancellation()
	if s.hasPendingMCP("child", "turn") || len(s.cancelledMCPLabels()) != 0 {
		t.Fatal("real child reply was retained as unfinished work")
	}
}

func TestMCPChildFirstObservedTerminalUsesNativeRootAttribution(t *testing.T) {
	for _, test := range []struct {
		name, status, spawnTurn, rootTurn, result string
		captured                                  bool
	}{
		{"new-child-local-failure", "failed", "root-turn", "root-turn", `null`, true},
		{"reused-child-send-input-local-failure", "completed", "older-root-turn", "root-turn", `null`, true},
		{"current-child-interrupted", "interrupted", "root-turn", "root-turn", `null`, true},
		{"older-root-terminal", "failed", "older-root-turn", "older-root-turn", `null`, false},
		{"unknown-root-terminal", "failed", "older-root-turn", "", `null`, false},
		{"current-child-remote-reply", "completed", "root-turn", "root-turn", `{"content":[]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, fixture, out := observationSession(t, test.status)
			s.cfg.view = &viewLaunch{ViewSession: agent.ViewSession{MCP: cancellationMCPBindings("child")}}
			fixture.mu.Lock()
			fixture.rootTurnID = test.spawnTurn
			fixture.childRootTurnID = test.rootTurn
			fixture.rootSendInput = test.spawnTurn != "root-turn" && test.rootTurn == "root-turn"
			fixture.childItems = []json.RawMessage{json.RawMessage(`{"type":"mcpToolCall","id":"child-call","server":"child","tool":"wait","arguments":{},"status":"failed","error":{"message":"local wait failed"},"result":` + test.result + `}`)}
			fixture.persist(t)
			fixture.mu.Unlock()
			// No prior history sample has observed the child's running Turn. The
			// ordinary terminal drain reads paginated native history and rollout.
			s.latchMCPCancellation()
			s.onTurnCompleted(rootCompleted)
			collectObserved(t, out)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.AwaitSettlement(ctx); err != nil {
				t.Fatal(err)
			}
			var want []string
			if test.captured {
				want = []string{"child"}
			}
			if got := s.cancelledMCPLabels(); !slices.Equal(got, want) {
				t.Fatalf("captured %v, want %v", got, want)
			}
			fixture.mu.Lock()
			interrupts := fixture.interrupted
			fixture.mu.Unlock()
			if interrupts != 0 {
				t.Fatal("terminal child incorrectly interrupted", interrupts)
			}
		})
	}
}

func TestExecutorCancelledMCPRequiresScopeCloseThenInvalidationAndReload(t *testing.T) {
	for _, mode := range []string{"mcp-confirmed", "mcp-caller-cancelled", "mcp-stop-failed", "mcp-invalidate-unconfirmed", "mcp-reload-failed"} {
		t.Run(mode, func(t *testing.T) {
			e, root := executorFixture(t, mode)
			e.base.cfg.view.MCP = cancellationMCPBindings("active", "late", "healthy")
			stopping := make(chan []string, 1)
			release := make(chan struct{})
			e.base.cfg.view.StopMCP = func(ctx context.Context, labels []string) error {
				stopping <- append([]string(nil), labels...)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if mode == "mcp-stop-failed" {
					return errors.New("scope closure unconfirmed")
				}
				return nil
			}
			out := make(chan proto.Envelope, 30)
			turn, err := e.StartTurn(t.Context(), "cancel", proto.TextInput("hold"), out)
			if err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			for {
				select {
				case frame := <-out:
					if frame.Type == proto.TypeDelta {
						goto started
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		started:
			cancelled := make(chan error, 1)
			callerCtx, cancelCaller := context.WithCancel(ctx)
			defer cancelCaller()
			go func() { cancelled <- turn.Cancel(callerCtx) }()
			select {
			case labels := <-stopping:
				if !slices.Equal(labels, []string{"active", "late"}) {
					t.Fatal(labels)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case err := <-cancelled:
				t.Fatal("cancel returned before ScopeClosed", err)
			default:
			}
			for _, frame := range preparationFrames(t, root) {
				if frame.Method == "mcpServer/invalidate" || frame.Method == "config/mcpServer/reload" {
					t.Fatal("native invalidation preceded ScopeClosed")
				}
			}
			callerCancelled := mode == "mcp-caller-cancelled"
			if callerCancelled {
				cancelCaller()
				if err := <-cancelled; !errors.Is(err, context.Canceled) {
					t.Fatal("cancel caller could not stop waiting", err)
				}
			}
			close(release)
			if callerCancelled {
				_, err = turn.AwaitSettlement(ctx)
			} else {
				err = <-cancelled
			}
			confirmed := mode == "mcp-confirmed" || callerCancelled
			if (err == nil) != confirmed {
				t.Fatal(mode, err)
			}
			settlement, settledErr := turn.AwaitSettlement(ctx)
			if (settledErr == nil) != confirmed || settlement.Reusable != confirmed {
				t.Fatal(settlement, settledErr)
			}
			if !e.base.rpc.Alive() {
				t.Fatal("cancel released the native owner")
			}
			done := 0
			for frame := range out {
				if frame.Type == proto.TypeDone {
					done++
				}
			}
			if done != 1 {
				t.Fatal("terminal count", done)
			}
			if confirmed {
				nextOut := make(chan proto.Envelope, 20)
				next, err := e.StartTurn(ctx, "next", proto.TextInput("answer"), nextOut)
				if err != nil || !awaitExecutorTurn(t, next, nextOut).Reusable {
					t.Fatal(err)
				}
			} else if _, err := e.StartTurn(ctx, "forbidden", proto.TextInput("answer"), make(chan proto.Envelope, 20)); err == nil {
				t.Fatal("unconfirmed MCP cleanup permitted reuse")
			}
			if err := turn.Cancel(ctx); (err == nil) != confirmed {
				t.Fatal("old cancellation changed its result", err)
			}
			counts := map[string]int{}
			pids := map[int]bool{}
			for _, frame := range preparationFrames(t, root) {
				counts[frame.Method]++
				pids[frame.PID] = true
				if frame.Method == "mcpServer/invalidate" {
					var p McpServerInvalidateParams
					if json.Unmarshal(frame.Params, &p) != nil || p.ThreadID != "fixture-native-thread" || !slices.Equal(p.ServerNames, []string{"active", "late"}) || counts["turn/interrupt"] != 1 {
						t.Fatal("wrong native target", string(frame.Params))
					}
				}
			}
			wantInvalidate := 1
			if mode == "mcp-stop-failed" {
				wantInvalidate = 0
			}
			wantReload := 0
			if confirmed || mode == "mcp-reload-failed" {
				wantReload = 1
			}
			if counts["mcpServer/invalidate"] != wantInvalidate || counts["config/mcpServer/reload"] != wantReload || counts["turn/interrupt"] != 1 || counts["thread/start"] != 1 || counts["initialize"] != 1 || counts["mcpServerStatus/list"] != 0 || len(pids) != 1 {
				t.Fatal(counts, pids)
			}
		})
	}
}

func TestPreparationRequiresDeclaredMCPInvalidation(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	cfg.view.MCP = cancellationMCPBindings("local")
	cfg.view.StopMCP = func(context.Context, []string) error { return nil }
	t.Setenv("OAC_TEST_MCP_UNSUPPORTED", "1")
	_, err := testExecutor(t, "complete", req, cfg)
	if !errors.Is(err, agent.ErrUnsupportedOperation) {
		t.Fatal(err)
	}
	assertPreparationOnly(t, root)
}
