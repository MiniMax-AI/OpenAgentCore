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

// Every registered factory prepares the bound model configuration before
// native code.
func TestEveryRegistryEntryPreparesTheBoundModelConfiguration(t *testing.T) {
	registry := agent.NewRegistry()
	configuration := prototest.ModelConfiguration()
	calls := 0
	var prepared harnessconfig.PreparedConfiguration
	expected := errors.New("native entry reached")
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, configuration)
	registry.RegisterExecutor("fixture", func(_ context.Context, req agent.PrepareRequest) (agent.Executor, error) {
		calls++
		prepared = req.Prepared
		return nil, expected
	})
	configuration.Providers[0].Protocol = "anthropic"
	executor, _ := registry.ResolveExecutor("fixture")
	responses := &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "https://provider.example", APIKey: "private-sentinel"}
	for _, req := range []proto.PromptRequestPayload{
		{Model: "fixture", ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://provider.example", APIKey: "private-sentinel"}},
		{ModelProvider: responses},
		{Model: "fixture"},
		{HarnessConfig: proto.HarnessConfig(`{"unknown":"private-sentinel"}`)},
	} {
		_, err := executor(t.Context(), agent.PrepareRequest{PromptRequestPayload: req})
		if err == nil || errors.Is(err, expected) || calls != 0 || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("invalid configuration reached native entry or leaked values")
		}
	}
	if _, err := executor(t.Context(), agent.PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{Model: "fixture", ModelProvider: responses}}); !errors.Is(err, expected) || calls != 1 {
		t.Fatal("bound declaration was lost or mutated", err)
	}
	if prepared.Model != "fixture" || prepared.Provider != *responses {
		t.Fatalf("the factory received %+v, not the prepared configuration", prepared)
	}
}

func TestRegistryRejectsInvalidConfigurationDeclaration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("invalid configuration registered")
		}
	}()
	configuration := prototest.ModelConfiguration()
	configuration.Providers[0].Protocol = "unknown"
	agent.NewRegistry().RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, configuration)
}
