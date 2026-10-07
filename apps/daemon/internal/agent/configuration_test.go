package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestEveryRegistryEntryPreparesTheBoundModelConfiguration(t *testing.T) {
	registry := agent.NewRegistry()
	configuration := harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: "responses"}}}
	calls := 0
	expected := errors.New("native entry reached")
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, configuration, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		calls++
		return nil, expected
	})
	registry.RegisterExecutor("fixture", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
		calls++
		return nil, expected
	})
	configuration.Providers[0].Protocol = "anthropic"
	copy, err := registry.Configuration("fixture")
	if err != nil {
		t.Fatal(err)
	}
	copy.Providers[0].Protocol = "anthropic"
	factory, _ := registry.Resolve("fixture")
	executor, _ := registry.ResolveExecutor("fixture")
	entries := []func(proto.PromptRequestPayload) error{
		func(req proto.PromptRequestPayload) error { _, err := factory(t.Context(), req, nil); return err },
		func(req proto.PromptRequestPayload) error { _, err := executor(t.Context(), req); return err },
	}
	for _, entry := range entries {
		responses := &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "https://provider.example", APIKey: "private-sentinel"}
		for _, req := range []proto.PromptRequestPayload{
			{Model: "fixture", ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://provider.example", APIKey: "private-sentinel"}},
			{ModelProvider: responses},
			{Model: "fixture"},
			{HarnessConfig: proto.HarnessConfig(`{"unknown":"private-sentinel"}`)},
		} {
			before := calls
			err := entry(req)
			if err == nil || errors.Is(err, expected) || calls != before || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatal("invalid configuration reached native entry or leaked values")
			}
		}
		if err := entry(proto.PromptRequestPayload{Model: "fixture", ModelProvider: responses}); !errors.Is(err, expected) {
			t.Fatal("bound declaration was lost or mutated", err)
		}
	}
	if calls != 2 {
		t.Fatal("unexpected native calls", calls)
	}
	if _, err := registry.Configuration("missing"); err == nil {
		t.Fatal("undeclared configuration inferred")
	}
}

func TestRegistryRejectsInvalidConfigurationDeclaration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("invalid configuration registered")
		}
	}()
	agent.NewRegistry().RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: "unknown"}}}, stubFactory("fixture"))
}
