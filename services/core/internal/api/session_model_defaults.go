package api

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func (h *Handler) sessionAgentDefaults(ctx context.Context, tenant string, input sessionRequest) (*v1.SavedAgent, *v1.ModelProviderInput, error) {
	if input.AgentID == nil {
		return nil, nil, nil
	}
	if !validAgentID(*input.AgentID) {
		return nil, nil, store.ErrNotFound
	}
	inherit := input.XAgentsCore == nil || input.XAgentsCore.ModelProvider == nil
	resource, provider, err := h.Agents.GetAgentForSession(ctx, tenant, *input.AgentID, inherit)
	if err != nil {
		return nil, nil, err
	}
	saved := &v1.SavedAgent{ID: resource.ID}
	if err := json.Unmarshal(resource.Configuration, &saved.SavedAgentConfiguration); err != nil {
		return nil, nil, err
	}
	if inherit && saved.XAgentsCore != nil && saved.XAgentsCore.ModelProvider != nil && provider == nil {
		return nil, nil, credentialcrypto.ErrUnavailable
	}
	return saved, provider, nil
}

// modelProviderRequiredError reports a hosted or self-hosted Session that
// would have no model provider. The message says what to configure.
type modelProviderRequiredError struct{ message string }

func (e *modelProviderRequiredError) Error() string { return e.message }

// modelProviderDefaultsError carries a storage or decryption failure while
// reading the deployment default; it is reported as a service error.
type modelProviderDefaultsError struct{ err error }

func (e *modelProviderDefaultsError) Error() string { return e.err.Error() }
func (e *modelProviderDefaultsError) Unwrap() error { return e.err }

func modelProviderRequired(environment, engine string) error {
	if environment == "self_hosted" {
		return &modelProviderRequiredError{"self_hosted Sessions need a model provider for harness " + engine + ": pass x_agents_core.model_provider or use an Agent that has one saved. Deployment default model providers apply to openai_hosted and none Sessions, never to self_hosted."}
	}
	return &modelProviderRequiredError{"No model provider is configured for harness " + engine + ". Pass x_agents_core.model_provider, use an Agent that has one saved, or ask the Core administrator to set a deployment default model provider for " + engine + "."}
}

// resolveSessionExecution applies provider precedence: the Session bundle, the
// saved Agent bundle, then the deployment default where the environment allows
// it. Bundles are never merged.
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
	environment := input.Environment.Type
	if provider == nil && v1.ModelProviderAllowed(environment, v1.ModelProviderSourceDeployment) {
		snapshot := input.deploymentDefaults
		if snapshot != nil {
			provider, revision = snapshot.Provider, snapshot.Revision
		}
		source = v1.ModelProviderSourceDeployment
	}
	if provider == nil {
		if v1.ModelProviderRequired(environment) {
			return "", nil, "", uuid.Nil, modelProviderRequired(environment, engine)
		}
		return engine, nil, "", uuid.Nil, nil
	}
	if !v1.ModelProviderAllowed(environment, source) {
		return "", nil, "", uuid.Nil, errors.New("caller model credentials require an openai_hosted or self_hosted environment")
	}
	if err := provider.ValidateConfiguration(engine, input.resolvedHarnessConfig); err != nil {
		return "", nil, "", uuid.Nil, err
	}
	return engine, provider, source, revision, nil
}
