package mcode

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/contracttest"
)

// An available runtime is execution-capable; a rejected native version is unavailable.
func TestMCodeExecutionFollowsAvailability(t *testing.T) {
	for _, tc := range []struct {
		version string
		check   error
	}{{SupportedVersion, nil}, {"0.3.11", errors.New("mcode: unsupported version 0.3.11")}} {
		t.Run(tc.version, func(t *testing.T) {
			rc := agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}
			runtime := discoverWithCheck(t.Context(), rc, Declaration.Info(), func(context.Context, string) (string, error) { return tc.version, tc.check })
			info, available := runtime.Info, tc.check == nil
			if (runtime.Executor != nil) != available || info.Capabilities.WorkspaceReadPreparation.IsSupported() {
				t.Fatalf("factories: %+v", runtime)
			}
			if info.Available != available || info.Capabilities.EnvironmentNone.IsSupported() != available || info.Capabilities.DurableInputReceipts.IsSupported() != available || info.Capabilities.SubagentObservations.IsSupported() != available {
				t.Fatalf("capabilities=%+v", info.Capabilities)
			}
			if info.Capabilities.NativeSessionRecovery.IsSupported() || info.Capabilities.LocalEnvironment.IsSupported() {
				t.Fatal("unqualified capability advertised")
			}
		})
	}
}

// Discovery narrows the declaration for the installed host and never widens it.
func TestDiscoveryOnlyNarrowsDeclaration(t *testing.T) {
	runtime := discoverWithCheck(t.Context(), agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}, Declaration.Info(), func(context.Context, string) (string, error) { return SupportedVersion, nil })
	contracttest.RequireNarrowed(t, Declaration.Capabilities, runtime.Info.Capabilities)
}
