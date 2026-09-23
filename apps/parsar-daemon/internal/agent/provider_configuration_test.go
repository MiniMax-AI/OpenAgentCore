package agent_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig/builtin"
)

func TestRegisteredAdaptersShareProviderDeclarations(t *testing.T) {
	registry := agent.NewRegistry()
	for _, kind := range builtin.Registry().Kinds() {
		registry.RegisterKind(proto.SupportedAgentKind{Kind: kind, Available: false, Version: "fixture"}, stubFactory(kind))
	}
	for _, kind := range registry.SupportedAgentKinds() {
		declaration, _ := builtin.Registry().Lookup(kind.Kind)
		descriptor := kind.ProviderConfiguration
		if kind.Available || descriptor == nil || descriptor.SchemaVersion != 1 || len(descriptor.Providers) != len(declaration.Providers) {
			t.Fatalf("declaration changed readiness or was omitted: %#v", kind)
		}
		for _, provider := range descriptor.Providers {
			rule, ok := declaration.Provider(provider.Protocol)
			if !ok || rule.RequiresTokenLimits != provider.RequiresTokenLimits {
				t.Fatalf("adapter registration disagrees with Core admission: %#v", provider)
			}
		}
	}
}

func TestProviderDescriptorIsSafeDefensiveAndWireCompatible(t *testing.T) {
	configuration := harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: "fixture", RequiresTokenLimits: true}}}
	declarations := harnessconfig.NewRegistry(map[string]harnessconfig.Configuration{"additional-adapter": configuration})
	registry := agent.NewRegistryWithConfigurations(declarations)
	registry.RegisterKind(proto.SupportedAgentKind{
		Kind: "additional-adapter", Available: true,
		ProviderConfiguration: &proto.AgentProviderConfiguration{SchemaVersion: 99, Providers: []proto.AgentProviderProtocol{{Protocol: "private-canary"}}},
	}, stubFactory("additional-adapter"))
	configuration.Providers[0].Protocol = "private-canary"
	first := registry.SupportedAgentKinds()
	first[0].ProviderConfiguration.Providers[0].Protocol = "private-canary"
	first[0].ProviderConfiguration.SchemaVersion = 99
	next := registry.SupportedAgentKinds()
	if next[0].ProviderConfiguration.SchemaVersion != 1 || next[0].ProviderConfiguration.Providers[0].Protocol != "fixture" {
		t.Fatal("caller mutated the adapter descriptor")
	}
	envelope, err := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: next})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(envelope.Payload), "private-canary") {
		t.Fatal("unregistered configuration leaked")
	}
	var roundTrip proto.HeartbeatPayload
	if err := envelope.DecodePayload(&roundTrip); err != nil || !reflect.DeepEqual(roundTrip.SupportedAgentKinds, next) {
		t.Fatalf("descriptor round trip failed: %v", err)
	}
	// The existing envelope decoder ignores added JSON fields; a peer built
	// before this optional descriptor therefore still accepts the heartbeat.
	var oldPeer struct {
		Kinds []struct {
			Kind         string                      `json:"kind"`
			Available    bool                        `json:"available"`
			Capabilities proto.AgentKindCapabilities `json:"capabilities"`
		} `json:"supported_agent_kinds"`
	}
	if err := envelope.DecodePayload(&oldPeer); err != nil || len(oldPeer.Kinds) != 1 || oldPeer.Kinds[0].Kind != "additional-adapter" || !oldPeer.Kinds[0].Available {
		t.Fatalf("older peer rejected the optional declaration: %v", err)
	}
	var oldDescriptor proto.SupportedAgentKind
	if json.Unmarshal([]byte(`{"kind":"additional-adapter","available":true}`), &oldDescriptor) != nil || oldDescriptor.ProviderConfiguration != nil {
		t.Fatal("missing declaration must remain unknown")
	}
	registry.Register("undeclared", stubFactory("undeclared"))
	for _, kind := range registry.SupportedAgentKinds() {
		if kind.Kind == "undeclared" && kind.ProviderConfiguration != nil {
			t.Fatal("unknown adapter inherited support")
		}
	}
}
