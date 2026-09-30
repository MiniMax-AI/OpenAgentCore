package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/claudesdk"
)

func TestNativeInstallationDiscoversAndRegistersOnlySelectedHarness(t *testing.T) {
	for _, name := range []string{"codex", "claude", "minimax"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OAC_RUNTIME_HOME", root)
			t.Setenv(claudeSDKEntrypointEnv, filepath.Join(root, "host-claude", "main.js"))
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(claudeSDKNodeEnv, binary)
			called := make(map[string]bool)
			check := func(kind, version string) func(context.Context, string) (string, error) {
				return func(context.Context, string) (string, error) { called[kind] = true; return version, nil }
			}
			checks := agentCLIChecks{
				ClaudeCode: check("claude_code", "2.1.269"),
				OpenCode:   check("opencode", "1.0.0"),
				Codex:      check("codex", "codex-cli 0.153.4"),
				Pi:         check("pi", "0.1.0"),
				MCode:      check("mcode", "0.4.12"),
				ClaudeSDK: func(context.Context, claudesdk.Config) (claudesdk.RuntimeInfo, error) {
					called["claude_sdk"] = true
					return claudesdk.RuntimeInfo{SDK: "0.3.269"}, nil
				},
			}
			kinds := nativeInstallationKinds([]string{name})
			rc := &runContext{stdout: io.Discard, stderr: io.Discard, installedKinds: kinds}
			path := os.Getenv("PATH")
			discovery, err := discoverAgentCLIs(t.Context(), rc, "default", checks)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(called, kinds) {
				t.Fatalf("probed unselected host Harness: got %v, want %v", called, kinds)
			}
			registry := agent.NewRegistry()
			registerAgentKinds(registry, discovery, "")
			expected := []string{nativeHarnesses[name].AgentKind}
			if !reflect.DeepEqual(registry.Kinds(), expected) {
				t.Fatalf("registered unselected host Harness: %v", registry.Kinds())
			}
			if os.Getenv("PATH") != path {
				t.Fatal("installation discovery removed the tool PATH")
			}
		})
	}
}
