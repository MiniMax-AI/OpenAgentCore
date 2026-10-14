package codex

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// A model host that does not resolve must fail the Turn with
// connection_failed instead of retrying forever. Opt-in: it needs the pinned
// native binary but no credentials or network.
func TestExecutorNativeUnreachableModelHost(t *testing.T) {
	binary := os.Getenv("OAC_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("requires the pinned native Codex binary")
	}
	if version, err := exec.Command(binary, "--version").Output(); err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.4" {
		t.Fatal("native artifact is not Codex 0.153.4")
	}
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	cfg := defaultSessionConfig(testStateRoot(t))
	cfg.codexBinary = binary
	cfg.logger = slog.New(slog.DiscardHandler)
	req := proto.PromptRequestPayload{
		AgentKind: "codex", AgentStateKey: "unreachable-model-host",
		DisableExecutionEnvironment: true, DisableSubagents: true,
		AgentOptions: map[string]any{"model": "gpt-5.5", "model_provider": map[string]any{"base_url": "https://model-host.invalid/v1", "protocol": "responses", "api_key": "unused"}},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	e, err := newExecutor(ctx, req, cfg)
	if err != nil {
		t.Fatalf("native prepare failed: %v", err)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_ = e.Close(c)
	})
	output := make(chan proto.Envelope, 64)
	began := time.Now()
	turn, err := e.StartTurn(ctx, "unreachable", proto.TextInput("Reply OK."), output)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	var failure proto.ErrorPayload
	for done := false; !done; {
		select {
		case event := <-output:
			t.Logf("+%s %s", time.Since(began).Round(time.Millisecond), event.Type)
			if event.Type == proto.TypeError {
				_ = event.DecodePayload(&failure)
			}
			done = event.Type == proto.TypeDone
		case <-ctx.Done():
			t.Fatalf("Turn did not settle within %s", time.Since(began).Round(time.Second))
		}
	}
	_, _ = turn.AwaitSettlement(ctx)
	t.Logf("failed after %s with %q", time.Since(began).Round(time.Millisecond), failure.Code)
	if failure.Code != "connection_failed" {
		t.Fatalf("failure code = %q, want connection_failed", failure.Code)
	}
}
