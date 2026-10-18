package pi

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// recordingPi writes the child arguments to a file before handing over to the
// test helper, so the test can assert what the adapter actually launches.
func recordingPi(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("recording launcher uses a POSIX shell")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	path := filepath.Join(dir, "pi")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nGO_WANT_PI_RPC_HELPER=1 exec " + os.Args[0] + " -test.run=^TestExecutorHelper$\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("recording pi: %v", err)
	}
	t.Setenv("OAC_RUNTIME_PI_BIN", path)
	return path, argsFile
}

func TestHarnessConfigReachesTheLaunch(t *testing.T) {
	_, argsFile := recordingPi(t)
	executor := prepareTestExecutorWith(t, map[string]any{"thinking_level": "high"})
	defer executor.Close(context.Background())
	body, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	if !strings.Contains(string(body), "--thinking\nhigh\n") {
		t.Fatalf("thinking level not applied: %q", string(body))
	}
}

func TestHarnessConfigRejectsUnknownLevel(t *testing.T) {
	factory := NewExecutorFactory(t.TempDir(), t.TempDir())
	_, err := factory(context.Background(), proto.PromptRequestPayload{
		AgentKind: "pi",
		AgentOptions: map[string]any{
			"model":          "test-model",
			"harness_config": map[string]any{"thinking_level": "none"},
			"model_provider": map[string]any{
				"protocol": "chat_completions",
				"base_url": "http://127.0.0.1:1/v1",
				"api_key":  "test-key",
			},
		},
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceRoot: t.TempDir()},
	})
	if err == nil {
		t.Fatal("expected native configuration rejection")
	}
}
