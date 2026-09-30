package codex

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestMCPRequiredDiscoveryRequiresPinnedNative(t *testing.T) {
	for _, version := range []string{"codex-cli 0.153.4", "codex-cli 0.153.3", "codex-cli 0.154.0"} {
		runtime := discoverWithCheck(t.Context(), agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}, Declaration.Info, func(context.Context, string) (string, error) { return version, nil })

		if !runtime.Info.Available || runtime.Executor == nil || (runtime.Preparation != nil) != SupportsLocalEnvironment(version) || !runtime.SessionCapabilityContext || !runtime.ExecutorCapabilityContext {
			t.Fatalf("factories: %+v", runtime)
		}
		if runtime.Info.Capabilities.MCPHTTPRequired.IsSupported() != (version == "codex-cli 0.153.4") {
			t.Fatal("unverified native combination advertised")
		}
		if runtime.Info.Capabilities.NativeSessionRecovery.IsSupported() != (version == "codex-cli 0.153.4") {
			t.Fatal("unverified native recovery advertised")
		}
	}
}

// The declaration must retain the complete baseline capability descriptor.
func TestDeclaredCapabilityBaseline(t *testing.T) {
	expected := map[string]bool{"SubagentObservations": true, "Streaming": true, "Permissions": true, "Usage": true, "Resume": true, "Steering": true, "MessageItems": true, "ToolObservations": true, "EnvironmentNone": true, "ProgrammaticToolCallingDisable": true, "WebSearchControl": true, "MessageImages": true, "FunctionResultImages": true, "SubagentControl": true, "DurableInputReceipts": true, "DurableTurns": true, "FunctionTools": true, "MCPHTTPTools": true, "MCPHTTPBearerAuth": true, "WorkspaceAuthoring": true}
	expected["ExecutionControls"], expected["TextVerbosity"] = SupportsTextVerbosity, SupportsTextVerbosity
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

func TestUnavailableRuntimeHasNoExecutionFactories(t *testing.T) {
	runtime := discoverWithCheck(t.Context(), agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}, Declaration.Info, func(context.Context, string) (string, error) { return "", errors.New("missing") })
	if runtime.Info.Available || runtime.Executor != nil || runtime.Preparation != nil || runtime.Session == nil {
		t.Fatalf("unavailable runtime: %+v", runtime)
	}
}
