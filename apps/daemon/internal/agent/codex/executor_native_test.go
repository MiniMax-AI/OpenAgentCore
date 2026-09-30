package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// This opt-in test uses the real pinned harness and an explicitly configured
// Responses provider. Credentials are read from a private file, never arguments.
func TestExecutorNativeReuse(t *testing.T) {
	if os.Getenv("OAC_TEST_CODEX_EXECUTOR_NATIVE") != "1" {
		t.Skip("requires an authorized native model acceptance environment")
	}
	binary, root := os.Getenv("OAC_TEST_CODEX_BINARY"), os.Getenv("OAC_TEST_CODEX_LIVE_ROOT")
	model, endpoint := os.Getenv("OAC_TEST_CODEX_MODEL"), os.Getenv("OAC_TEST_CODEX_BASE_URL")
	if binary == "" || root == "" || model == "" || endpoint == "" {
		t.Fatal("missing explicit native acceptance configuration")
	}
	version, err := exec.Command(binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.4" {
		t.Fatal("native artifact is not Codex 0.153.4")
	}
	key, err := os.ReadFile(os.Getenv("OAC_TEST_CODEX_KEY_FILE"))
	if err != nil || strings.TrimSpace(string(key)) == "" {
		t.Fatal("private provider credential unavailable")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal("cannot create isolated acceptance root")
	}
	isolated, err := os.MkdirTemp(root, "run-")
	if err != nil {
		t.Fatal("cannot create isolated acceptance workspace")
	}
	t.Cleanup(func() { _ = os.RemoveAll(isolated) })
	t.Setenv("OAC_RUNTIME_HOME", isolated)
	for _, name := range []string{"CODEX_EXEC_SERVER_URL", "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL", "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID", "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN"} {
		t.Setenv(name, "")
	}
	cfg := defaultSessionConfig()
	cfg.codexBinary = binary
	cfg.logger = obslog.Discard()
	req := proto.PromptRequestPayload{
		AgentKind: "codex", AgentStateKey: "executor-native",
		StrictResume: true, DisableExecutionEnvironment: true, DisableSubagents: true, ObserveMessages: true,
		AgentOptions:  map[string]any{"model": model, "model_provider": map[string]any{"base_url": endpoint, "protocol": "responses", "api_key": strings.TrimSpace(string(key))}},
		FunctionTools: []proto.FunctionTool{{Name: "hold", Description: "Wait until the host supplies a result.", Parameters: json.RawMessage("{\"type\":\"object\",\"properties\":{},\"additionalProperties\":false}")}},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	began := time.Now()
	e, err := newExecutor(ctx, req, cfg)
	if err != nil {
		t.Fatalf("native prepare failed (%T)", err)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := e.Close(c); err != nil {
			t.Errorf("native owner cleanup failed (%T)", err)
		}
	})
	t.Logf("native_version=0.153.4 prepare_ms=%d", time.Since(began).Milliseconds())
	pid := e.prepared.session.rpc.cmd.Process.Pid
	var thread string
	run := func(id, prompt string, old agent.Turn, interrupt bool) (agent.Turn, string) {
		t.Helper()
		output := make(chan proto.Envelope, 512)
		started := time.Now()
		turn, err := e.StartTurn(ctx, id, proto.TextInput(prompt), output)
		if err != nil {
			t.Fatalf("%s start failed (%T)", id, err)
		}
		if old != nil {
			if err := old.Cancel(ctx); err != nil {
				t.Fatalf("%s stale cancellation failed", id)
			}
		}
		done := make(chan struct{})
		called := make(chan struct{}, 1)
		var text string
		var terminal int
		var first time.Duration
		go func() {
			defer close(done)
			for event := range output {
				if first == 0 {
					first = time.Since(started)
				}
				if event.Type == proto.TypeFunctionCall {
					select {
					case called <- struct{}{}:
					default:
					}
				}
				if event.Type == proto.TypeDone {
					var result proto.DonePayload
					_ = event.DecodePayload(&result)
					text = result.Content
					terminal++
				}
			}
		}()
		if interrupt {
			select {
			case <-called:
			case <-done:
				t.Fatal("native cancellation did not reach its function")
			case <-ctx.Done():
				t.Fatal("native function admission timed out")
			}
			cancelStarted := time.Now()
			if err := turn.Cancel(ctx); err != nil {
				t.Fatalf("native interrupt failed (%T)", err)
			}
			t.Logf("turn=%s cancel_settled_ms=%d", id, time.Since(cancelStarted).Milliseconds())
		}
		settlement, err := turn.AwaitSettlement(ctx)
		if err != nil || !settlement.Reusable {
			t.Fatalf("%s native settlement not reusable (error=%t reason=%s)", id, err != nil, settlement.Reason)
		}
		<-done
		if terminal != 1 {
			t.Fatalf("%s emitted %d terminals", id, terminal)
		}
		s := turn.(*Session)
		if thread == "" {
			thread = s.currentThreadID()
		}
		if thread == "" || s.currentThreadID() != thread || !s.rpc.Alive() || s.rpc.cmd.Process.Pid != pid {
			t.Fatal("native owner/thread changed")
		}
		t.Logf("turn=%s first_event_ms=%d settled_ms=%d same_process=true same_thread=true", id, first.Milliseconds(), time.Since(started).Milliseconds())
		return turn, text
	}
	first, _ := run("cold", "Remember the exact code CEDAR-4729 for this conversation. Reply only SAVED. Do not call tools.", nil, false)
	_, answer := run("warm", "What is the exact code I asked you to remember? Reply only the code. Do not call tools.", nil, false)
	if strings.TrimSpace(answer) != "CEDAR-4729" {
		t.Fatal("warm native Turn did not retain conversation memory")
	}
	cancelled, _ := run("cancel", "Call the hold function now with an empty object. Wait for its result before responding.", nil, true)
	_, answer = run("followup", "What is the exact code I asked you to remember? Reply only the code. Do not call tools.", cancelled, false)
	if strings.TrimSpace(answer) != "CEDAR-4729" {
		t.Fatal("post-cancellation native Turn lost conversation memory")
	}
	if err := first.Cancel(ctx); err != nil {
		t.Fatal("retired first Turn cancellation failed")
	}
	if err := e.Close(ctx); err != nil {
		t.Fatal("native final cleanup failed")
	}
	if e.prepared.session.rpc.Alive() {
		t.Fatal("native child remained alive after owner close")
	}
}
