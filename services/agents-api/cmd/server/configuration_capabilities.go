package main

import (
	"slices"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
)

// configurationCapabilities projects only provider compatibility facts. Runtime
// heartbeats and operation admission remain authoritative for actual execution.
func configurationCapabilities(catalog engine.Catalog, registry harnessconfig.Registry, configured v1.CoreConfiguredStartupConfiguration) *v1.CoreConfigurationCapabilities {
	result := &v1.CoreConfigurationCapabilities{
		SchemaVersion: 1, Scope: "core_build_provider_configuration", RuntimeAvailability: "unknown",
		Admission: v1.ModelProviderConfigurationAdmission(), Harnesses: []v1.CoreHarnessConfigurationSupport{},
	}
	kinds := append(catalog.Kinds(), registry.Kinds()...)
	slices.Sort(kinds)
	for _, kind := range slices.Compact(kinds) {
		entry := v1.CoreHarnessConfigurationSupport{
			Harness: kind, Support: "unknown", Enabled: slices.Contains(configured.EnabledHarnesses, kind),
			Default: configured.DefaultHarness == kind, Providers: []v1.CoreProviderProtocolSupport{},
		}
		if declaration, ok := registry.Lookup(kind); ok && len(declaration.Providers) != 0 {
			entry.Support = "supported"
			for _, provider := range declaration.Providers {
				view := v1.CoreProviderProtocolSupport{
					Protocol: provider.Protocol, RequiredFields: []string{"protocol", "base_url", "api_key"}, PositiveFields: []string{},
				}
				if provider.RequiresTokenLimits {
					view.RequiredFields = append(view.RequiredFields, "context_window", "max_output_tokens")
					view.PositiveFields = []string{"context_window", "max_output_tokens"}
				}
				entry.Providers = append(entry.Providers, view)
			}
			slices.SortFunc(entry.Providers, func(a, b v1.CoreProviderProtocolSupport) int {
				if a.Protocol < b.Protocol {
					return -1
				}
				if a.Protocol > b.Protocol {
					return 1
				}
				return 0
			})
		}
		result.Harnesses = append(result.Harnesses, entry)
	}
	return result
}
