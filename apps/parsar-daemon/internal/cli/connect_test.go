package cli

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/mcode"
	opencodeagent "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
)

func TestScrubInlineConnectArgsRemovesTokenURLAndDeviceName(t *testing.T) {
	got := scrubInlineConnectArgs([]string{
		"oac-daemon", "connect",
		"--url", "https://core.example.com",
		"--token=rtk_secret",
		"--device-name", "dev-1",
		"-b",
		"--profile", "sandbox",
	})
	want := []string{"oac-daemon", "connect", "-b", "--profile", "sandbox"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scrubInlineConnectArgs() = %#v, want %#v", got, want)
	}
}

func TestLoadInlineConnectEnvFillsMissingValuesAndUnsets(t *testing.T) {
	t.Setenv(connectInlineURLEnv, "https://core.example.com")
	t.Setenv(connectInlineTokenEnv, "rtk_secret")
	t.Setenv(connectInlineDeviceNameEnv, "dev-1")

	serverURL, token, deviceName := "", "", ""
	loadInlineConnectEnv(&serverURL, &token, &deviceName)

	if serverURL != "https://core.example.com" || token != "rtk_secret" || deviceName != "dev-1" {
		t.Fatalf("loaded values = (%q, %q, %q)", serverURL, token, deviceName)
	}
	if got := inlineConnectEnvValue(connectInlineTokenEnv); got != "" {
		t.Fatalf("%s still set to %q", connectInlineTokenEnv, got)
	}
}

func inlineConnectEnvValue(key string) string { return os.Getenv(key) }

// Regression: pre-fork auth.json check used to run BEFORE env-to-flag
// hydration, so sandboxes passing the token via env bailed with
// "not paired". loadInlineConnectEnv now runs first.
func TestLoadInlineConnectEnvHydratesParentProcessFlags(t *testing.T) {
	t.Setenv(connectInlineURLEnv, "https://core.example.com")
	t.Setenv(connectInlineTokenEnv, "rtk_secret")

	serverURL, token, deviceName := "", "", ""

	loadInlineConnectEnv(&serverURL, &token, &deviceName)

	// Same predicate runConnect uses to decide whether to skip the
	// pre-fork auth.json check.
	inlinePair := strings.TrimSpace(serverURL) != "" || strings.TrimSpace(token) != ""
	if !inlinePair {
		t.Fatalf("inlinePair=false after env hydration; serverURL=%q token=%q", serverURL, token)
	}
}

func TestDiscoverAgentCLIsAllowsOpenCodeWithoutClaude(t *testing.T) {
	t.Setenv(claudeSDKEntrypointEnv, "")
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	got, err := discoverAgentCLIs(t.Context(), rc, "default", agentCLIChecks{
		MCode: func(context.Context, string) (string, error) { return "", mcode.ErrCLINotFound },
		ClaudeCode: func(context.Context, string) (string, error) {
			return "", claudecode.ErrCLINotFound
		},
		OpenCode: func(context.Context, string) (string, error) {
			return "opencode 1.4.3", nil
		},
		Codex: func(context.Context, string) (string, error) {
			return "", codex.ErrCLINotFound
		},
		Pi: func(context.Context, string) (string, error) {
			return "", pi.ErrCLINotFound
		},
	})
	if err != nil {
		t.Fatalf("discoverAgentCLIs: %v", err)
	}
	if got.ClaudeCode.Available {
		t.Fatalf("ClaudeCode.Available = true, want false: %#v", got.ClaudeCode)
	}
	if !got.OpenCode.Available || got.OpenCode.Version != "opencode 1.4.3" {
		t.Fatalf("OpenCode descriptor = %#v", got.OpenCode)
	}
	if got.Codex.Available {
		t.Fatalf("Codex.Available = true, want false: %#v", got.Codex)
	}
	if got.Pi.Available {
		t.Fatalf("Pi.Available = true, want false: %#v", got.Pi)
	}
	if !got.OpenCode.Capabilities.Streaming.IsSupported() || !got.OpenCode.Capabilities.Usage.IsSupported() || got.OpenCode.Capabilities.Permissions.IsSupported() {
		t.Fatalf("OpenCode capabilities = %#v", got.OpenCode.Capabilities)
	}
	if !strings.Contains(stdout.String(), "OpenCode preflight ok") {
		t.Fatalf("stdout missing OpenCode ok line: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "claude_code unavailable") {
		t.Fatalf("stderr missing Claude unavailable line: %q", stderr.String())
	}
}

func TestDiscoverAgentCLIsBothMissingFails(t *testing.T) {
	t.Setenv(claudeSDKEntrypointEnv, "")
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	got, err := discoverAgentCLIs(t.Context(), rc, "default", agentCLIChecks{
		MCode: func(context.Context, string) (string, error) { return "", mcode.ErrCLINotFound },
		ClaudeCode: func(context.Context, string) (string, error) {
			return "", claudecode.ErrCLINotFound
		},
		OpenCode: func(context.Context, string) (string, error) {
			return "", opencodeagent.ErrCLINotFound
		},
		Codex: func(context.Context, string) (string, error) {
			return "", codex.ErrCLINotFound
		},
		Pi: func(context.Context, string) (string, error) {
			return "", pi.ErrCLINotFound
		},
	})
	if err == nil {
		t.Fatalf("expected error when all CLIs missing, got descriptors %#v", got)
	}
	if !strings.Contains(err.Error(), "no supported agent CLI") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ClaudeCode.Available || got.OpenCode.Available || got.Codex.Available || got.Pi.Available {
		t.Fatalf("available descriptors after missing CLIs: %#v", got)
	}
}

func TestDiscoverAgentCLIsBothAvailable(t *testing.T) {
	t.Setenv(claudeSDKEntrypointEnv, "")
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	got, err := discoverAgentCLIs(t.Context(), rc, "default", agentCLIChecks{
		MCode: func(context.Context, string) (string, error) { return "0.4.12", nil },
		ClaudeCode: func(context.Context, string) (string, error) {
			return "claude 2.0.0", nil
		},
		OpenCode: func(context.Context, string) (string, error) {
			return "opencode 1.4.3", nil
		},
		Codex: func(context.Context, string) (string, error) {
			return "codex 0.141.0", nil
		},
		Pi: func(context.Context, string) (string, error) {
			return "pi 0.1.0", nil
		},
	})
	if err != nil {
		t.Fatalf("discoverAgentCLIs: %v", err)
	}
	if !got.ClaudeCode.Available || got.ClaudeCode.Version != "claude 2.0.0" {
		t.Fatalf("ClaudeCode descriptor = %#v", got.ClaudeCode)
	}
	if !got.OpenCode.Available || got.OpenCode.Version != "opencode 1.4.3" {
		t.Fatalf("OpenCode descriptor = %#v", got.OpenCode)
	}
	if !got.Codex.Available || got.Codex.Version != "codex 0.141.0" {
		t.Fatalf("Codex descriptor = %#v", got.Codex)
	}
	if !got.ClaudeCode.Capabilities.Permissions.IsSupported() || !got.ClaudeCode.Capabilities.Resume.IsSupported() {
		t.Fatalf("ClaudeCode capabilities = %#v", got.ClaudeCode.Capabilities)
	}
	if got.Codex.Capabilities.ExecutionControls.IsSupported() != codex.SupportsTextVerbosity || got.ClaudeCode.Capabilities.ExecutionControls.IsSupported() || got.OpenCode.Capabilities.ExecutionControls.IsSupported() || !got.Codex.Capabilities.ToolObservations.IsSupported() || !got.Codex.Capabilities.SubagentControl.IsSupported() || got.Codex.Capabilities.TextVerbosity.IsSupported() != codex.SupportsTextVerbosity || !got.Codex.Capabilities.WebSearchControl.IsSupported() || !got.Codex.Capabilities.EnvironmentNone.IsSupported() || !got.Codex.Capabilities.MessageItems.IsSupported() || !got.Codex.Capabilities.Streaming.IsSupported() || !got.Codex.Capabilities.Permissions.IsSupported() || !got.Codex.Capabilities.Resume.IsSupported() {
		t.Fatalf("Codex capabilities = %#v (want Streaming+Permissions+Resume)", got.Codex.Capabilities)
	}
	if !got.Pi.Available || got.Pi.Version != "pi 0.1.0" {
		t.Fatalf("Pi descriptor = %#v", got.Pi)
	}
	if !got.Pi.Capabilities.Streaming.IsSupported() || !got.Pi.Capabilities.Usage.IsSupported() || !got.Pi.Capabilities.Resume.IsSupported() || got.Pi.Capabilities.Permissions.IsSupported() {
		t.Fatalf("Pi capabilities = %#v (want Streaming+Usage+Resume, no Permissions)", got.Pi.Capabilities)
	}
	if !got.MCode.Available || !got.MCode.Capabilities.Resume.IsSupported() || got.MCode.Capabilities.Usage.IsSupported() {
		t.Fatalf("mcode descriptor = %#v", got.MCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRegisterAgentKindsPreservesDescriptors(t *testing.T) {
	reg := agent.NewRegistry()
	registerAgentKinds(reg, agentCLIDiscovery{
		MCode: proto.SupportedAgentKind{Kind: "mcode", Available: true, Version: "0.4.12", Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{Streaming: proto.CapabilitySupported, Permissions: proto.CapabilitySupported, Resume: proto.CapabilitySupported})},
		ClaudeCode: proto.SupportedAgentKind{
			Kind:      "claude_code",
			Available: true,
			Version:   "claude 2.0.0",
			Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
				Streaming:   proto.CapabilitySupported,
				Permissions: proto.CapabilitySupported,
				Usage:       proto.CapabilitySupported,
				Resume:      proto.CapabilitySupported,
			}),
		},
		OpenCode: proto.SupportedAgentKind{
			Kind:      "opencode",
			Available: false,
			Version:   "missing",
			Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
				Streaming: proto.CapabilitySupported,
				Usage:     proto.CapabilitySupported,
			}),
		},
		Codex: proto.SupportedAgentKind{
			Kind:      "codex",
			Available: true,
			Version:   "codex 0.141.0",
			Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
				Streaming:   proto.CapabilitySupported,
				Permissions: proto.CapabilitySupported,
				Usage:       proto.CapabilitySupported,
				Resume:      proto.CapabilitySupported,
			}),
		},
		Pi: proto.SupportedAgentKind{
			Kind:      "pi",
			Available: true,
			Version:   "pi 0.1.0",
			Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
				Streaming: proto.CapabilitySupported,
				Usage:     proto.CapabilitySupported,
				Resume:    proto.CapabilitySupported,
			}),
		},
	}, "https://core.example.test")

	kinds := reg.SupportedAgentKinds()
	if len(kinds) != 5 {
		t.Fatalf("SupportedAgentKinds len = %d, want 5: %#v", len(kinds), kinds)
	}
	// Sorted: claude_code, codex, mcode, opencode, pi.
	if kinds[0].Kind != "claude_code" || kinds[1].Kind != "codex" || kinds[2].Kind != "mcode" || kinds[3].Kind != "opencode" || kinds[4].Kind != "pi" {
		t.Fatalf("SupportedAgentKinds sort = %#v", kinds)
	}
	if !kinds[0].Available || kinds[0].Version != "claude 2.0.0" || !kinds[0].Capabilities.Permissions.IsSupported() {
		t.Fatalf("claude descriptor not preserved: %#v", kinds[0])
	}
	if !kinds[1].Available || kinds[1].Version != "codex 0.141.0" || !kinds[1].Capabilities.Resume.IsSupported() {
		t.Fatalf("codex descriptor not preserved: %#v", kinds[1])
	}
	if kinds[3].Available || kinds[3].Version != "missing" || !kinds[3].Capabilities.Streaming.IsSupported() || !kinds[3].Capabilities.Usage.IsSupported() {
		t.Fatalf("opencode descriptor not preserved: %#v", kinds[3])
	}
	if !kinds[4].Available || kinds[4].Version != "pi 0.1.0" || !kinds[4].Capabilities.Resume.IsSupported() || kinds[4].Capabilities.Permissions.IsSupported() {
		t.Fatalf("pi descriptor not preserved: %#v", kinds[4])
	}
	if _, err := reg.Resolve("opencode"); err != nil {
		t.Fatalf("opencode factory not registered: %v", err)
	}
	if _, err := reg.Resolve("codex"); err != nil {
		t.Fatalf("codex factory not registered: %v", err)
	}
	if _, err := reg.Resolve("pi"); err != nil {
		t.Fatalf("pi factory not registered: %v", err)
	}
}
