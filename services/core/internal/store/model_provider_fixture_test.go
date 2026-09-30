package store_test

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Hosted and self-hosted Sessions must freeze a model provider. HTTP fixtures
// supply one here instead of relaxing that check; the store needs a credential
// key (store.NewModelTestStore).

// fixtureDeploymentProvider configures a deployment default for every harness.
func fixtureDeploymentProvider() func(*api.Dependencies) {
	return modelProviderDefaults(func(_ context.Context, harness string) (*modelconfiguration.Snapshot, error) {
		return &modelconfiguration.Snapshot{Model: "fixture", Provider: store.FixtureModelProvider(harness), Revision: uuid.New()}, nil
	})
}

// fixtureSessionProvider is the top-level Session request member that supplies
// the harness's fixture bundle, for self-hosted Sessions.
func fixtureSessionProvider(harness string) string {
	raw, _ := json.Marshal(map[string]any{"model_provider": store.FixtureModelProvider(harness)})
	return `"x_agents_core":` + string(raw)
}
