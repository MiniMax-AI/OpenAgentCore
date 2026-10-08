package codex

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

func TestMCPRequiredDiscoveryRequiresPinnedNative(t *testing.T) {
	for _, version := range []string{"codex-cli 0.153.4", "codex-cli 0.153.3", "codex-cli 0.154.0"} {
		runtime := discoverWithCheck(t.Context(), agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}, Declaration.Info, func(context.Context, string) (string, error) { return version, nil })

		if !runtime.Info.Available {
			t.Fatalf("runtime: %+v", runtime)
		}
		if runtime.Info.Capabilities.MCPHTTPRequired.IsSupported() != (version == "codex-cli 0.153.4") {
			t.Fatal("unverified native combination advertised")
		}
		if runtime.Info.Capabilities.NativeSessionRecovery.IsSupported() != (version == "codex-cli 0.153.4") {
			t.Fatal("unverified native recovery advertised")
		}
	}
}

func TestUnavailableRuntimeHasNoView(t *testing.T) {
	runtime := discoverWithCheck(t.Context(), agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}, Declaration.Info, func(context.Context, string) (string, error) { return "", errors.New("missing") })
	if runtime.Info.Available || runtime.View != nil {
		t.Fatalf("unavailable runtime: %+v", runtime)
	}
}
