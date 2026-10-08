package integration

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
)

// Every Session must freeze a model provider. HTTP fixtures supply one here
// instead of relaxing that check.

// fixtureDeploymentProvider configures a deployment default for every harness.
func fixtureDeploymentProvider() func(*api.Dependencies) {
	return modelProviderDefaults(func(_ context.Context, harness string) (*modelconfiguration.Snapshot, error) {
		return &modelconfiguration.Snapshot{Model: "fixture", Provider: FixtureModelProvider(harness), Revision: uuid.New()}, nil
	})
}

// fixtureSessionProvider is the top-level Session request member that supplies
// the harness's fixture bundle.
func fixtureSessionProvider(harness string) string {
	raw, _ := json.Marshal(map[string]any{"model_provider": FixtureModelProvider(harness)})
	return `"x_agents_core":` + string(raw)
}
