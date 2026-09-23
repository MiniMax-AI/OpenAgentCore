package main

import (
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
)

func coreStartupConfiguration(defaultHarness string, enabledHarnesses []string, daemonGateway bool, endpoints map[string]bool, managedProviderKind string, managed *execution.RuntimeProvider) v1.CoreStartupConfiguration {
	if !daemonGateway {
		enabledHarnesses = []string{}
		managed = nil
	}
	modelProviders := make([]v1.CoreHarnessModelProviderConfiguration, 0, len(enabledHarnesses))
	for _, kind := range enabledHarnesses {
		modelProviders = append(modelProviders, v1.CoreHarnessModelProviderConfiguration{Harness: kind, EndpointConfigured: endpoints[kind]})
	}
	managedConfiguration := v1.CoreManagedSandboxConfiguration{}
	if managed != nil && managedProviderKind != "" {
		provider := managedProviderKind
		managedConfiguration = v1.CoreManagedSandboxConfiguration{Enabled: true, Provider: &provider, Maintenance: managed.Maintenance}
	}
	result := v1.CoreStartupConfiguration{
		Object: "agents.core.startup_configuration", SchemaVersion: 1,
		Supported: v1.CoreSupportedConfiguration{
			Harnesses: (engine.Catalog{}).Kinds(), ManagedSandboxProviders: []string{"docker", "microsandbox"},
		},
		Configured: v1.CoreConfiguredStartupConfiguration{
			DefaultHarness: defaultHarness, EnabledHarnesses: enabledHarnesses, DaemonGateway: daemonGateway, SelfHosted: daemonGateway,
			ManagedSandbox: managedConfiguration, ModelProviders: modelProviders,
		},
	}
	result.ConfigurationCapabilities = configurationCapabilities(engine.Catalog{}, builtin.Registry(), result.Configured)
	return result
}
