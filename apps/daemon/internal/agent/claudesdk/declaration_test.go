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
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/contracttest"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"
)

func TestClaudeSDKInvalidPathsFailBeforeProbe(t *testing.T) {
	for _, relative := range []string{"entrypoint", "home"} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OAC_RUNTIME_HOME", root)
			t.Setenv(claudeSDKEntrypointEnv, filepath.Join(root, "main.js"))
			if relative == "entrypoint" {
				t.Setenv(claudeSDKEntrypointEnv, "main.js")
			} else {
				root = "relative-home"
			}
			out := discoverWithCheck(t.Context(), agent.DiscoveryOptions{RuntimeRoot: root, StateRoot: t.TempDir(), Profile: "default", Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}, Declaration.Info(), func(context.Context, Config) (RuntimeInfo, error) {
				t.Fatal("invalid paths reached runtime probe")
				return RuntimeInfo{}, nil
			})
			if out == nil || out.Info.Available {
				t.Fatal("invalid runtime advertised as ready")
			}
		})
	}
}

func TestClaudeSDKFeatureDiscovery(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	t.Setenv(claudeSDKEntrypointEnv, filepath.Join(root, "main.js"))
	node, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(claudeSDKNodeEnv, node)
	for _, features := range [][]string{nil, {"mcp_http_tools"}, {"mcp_http_bearer_auth"}, {"mcp_http_tools", "mcp_http_bearer_auth"}, {"mcp_http_required"}, {"mcp_http_tools", "mcp_http_required"}, {"subagent_resources"}, {"structured_output"}} {
		out := discoverWithCheck(t.Context(), agent.DiscoveryOptions{RuntimeRoot: root, StateRoot: t.TempDir(), Profile: "default", Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}, Declaration.Info(), func(context.Context, Config) (RuntimeInfo, error) {
			info := RuntimeInfo{SDK: declaration.Declaration.Native.Version, Native: "native", Features: features}
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
	}
}

// Discovery narrows the declaration to the installed bundle and never widens it.
func TestDiscoveryOnlyNarrowsDeclaration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	t.Setenv(claudeSDKEntrypointEnv, filepath.Join(root, "main.js"))
	node, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(claudeSDKNodeEnv, node)
	features := []string{"message_images", "function_result_images", "tool_search", "structured_output", "subagent_resources", "mcp_http_tools", "mcp_http_bearer_auth", "mcp_http_required"}
	out := discoverWithCheck(t.Context(), agent.DiscoveryOptions{RuntimeRoot: root, StateRoot: t.TempDir(), Profile: "default", Stdout: io.Discard, Stderr: io.Discard}, Declaration.Info(), func(context.Context, Config) (RuntimeInfo, error) {
		return RuntimeInfo{SDK: declaration.Declaration.Native.Version, Native: "native", Features: features}, nil
	})
	if out == nil || !out.Info.Available {
		t.Fatal("runtime unavailable")
	}
	contracttest.RequireNarrowed(t, Declaration.Capabilities, out.Info.Capabilities)
}

func TestRuntimeDiscoveryConfigurationAndRegistration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	node, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(claudeSDKNodeEnv, node)
	entrypoint := filepath.Join(root, "bundle", "main.js")
	options := agent.DiscoveryOptions{RuntimeRoot: root, StateRoot: t.TempDir(), Profile: "test", Stdout: io.Discard, Stderr: io.Discard}
	for _, configured := range []bool{false, true} {
		for _, ready := range []bool{false, true} {
			t.Setenv(claudeSDKEntrypointEnv, "")
			if configured {
				t.Setenv(claudeSDKEntrypointEnv, entrypoint)
			}
			calls := 0
			runtime := discoverWithCheck(t.Context(), options, Declaration.Info(), func(_ context.Context, c Config) (RuntimeInfo, error) {
				calls++
				if c.Node != node || c.Entrypoint != entrypoint || c.StateDir != filepath.Join(options.StateRoot, "daemon", "test", "runtime", "claude-sdk") || c.Env != nil {
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
			if calls != 1 || runtime.Info.Available != ready || (runtime.Executor != nil) != ready || runtime.Info.Capabilities.WorkspaceReadPreparation.IsSupported() {
				t.Fatalf("runtime: %+v", runtime)
			}
			registry := agent.NewRegistry()
			registry.Register(Declaration, *runtime)
			info := registry.SupportedAgentKinds()[0]
			if info.Capabilities.Preparation.IsSupported() != ready {
				t.Fatal(info)
			}
		}
	}
}
