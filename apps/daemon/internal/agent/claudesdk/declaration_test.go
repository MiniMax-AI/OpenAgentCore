package claudesdk

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

func TestClaudeSDKRelativeEntrypointFailsBeforeProbe(t *testing.T) {
	t.Setenv(claudeSDKEntrypointEnv, "main.js")
	out := discoverWithCheck(t.Context(), agent.DiscoveryOptions{Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}, Declaration.Info, func(context.Context, Config) (RuntimeInfo, error) {
		t.Fatal("invalid paths reached runtime probe")
		return RuntimeInfo{}, nil
	})
	if out == nil || out.Info.Available {
		t.Fatal("invalid runtime advertised as ready")
	}
}

func TestClaudeSDKFeatureDiscovery(t *testing.T) {
	t.Setenv(claudeSDKEntrypointEnv, filepath.Join(t.TempDir(), "main.js"))
	node, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(claudeSDKNodeEnv, node)
	for _, features := range [][]string{nil, {"mcp_http_tools"}, {"mcp_http_bearer_auth"}, {"mcp_http_tools", "mcp_http_bearer_auth"}, {"mcp_http_required"}, {"mcp_http_tools", "mcp_http_required"}, {"subagent_resources"}, {"structured_output"}} {
		out := discoverWithCheck(t.Context(), agent.DiscoveryOptions{Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}, Declaration.Info, func(context.Context, Config) (RuntimeInfo, error) {
			// The bundle's workspace variants, which the declaration uses.
			workspace := []string{"workspace_tools", "workspace_prepare", "workspace_command_observations", "local_runtime_v2", "workspace_mcp_http", "workspace_structured_output"}
			info := RuntimeInfo{SDK: "0.3.269", Native: "2.1.269 (Claude Code)", Features: append(slices.Clone(features), workspace...)}
			return info, nil
		})
		supported := len(features) > 0 && features[0] == "mcp_http_tools"
		if out == nil || !out.Info.Available || out.Info.Capabilities.MCPHTTPTools.IsSupported() != supported || out.Info.Capabilities.MCPHTTPBearerAuth.IsSupported() != (supported && slices.Contains(features, "mcp_http_bearer_auth")) || out.Info.Capabilities.MCPHTTPRequired.IsSupported() != (supported && slices.Contains(features, "mcp_http_required")) {
			t.Fatal("MCP feature discovery widened the runtime profile")
		}
		if out.Info.Capabilities.StructuredOutput.IsSupported() != slices.Contains(features, "structured_output") {
			t.Fatal("structured output feature does not match the installed runtime")
		}
		if out.Info.Capabilities.SubagentObservations.IsSupported() != slices.Contains(features, "subagent_resources") {
			t.Fatal("Subagent feature discovery does not match the runtime contract")
		}
		if out.Info.Capabilities.NativeSessionRecovery.IsSupported() {
			t.Fatal("discovery claimed native session recovery")
		}
	}
}

func TestRuntimeDiscoveryConfigurationAndRegistration(t *testing.T) {
	node, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(claudeSDKNodeEnv, node)
	entrypoint := filepath.Join(t.TempDir(), "bundle", "main.js")
	options := agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}
	for _, configured := range []bool{false, true} {
		for _, ready := range []bool{false, true} {
			t.Setenv(claudeSDKEntrypointEnv, "")
			if configured {
				t.Setenv(claudeSDKEntrypointEnv, entrypoint)
			}
			calls := 0
			runtime := discoverWithCheck(t.Context(), options, Declaration.Info, func(_ context.Context, c Config) (RuntimeInfo, error) {
				calls++
				if c.Node != node || c.Entrypoint != entrypoint || c.Env != nil {
					t.Fatalf("configuration: %+v", c)
				}
				if !ready {
					return RuntimeInfo{}, errors.New("readiness failed")
				}
				return RuntimeInfo{SDK: "test-sdk", Native: "test-native"}, nil
			})
			if !configured {
				if runtime != nil || calls != 0 {
					t.Fatal("unconfigured runtime probed")
				}
				continue
			}
			// The probe reports no native binary, so no view is declared.
			if calls != 1 || runtime.Info.Available != ready || runtime.View != nil || ready && runtime.Info.Capabilities.LocalEnvironment.IsSupported() {
				t.Fatalf("runtime: %+v", runtime)
			}
		}
	}
}
