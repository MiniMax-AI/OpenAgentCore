package api

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/google/uuid"
)

// sessionAgentDefaults reads the Session's saved Agent. A Session that
// inherits the Agent's model provider reads the opened bundle with it.
func (h *Handler) sessionAgentDefaults(ctx context.Context, tenant string, input sessionRequest) (*v1.SavedAgent, *v1.ModelProviderInput, error) {
	if input.AgentID == nil {
		return nil, nil, nil
	}
	inherit := input.XAgentsCore == nil || input.XAgentsCore.ModelProvider == nil
	var resource agents.Agent
	var provider *v1.ModelProviderInput
	var err error
	if inherit {
		resource, provider, err = h.AgentsReader.GetAgentWithModelProvider(ctx, tenant, *input.AgentID)
	} else {
		resource, err = h.AgentsReader.GetAgent(ctx, tenant, *input.AgentID)
	}
	if err != nil {
		return nil, nil, err
	}
	saved := &v1.SavedAgent{ID: resource.ID}
	if err := json.Unmarshal(resource.Configuration, &saved.SavedAgentConfiguration); err != nil {
		return nil, nil, &storedDataError{err}
	}
	if inherit && saved.XAgentsCore != nil && saved.XAgentsCore.ModelProvider != nil && provider == nil {
		return nil, nil, errors.New("saved agent model provider bundle is missing")
	}
	return saved, provider, nil
}

// modelProviderRequiredError reports a Session that would have no model
// provider. The message says what to configure.
type modelProviderRequiredError struct{ message string }

func (e *modelProviderRequiredError) Error() string { return e.message }

func modelProviderRequired(engine string) error {
	return &modelProviderRequiredError{"No model provider is configured for harness " + engine + ". Pass x_agents_core.model_provider, use an Agent that has one saved, or ask the Core administrator to set a deployment default model provider for " + engine + "."}
}

// resolveSessionExecution applies provider precedence: the Session bundle, the
// saved Agent bundle, then the deployment default. Every Environment type
// accepts every source, and every Session needs one. Bundles are never merged.
func (h *Handler) resolveSessionExecution(ctx context.Context, input sessionRequest, inherited *v1.ModelProviderInput, raw json.RawMessage) (string, *v1.ModelProviderInput, string, uuid.UUID, error) {
	engine, err := h.sessionHarness(raw)
	if err != nil {
		return "", nil, "", uuid.Nil, err
	}
	var revision uuid.UUID
	provider, source := inherited, v1.ModelProviderSourceAgent
	if extension := input.XAgentsCore; extension != nil {
		if extension.ModelProvider == nil && !input.modelProviderNull && len(extension.HarnessConfig) == 0 && len(extension.Environment) == 0 {
			return "", nil, "", uuid.Nil, errors.New("x_agents_core requires an execution option")
		}
		if extension.ModelProvider != nil {
			provider, source = extension.ModelProvider, v1.ModelProviderSourceSession
		}
	}
	// The guest daemon of a self_hosted Environment runs on the caller's
	// machine, so a deployment key must not reach it until the Harness runs on
	// an agent host.
	if provider == nil && input.Environment.Type == "self_hosted" {
		return "", nil, "", uuid.Nil, &modelProviderRequiredError{"self_hosted Sessions need a model provider for harness " + engine + ": pass x_agents_core.model_provider or use an Agent that has one saved."}
	}
	if provider == nil && input.deploymentDefaults != nil {
		provider, source, revision = input.deploymentDefaults.Provider, v1.ModelProviderSourceDeployment, input.deploymentDefaults.Revision
	}
	if provider == nil {
		return "", nil, "", uuid.Nil, modelProviderRequired(engine)
	}
	if err := provider.ValidateConfiguration(engine, input.resolvedHarnessConfig); err != nil {
		return "", nil, "", uuid.Nil, err
	}
	return engine, provider, source, revision, nil
}
