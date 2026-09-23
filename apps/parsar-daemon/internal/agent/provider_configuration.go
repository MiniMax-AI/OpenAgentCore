package agent

import (
	"cmp"
	"slices"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"
)

func providerConfigurationDescriptor(registry harnessconfig.Registry, kind string) *proto.AgentProviderConfiguration {
	configuration, known := registry.Lookup(kind)
	if !known || len(configuration.Providers) == 0 {
		return nil
	}
	descriptor := &proto.AgentProviderConfiguration{SchemaVersion: 1, Providers: make([]proto.AgentProviderProtocol, 0, len(configuration.Providers))}
	for _, provider := range configuration.Providers {
		descriptor.Providers = append(descriptor.Providers, proto.AgentProviderProtocol{
			Protocol: provider.Protocol, RequiresTokenLimits: provider.RequiresTokenLimits,
		})
	}
	slices.SortFunc(descriptor.Providers, func(a, b proto.AgentProviderProtocol) int { return cmp.Compare(a.Protocol, b.Protocol) })
	return descriptor
}
