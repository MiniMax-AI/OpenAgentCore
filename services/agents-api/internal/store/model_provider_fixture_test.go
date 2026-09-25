package store_test

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Hosted and self-hosted Sessions must freeze a model provider. HTTP fixtures
// supply one here instead of relaxing that check; the store needs a credential
// key (store.NewModelTestStore).

// fixtureDeploymentProvider configures a deployment default for every harness.
func fixtureDeploymentProvider() api.Option {
	return api.WithModelProviderDefaults(func(_ context.Context, harness string) (*v1.ModelProviderInput, error) {
		return store.FixtureModelProvider(harness), nil
	})
}

// fixtureSessionProvider is the top-level Session request member that supplies
// the harness's fixture bundle, for self-hosted Sessions.
func fixtureSessionProvider(harness string) string {
	raw, _ := json.Marshal(map[string]any{"model_provider": store.FixtureModelProvider(harness)})
	return `"x_agents_core":` + string(raw)
}
