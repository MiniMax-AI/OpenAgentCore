package mcode

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// An available runtime is execution-capable; a rejected native version is unavailable.
func TestMCodeExecutionFollowsAvailability(t *testing.T) {
	for _, tc := range []struct {
		version string
		check   error
	}{{SupportedVersion, nil}, {"0.3.11", errors.New("mcode: unsupported version 0.3.11")}} {
		t.Run(tc.version, func(t *testing.T) {
			rc := agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}
			runtime := discoverWithCheck(t.Context(), rc, Declaration.Info, func(context.Context, string) (string, error) { return tc.version, tc.check })
			info, available := runtime.Info, tc.check == nil
			if (runtime.Executor != nil) != available {
				t.Fatalf("factories: %+v", runtime)
			}
			if info.Available != available || info.Capabilities.EnvironmentNone.IsSupported() != available || info.Capabilities.SubagentObservations.IsSupported() != available {
				t.Fatalf("capabilities=%+v", info.Capabilities)
			}
			if info.Capabilities.NativeSessionRecovery.IsSupported() || info.Capabilities.LocalEnvironment.IsSupported() || info.Capabilities.FunctionTools.IsSupported() {
				t.Fatal("unqualified capability advertised")
			}
		})
	}
}

// The static declaration supports nothing until discovery finds the CLI.
func TestDeclaredCapabilityBaseline(t *testing.T) {
	value := reflect.ValueOf(Declaration.Info.Capabilities)
	for i := 0; i < value.NumField(); i++ {
		if got := value.Field(i).Interface(); got != proto.CapabilityUnsupported {
			t.Errorf("%s = %v, want unsupported", value.Type().Field(i).Name, got)
		}
	}
	if err := Declaration.Info.ValidateDeclaration(); err != nil {
		t.Fatal(err)
	}
}
