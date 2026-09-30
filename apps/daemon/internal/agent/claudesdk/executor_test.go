//go:build unix

package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/contracttest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func persistentConfig(t *testing.T, mode string) (Config, proto.PromptRequestPayload) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	entry := filepath.Join(root, "worker")
	if err := os.WriteFile(entry, []byte("version-one"), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{Node: os.Args[0], Entrypoint: entry, StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_EXECUTOR_HELPER=1", "SDK_EXECUTOR_DIR=" + root, "SDK_EXECUTOR_MODE=" + mode, "GORACE=atexit_sleep_ms=0"}}, proto.PromptRequestPayload{DisableExecutionEnvironment: true, AgentOptions: map[string]any{"model": "fixture"}}
}

func runPersistentExecutorHelper() {
	root := os.Getenv("SDK_EXECUTOR_DIR")
	if len(os.Args) > 1 && strings.HasSuffix(os.Args[1], "runtime_check.js") {
		file, _ := os.OpenFile(filepath.Join(root, "probes"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if file != nil {
			_, _ = file.WriteString("probe\n")
			_ = file.Close()
		}
		printlnReport := json.NewEncoder(os.Stdout)
		_ = printlnReport.Encode(RuntimeInfo{Type: "runtime_ready", Protocol: 3, Node: "fixture", SDK: "fixture", MCP: "fixture", Native: "fixture"})
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return
	}
	var prepare map[string]json.RawMessage
	if json.Unmarshal(scanner.Bytes(), &prepare) != nil || string(prepare["type"]) != `"executor_prepare"` || prepare["input"] != nil || prepare["run_id"] != nil {
		os.Exit(4)
	}
	_ = os.WriteFile(filepath.Join(root, "prepared"), []byte(strconv.Itoa(os.Getpid())), 0600)
	encode := func(event bridgeEvent) { _ = json.NewEncoder(os.Stdout).Encode(event) }
	encode(bridgeEvent{Type: "executor_ready", Protocol: 3})
	if os.Getenv("SDK_EXECUTOR_MODE") == "block" {
		time.Sleep(time.Hour)
		return
	}
	var active, old string
	settle := func(cancel bool) {
		encode(bridgeEvent{Type: "input_closed", TurnID: active, SessionID: "native-persistent"})
		if cancel {
			encode(bridgeEvent{Type: "error", TurnID: active, Code: "cancelled"})
		} else {
			encode(bridgeEvent{Type: "result", TurnID: active, SessionID: "native-persistent", Text: active})
		}
		confirmed, reusable, reason := true, true, ""
		switch os.Getenv("SDK_EXECUTOR_MODE") {
		case "unknown_cancel", "queued_cancel", "pending_function_unconfirmed":
			confirmed, reusable, reason = false, false, "cancellation_unconfirmed"
		case "confirmed_closed":
			reusable, reason = false, "native_closed"
		}
		event := bridgeEvent{Type: "turn_settled", TurnID: active, Confirmed: &confirmed, Reusable: &reusable, Reason: reason}
		if os.Getenv("SDK_EXECUTOR_MODE") == "missing_confirmation" {
			event.Confirmed = nil
		}
		encode(event)
		old, active = active, ""
	}
	for scanner.Scan() {
		var command struct {
			Type    string             `json:"type"`
			TurnID  string             `json:"turn_id"`
			InputID string             `json:"input_id"`
			Input   proto.MessageInput `json:"input"`
		}
		if json.Unmarshal(scanner.Bytes(), &command) != nil {
			return
		}
		switch command.Type {
		case "turn_start":
			if active != "" {
				os.Exit(5)
			}
			active = command.TurnID
			if os.Getenv("SDK_EXECUTOR_MODE") == "late" && old != "" {
				encode(bridgeEvent{Type: "delta", TurnID: old, Delta: "late"})
				continue
			}
			encode(bridgeEvent{Type: "turn_started", TurnID: active})
			encode(bridgeEvent{Type: "input_ready", TurnID: active, SessionID: "native-persistent"})
			encode(bridgeEvent{Type: "delta", TurnID: active, Delta: "partial"})
			if strings.HasPrefix(os.Getenv("SDK_EXECUTOR_MODE"), "pending_function") {
				encode(bridgeEvent{Type: "function_call", TurnID: active, Call: &proto.FunctionCallPayload{CallID: "call", Name: "lookup", Arguments: json.RawMessage("{}")}})
			}
			if len(command.Input) > 0 && len(command.Input[0].Content) > 0 && command.Input[0].Content[0].Text != nil && *command.Input[0].Content[0].Text == "wait" {
				continue
			}
			settle(false)
		case "steer":
			if os.Getenv("SDK_EXECUTOR_MODE") == "text_contract" {
				encode(bridgeEvent{Type: "input_applied", TurnID: active, InputID: command.InputID})
				continue
			}
			// A full bridge write has happened, but no native input receipt exists.
			encode(bridgeEvent{Type: "delta", TurnID: active, Delta: "steer-written"})
		case "turn_cancel":
			if active == command.TurnID {
				settle(true)
			}
		}
	}
}

func consumeExecutorTurn(t *testing.T, owner agent.Executor, id, input string) (agent.Turn, <-chan proto.Envelope) {
	t.Helper()
	out := make(chan proto.Envelope, 16)
	turn, err := owner.StartTurn(t.Context(), id, proto.TextInput(input), out)
	if err != nil || turn == nil {
		t.Fatalf("StartTurn: %v", err)
	}
	return turn, out
}
func awaitExecutorTurn(t *testing.T, turn agent.Turn, out <-chan proto.Envelope, reusable bool) {
	t.Helper()
	for event := range out {
		if event.Type == proto.TypeDone {
			var done proto.DonePayload
			_ = event.DecodePayload(&done)
			if done.Metadata[proto.DoneMetaAgentSessionID] != "native-persistent" {
				t.Fatal("native continuity missing", done)
			}
		}
	}
	settlement, err := turn.AwaitSettlement(t.Context())
	if err != nil || settlement.Reusable != reusable {
		t.Fatalf("settlement: %+v %v", settlement, err)
	}
}
func TestExecutorRetainsProcessAcrossTurnsAndCancellation(t *testing.T) {
	config, req := persistentConfig(t, "")
	req.AgentOptions["model_provider"] = map[string]any{
		"protocol": "anthropic", "base_url": "https://provider.example/anthropic", "api_key": "fixture-key",
	}
	owner, err := NewExecutorFactory(config)(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	pid := owner.(*executor).base.process.Cmd.Process.Pid
	first, out := consumeExecutorTurn(t, owner, "first", "hello")
	awaitExecutorTurn(t, first, out, true)
	next, out := consumeExecutorTurn(t, owner, "next", "wait")
	if event := <-out; event.Type != proto.TypeDelta {
		t.Fatal(event.Type)
	}
	if err := first.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := next.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitExecutorTurn(t, next, out, true)
	third, out := consumeExecutorTurn(t, owner, "third", "hello")
	awaitExecutorTurn(t, third, out, true)
	if owner.(*executor).base.process.Cmd.Process.Pid != pid {
		t.Fatal("native process was replaced")
	}
	select {
	case <-owner.(*executor).base.process.Done():
		t.Fatal("native process exited between Turns")
	default:
	}
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

}
func TestExecutorLateTurnEventInvalidatesWithoutRetargeting(t *testing.T) {
	config, req := persistentConfig(t, "late")
	owner, err := NewExecutorFactory(config)(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	first, out := consumeExecutorTurn(t, owner, "first", "hello")
	awaitExecutorTurn(t, first, out, true)
	second, out := consumeExecutorTurn(t, owner, "second", "hello")
	for frame := range out {
		if frame.Type == proto.TypeDelta {
			t.Fatal("old Turn event reached successor")
		}
	}
	if _, err := second.AwaitSettlement(t.Context()); err == nil {
		t.Fatal("unknown native outcome marked settled")
	}
	next := make(chan proto.Envelope, 1)
	if turn, err := owner.StartTurn(t.Context(), "third", proto.TextInput("hello"), next); turn != nil || err == nil {
		t.Fatal("invalid executor accepted input")
	}
}
func TestExecutorCachesReadinessUntilInstalledArtifactChanges(t *testing.T) {
	config, req := persistentConfig(t, "")
	factory := NewExecutorFactory(config)
	for range 2 {
		owner, err := factory(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if err := owner.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		raw, _ := os.ReadFile(filepath.Join(filepath.Dir(config.Entrypoint), "probes"))
		return strings.Count(string(raw), "probe\n")
	}
	if count() != 1 {
		t.Fatal("unchanged registered runtime was reprobed")
	}
	time.Sleep(time.Millisecond)
	if err := os.WriteFile(config.Entrypoint, []byte("version-two-changed"), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := factory(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	if count() != 2 {
		t.Fatal("changed installation reused stale readiness")
	}
}

func TestExecutorSeparatesPreInputRejectionFromUnknownWrite(t *testing.T) {
	config, req := persistentConfig(t, "block")
	owner, err := NewExecutorFactory(config)(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	rejected := make(chan proto.Envelope, 1)
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if turn, err := owner.StartTurn(cancelled, "not-written", proto.TextInput("hello"), rejected); turn != nil || err == nil {
		t.Fatal("cancelled admission reached native input")
	}
	select {
	case <-rejected:
		t.Fatal("nil Turn consumed caller output")
	default:
	}
	e := owner.(*executor)
	if err := e.base.process.Stdin.Close(); err != nil {
		t.Fatal(err)
	}
	out := make(chan proto.Envelope, 8)
	turn, err := owner.StartTurn(t.Context(), "unknown-write", proto.TextInput("hello"), out)
	if turn == nil || err == nil {
		t.Fatalf("attempted write must preserve unknown Turn ownership: %T %v", turn, err)
	}
	for range out {
	}
	if _, err := turn.AwaitSettlement(t.Context()); err == nil {
		t.Fatal("lost input outcome became confirmed")
	}
}

func TestExecutorCancellationDeadlineInterruptsBlockedTransport(t *testing.T) {
	config, req := persistentConfig(t, "block")
	owner, err := NewExecutorFactory(config)(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	turn, out := consumeExecutorTurn(t, owner, "blocked", "hello")
	e := owner.(*executor)
	written := make(chan error, 1)
	go func() { written <- e.write(map[string]string{"padding": strings.Repeat("x", 2*1024*1024)}) }()
	deadline := time.Now().Add(time.Second)
	for e.base.writeMu.TryLock() {
		e.base.writeMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("transport write never started")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- turn.Cancel(ctx) }()
	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("blocked cancellation invented settlement")
		}
	case <-time.After(time.Second):
		t.Fatal("Cancel ignored its transport deadline")
	}
	<-written
	for range out {
	}
}

func TestSharedTextLifecycle(t *testing.T) {
	config, req := persistentConfig(t, "text_contract")
	owner, err := NewExecutorFactory(config)(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	contracttest.TextLifecycle(t, contracttest.TextFixture{
		Executor:      owner,
		CompleteInput: proto.TextInput("hello"), ActiveInput: proto.TextInput("wait"), SteeringInput: proto.TextInput("continue waiting"),
		Ready:       func(e proto.Envelope) bool { return e.Type == proto.TypeDelta },
		NativeOwner: func() string { return strconv.Itoa(owner.(*executor).base.process.Cmd.Process.Pid) },
	})
}
