package mcode

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestMCodeExecutionOptInIsVersionBound(t *testing.T) {
	for _, tc := range []struct {
		enabled, version string
		qualified        bool
	}{{"", "0.4.12", false}, {"1", "0.3.11", false}, {"1", "0.4.12", true}} {
		t.Run(tc.enabled+"/"+tc.version, func(t *testing.T) {
			t.Setenv("OAC_RUNTIME_MCODE_AGENTS_API", tc.enabled)
			rc := agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}
			runtime := discoverWithCheck(t.Context(), rc, Declaration.Info, func(context.Context, string) (string, error) { return tc.version, nil })
			if runtime.Executor == nil || runtime.Preparation != nil || !runtime.SessionCapabilityContext || !runtime.ExecutorCapabilityContext {
				t.Fatalf("factories: %+v", runtime)
			}
			info := runtime.Info
			if !info.Available || info.Capabilities.EnvironmentNone.IsSupported() != tc.qualified || info.Capabilities.DurableInputReceipts.IsSupported() != tc.qualified || info.Capabilities.SubagentObservations.IsSupported() != tc.qualified {
				t.Fatalf("capabilities=%+v", info.Capabilities)
			}
			if info.Capabilities.NativeSessionRecovery.IsSupported() || info.Capabilities.LocalEnvironment.IsSupported() || info.Capabilities.FunctionTools.IsSupported() {
				t.Fatal("unqualified capability advertised")
			}
		})
	}
}

// The declaration must retain the complete baseline capability descriptor.
func TestDeclaredCapabilityBaseline(t *testing.T) {
	expected := map[string]bool{"Streaming": true, "Permissions": true, "Resume": true, "WorkspaceAuthoring": true}
	value := reflect.ValueOf(Declaration.Info.Capabilities)
	for i := 0; i < value.NumField(); i++ {
		name := value.Type().Field(i).Name
		want := proto.CapabilityUnsupported
		if expected[name] {
			want = proto.CapabilitySupported
		}
		if got := value.Field(i).Interface(); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if err := Declaration.Info.ValidateDeclaration(); err != nil {
		t.Fatal(err)
	}
}
