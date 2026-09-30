package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"
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
	registry.RegisterPreparation("fixture", true, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
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
	preparation, _ := registry.ResolvePreparation("fixture")
	entries := []func(proto.PromptRequestPayload) error{
		func(req proto.PromptRequestPayload) error { _, err := factory(t.Context(), req, nil); return err },
		func(req proto.PromptRequestPayload) error { _, err := executor(t.Context(), req); return err },
		func(req proto.PromptRequestPayload) error { _, err := preparation(t.Context(), req); return err },
	}
	for _, entry := range entries {
		for _, options := range []map[string]any{
			{"model": nil},
			{"model": "fixture", "model_provider": map[string]any{"protocol": "anthropic", "base_url": "https://provider.example", "api_key": "private-sentinel"}},
			{"model_provider": map[string]any{"protocol": "responses", "base_url": "https://provider.example", "api_key": "private-sentinel"}},
			{"harness_config": map[string]any{"unknown": "private-sentinel"}},
		} {
			before := calls
			err := entry(proto.PromptRequestPayload{AgentOptions: options})
			if err == nil || errors.Is(err, expected) || calls != before || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatal("invalid configuration reached native entry or leaked values")
			}
		}
		if err := entry(proto.PromptRequestPayload{AgentOptions: map[string]any{"model": "fixture", "model_provider": map[string]any{"protocol": "responses", "base_url": "https://provider.example", "api_key": "private-sentinel"}}}); !errors.Is(err, expected) {
			t.Fatal("bound declaration was lost or mutated", err)
		}
	}
	if calls != 3 {
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
