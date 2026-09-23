package main

import (
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	sandboxconfig "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/config"
)

func TestCoreStartupConfigurationSeparatesSupportAndConfiguration(t *testing.T) {
	managed := &execution.RuntimeProvider{Maintenance: true}
	got := coreStartupConfiguration("codex", []string{"claude_sdk", "codex"}, true, map[string]bool{"codex": true}, "docker", managed)
	if got.Object != "agents.core.startup_configuration" || got.SchemaVersion != 1 || got.Configured.DefaultHarness != "codex" || !got.Configured.DaemonGateway || !got.Configured.SelfHosted {
		t.Fatalf("unexpected configuration: %#v", got)
	}
	if !reflect.DeepEqual(got.Supported.Harnesses, []string{"claude_sdk", "codex", "mcode"}) || !reflect.DeepEqual(got.Supported.ManagedSandboxProviders, []string{"docker", "microsandbox"}) {
		t.Fatalf("unexpected supported surface: %#v", got.Supported)
	}
	if got.Configured.ManagedSandbox.Provider == nil || *got.Configured.ManagedSandbox.Provider != "docker" || !got.Configured.ManagedSandbox.Enabled || !got.Configured.ManagedSandbox.Maintenance {
		t.Fatalf("unexpected managed sandbox: %#v", got.Configured.ManagedSandbox)
	}
	if len(got.Configured.ModelProviders) != 2 || got.Configured.ModelProviders[0].EndpointConfigured || !got.Configured.ModelProviders[1].EndpointConfigured {
		t.Fatalf("unexpected provider projection: %#v", got.Configured.ModelProviders)
	}
}

func TestCoreStartupConfigurationWithoutExecution(t *testing.T) {
	managed := &execution.RuntimeProvider{Maintenance: true}
	got := coreStartupConfiguration("codex", []string{"claude_sdk", "codex"}, false, map[string]bool{"codex": true}, "docker", managed)
	if got.Configured.DaemonGateway || got.Configured.SelfHosted || got.Configured.EnabledHarnesses == nil || len(got.Configured.EnabledHarnesses) != 0 || got.Configured.ManagedSandbox.Enabled || got.Configured.ManagedSandbox.Provider != nil || got.Configured.ManagedSandbox.Maintenance || len(got.Configured.ModelProviders) != 0 {
		t.Fatalf("unconfigured execution was reported: %#v", got.Configured)
	}
}

func TestCoreStartupConfigurationReportsMicrosandboxSelection(t *testing.T) {
	managed := &execution.RuntimeProvider{}
	got := coreStartupConfiguration("mcode", []string{"mcode"}, true, map[string]bool{"mcode": true}, "microsandbox", managed)
	if got.Configured.ManagedSandbox.Provider == nil || *got.Configured.ManagedSandbox.Provider != "microsandbox" || !got.Configured.ManagedSandbox.Enabled {
		t.Fatalf("unexpected managed sandbox: %#v", got.Configured.ManagedSandbox)
	}
	if len(got.Configured.ModelProviders) != 1 || !got.Configured.ModelProviders[0].EndpointConfigured {
		t.Fatalf("unexpected model endpoint projection: %#v", got.Configured.ModelProviders)
	}
}

func TestCoreStartupConfigurationReportsRemoteOnlyProvider(t *testing.T) {
	for _, kind := range []string{"docker", "microsandbox"} {
		managed := runtimeFromConfig(sandboxconfig.Config{Provider: kind, Maintenance: true}, &sandboxconfig.Built{})
		got := coreStartupConfiguration("codex", []string{"codex"}, true, nil, managedRuntimeProviderKind(managed), managed)
		if got.Configured.ManagedSandbox.Provider == nil || *got.Configured.ManagedSandbox.Provider != kind || !got.Configured.ManagedSandbox.Enabled || !got.Configured.ManagedSandbox.Maintenance {
			t.Fatalf("remote-only provider configuration lost: %#v", got.Configured.ManagedSandbox)
		}
	}
}
