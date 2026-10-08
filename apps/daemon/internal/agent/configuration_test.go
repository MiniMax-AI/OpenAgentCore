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
)

func TestEveryRegistryEntryPreparesTheBoundModelConfiguration(t *testing.T) {
	registry := agent.NewRegistry()
	configuration := harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: "responses"}}}
	calls := 0
	expected := errors.New("native entry reached")
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, configuration)
	registry.RegisterExecutor("fixture", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
		calls++
		return nil, expected
	})
	configuration.Providers[0].Protocol = "anthropic"
	executor, _ := registry.ResolveExecutor("fixture")
	for _, options := range []map[string]any{
		{"model": nil},
		{"model": "fixture", "model_provider": map[string]any{"protocol": "anthropic", "base_url": "https://provider.example", "api_key": "private-sentinel"}},
		{"model_provider": map[string]any{"protocol": "responses", "base_url": "https://provider.example", "api_key": "private-sentinel"}},
		{"harness_config": map[string]any{"unknown": "private-sentinel"}},
	} {
		_, err := executor(t.Context(), proto.PromptRequestPayload{AgentOptions: options})
		if err == nil || errors.Is(err, expected) || calls != 0 || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("invalid configuration reached native entry or leaked values")
		}
	}
	if _, err := executor(t.Context(), proto.PromptRequestPayload{AgentOptions: map[string]any{"model": "fixture", "model_provider": map[string]any{"protocol": "responses", "base_url": "https://provider.example", "api_key": "private-sentinel"}}}); !errors.Is(err, expected) || calls != 1 {
		t.Fatal("bound declaration was lost or mutated", err)
	}
}

func TestRegistryRejectsInvalidConfigurationDeclaration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("invalid configuration registered")
		}
	}()
	agent.NewRegistry().RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: "unknown"}}})
}
