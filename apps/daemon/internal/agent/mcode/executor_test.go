package mcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func executorFixture(t *testing.T, scenario string, workspace bool) (*executor, string) {
	t.Helper()
	config, req, record := workspaceFixture(t)
	req.ReleaseOnCompletion = false
	script, err := os.ReadFile(config.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(config.Binary, []byte(strings.Replace(string(script), "HELPER=prepared", "HELPER="+scenario, 1)), 0700); err != nil {
		t.Fatal(err)
	}
	var factory agent.ExecutorFactory
	if workspace {
		factory = NewExecutorFactory(&config)
	} else {
		req = executionRequest(t)
		req.ReleaseOnCompletion = false
		req.RunID, req.Input, req.ConversationID = "", nil, ""
		t.Setenv("OAC_RUNTIME_MCODE_BIN", config.Binary)
		factory = NewExecutorFactory(nil)
	}
	value, err := factory(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	e := value.(*executor)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	return e, record
}

func runExecutorFixtureTurn(t *testing.T, e *executor, id, text string) (*Session, []proto.Envelope) {
	t.Helper()
	out := make(chan proto.Envelope, 256)
	turn, err := e.StartTurn(t.Context(), id, proto.TextInput(text), out)
	if err != nil {
		t.Fatal(err)
	}
	var events []proto.Envelope
	for event := range out {
		events = append(events, event)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	settlement, err := turn.AwaitSettlement(ctx)
	if err != nil || !settlement.Reusable {
		t.Fatalf("settlement = %+v, %v", settlement, err)
	}
	return turn.(*Session), events
}

func TestExecutorReusesNativeOwnerWithFreshTurnsAndIdleDrain(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(map[bool]string{false: "none", true: "workspace"}[workspace], func(t *testing.T) {
			e, record := executorFixture(t, "executor-reuse", workspace)
			pid := e.connection.process.Cmd.Process.Pid
			first, _ := runExecutorFixtureTurn(t, e, "first", "one")
			second, events := runExecutorFixtureTurn(t, e, "second", "two")
			if first == second || e.connection.process.Cmd.Process.Pid != pid {
				t.Fatal("Turn reused mutable state or rebuilt the process")
			}
			text, tools, done := "", 0, 0
			for _, event := range events {
				if event.ID != "second" {
					t.Fatal("event escaped its Turn", event.ID)
				}
				switch event.Type {
				case proto.TypeError:
					t.Fatalf("execution failed: %s", event.Payload)
				case proto.TypeDelta:
					var delta proto.DeltaPayload
					_ = json.Unmarshal(event.Payload, &delta)
					text += delta.Delta
					if delta.Sequence != 1 {
						t.Fatal("sequence carried over from old Turn")
					}
				case proto.TypeToolCall:
					tools++
				case proto.TypeDone:
					done++
				}
			}
			if text != "two" || tools != 2 || done != 1 {
				t.Fatalf("cross-Turn state: text=%q tools=%d done=%d", text, tools, done)
			}
			raw, _ := os.ReadFile(record)
			if strings.Count(string(raw), "initialize\n") != 1 || strings.Count(string(raw), "session/new\n") != 1 || strings.Count(string(raw), "session/prompt\n") != 2 {
				t.Fatalf("native owner was recreated: %s", raw)
			}
			select {
			case <-e.connection.exited:
				t.Fatal("completion killed native owner")
			default:
			}
		})
	}
}

func TestExecutorCancellationRetiresOwnerAndLateCancelCannotRetarget(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "single-agent", false: "subagents"}[disabled], func(t *testing.T) {
			e, record := executorFixture(t, "executor-cancel", true)
			first, _ := runExecutorFixtureTurn(t, e, "first", "one")
			e.req.DisableSubagents = disabled
			if !disabled {
				// A terminal native history record still cannot prove Bash cleanup.
				dir := t.TempDir()
				node, bridge := filepath.Join(dir, "node"), filepath.Join(dir, "bridge.mjs")
				body := "#!/bin/sh\nprintf '%s\\n' '{\"version\":1,\"complete\":true,\"rootSessionId\":\"native-1\",\"sessions\":[{\"id\":\"native-1\",\"turns\":[{\"id\":\"root-turn\",\"status\":\"aborted\"}]}]}'\n"
				for name, data := range map[string]string{node: body, bridge: "", filepath.Join(dir, "subagent-snapshot.mjs"): ""} {
					if err := os.WriteFile(name, []byte(data), 0700); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("OAC_RUNTIME_MCODE_NODE", node)
				t.Setenv("OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE", bridge)
			}
			out := make(chan proto.Envelope, 32)
			second, err := e.StartTurn(t.Context(), "second", proto.TextInput("wait"), out)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-out:
				if event.Type != proto.TypeDelta {
					t.Fatal(event.Type)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("second Turn did not start")
			}
			if err = first.Cancel(t.Context()); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(record)
			if strings.Contains(string(raw), "session/cancel") {
				t.Fatal("late cancellation targeted the new Turn")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if err = second.Cancel(ctx); err != nil {
				t.Fatal("stopped nonreusable owner reported cancellation failure", err)
			}
			settlement, err := second.AwaitSettlement(ctx)
			if err != nil || settlement.Reusable {
				t.Fatal(settlement, err)
			}
			select {
			case <-e.connection.exited:
			default:
				t.Fatal("nonreusable settlement preceded native process exit")
			}
			if got := second.CancellationOutcome(); got.Content != "ready" || got.Metadata[proto.DoneMetaAgentSessionID] != "native-1" {
				t.Fatal("cancel lost settled outcome", got)
			}
			for range out {
			}
			thirdOut := make(chan proto.Envelope, 1)
			third, err := e.StartTurn(t.Context(), "third", proto.TextInput("three"), thirdOut)
			if third != nil || err == nil {
				t.Fatal("unproven native owner admitted a successor")
			}
			close(thirdOut)
			raw, _ = os.ReadFile(record)
			if strings.Count(string(raw), "session/cancel\n") != 1 || strings.Count(string(raw), "initialize\n") != 1 {
				t.Fatalf("cancellation replaced owner: %s", raw)
			}
		})
	}
}

func TestExecutorStartFailureOwnership(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		e, record := executorFixture(t, "executor-reuse", true)
		out := make(chan proto.Envelope, 1)
		turn, err := e.StartTurn(t.Context(), "", proto.TextInput("invalid"), out)
		if err == nil || turn != nil {
			t.Fatal("invalid Start acquired output")
		}
		close(out)
		raw, _ := os.ReadFile(record)
		if strings.Contains(string(raw), "session/prompt") {
			t.Fatal("invalid input was sent")
		}
		runExecutorFixtureTurn(t, e, "healthy", "valid")
	})
	t.Run("before input", func(t *testing.T) {
		e, record := executorFixture(t, "executor-preexit", true)
		out := make(chan proto.Envelope, 1)
		turn, err := e.StartTurn(t.Context(), "run", proto.TextInput("input"), out)
		if err == nil || turn != nil {
			t.Fatal("failed readiness retained caller output")
		}
		close(out)
		raw, _ := os.ReadFile(record)
		if strings.Contains(string(raw), "session/prompt") {
			t.Fatal("pre-input failure sent input")
		}
	})
	t.Run("after submission", func(t *testing.T) {
		e, record := executorFixture(t, "executor-exit", true)
		out := make(chan proto.Envelope, 32)
		turn, err := e.StartTurn(t.Context(), "run", proto.TextInput("input"), out)
		if err != nil || turn == nil {
			t.Fatal("submitted Turn lost ownership", err)
		}
		for range out {
		}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		settlement, err := turn.AwaitSettlement(ctx)
		if err == nil || settlement.Reusable {
			t.Fatal("native exit lost its settlement error", settlement, err)
		}
		raw, _ := os.ReadFile(record)
		if strings.Count(string(raw), "session/prompt\n") != 1 {
			t.Fatal("uncertain input was replayed")
		}
		rejected := make(chan proto.Envelope)
		again, err := e.StartTurn(t.Context(), "again", proto.TextInput("again"), rejected)
		if again != nil || err == nil {
			t.Fatal("invalid owner accepted another Turn")
		}
		close(rejected)
	})
}

func TestExecutorCloseRetainsOwnerAfterDeadline(t *testing.T) {
	e, _ := executorFixture(t, "executor-reuse", true)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_ = e.Close(cancelled)
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	if err := e.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.connection.exited:
	default:
		t.Fatal("Close lost native cleanup ownership")
	}
}

func TestExecutorFactoryPreparationFailureHasNoTypedNilOwner(t *testing.T) {
	config, req, _ := workspaceFixture(t)
	req.ReleaseOnCompletion = false
	if err := os.WriteFile(config.Binary, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	value, err := NewExecutorFactory(&config)(t.Context(), req)
	if err == nil || value != nil {
		t.Fatalf("settled preparation failure returned owner: nil=%t error=%v", value == nil, err)
	}
}
