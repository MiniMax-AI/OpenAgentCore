package api

import (
	"context"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/google/uuid"
)

// Hosted and self-hosted Sessions must freeze a model provider. Tests that
// exercise other behavior supply these fixtures instead of relaxing that check.

// fixtureModelProvider returns a valid bundle for the harness.
func fixtureModelProvider(harness string) *v1.ModelProviderInput {
	if harness == "codex" {
		return &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.fixture.example/v1", APIKey: "fixture-model-key"}
	}
	return &v1.ModelProviderInput{Protocol: "anthropic", BaseURL: "https://model.fixture.example/anthropic", APIKey: "fixture-model-key", ContextWindow: 200000, MaxOutputTokens: 8000}
}

// fixtureDeploymentProvider is a deployment default for every harness, for
// fakeModelProviders.resolve.
func fixtureDeploymentProvider(_ context.Context, harness string) (*modelconfiguration.Snapshot, error) {
	return &modelconfiguration.Snapshot{Model: "fixture", Provider: fixtureModelProvider(harness), Revision: uuid.New()}, nil
}

// fixtureSessionProvider is a top-level Session request member for Codex.
const fixtureSessionProvider = `"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://model.fixture.example/v1","api_key":"fixture-model-key"}}`

// fixtureAnthropicSessionProvider is the Claude Code and MiniMax Code equivalent.
const fixtureAnthropicSessionProvider = `"x_agents_core":{"model_provider":{"protocol":"anthropic","base_url":"https://model.fixture.example/anthropic","api_key":"fixture-model-key","context_window":200000,"max_output_tokens":8000}}`
