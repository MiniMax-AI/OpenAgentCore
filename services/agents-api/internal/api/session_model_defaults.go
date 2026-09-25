package api

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// ModelProviderDefaults resolves deployment configuration at Session creation,
// before the encrypted Session snapshot is committed.
type ModelProviderDefaults func(context.Context, string, string) (*v1.ModelProviderInput, map[string]any, error)

func WithModelProviderDefaults(resolve ModelProviderDefaults) Option {
	return func(h *Handler) { h.modelProviderDefaults = resolve }
}

type agentDefaultsStore interface {
	GetAgentForSession(context.Context, string, string, bool) (store.SavedAgent, *v1.ModelProviderInput, error)
}

func (h *Handler) sessionAgentDefaults(ctx context.Context, tenant string, input sessionRequest) (*v1.SavedAgent, *v1.ModelProviderInput, error) {
	if input.AgentID == nil {
		return nil, nil, nil
	}
	if !validAgentID(*input.AgentID) {
		return nil, nil, store.ErrNotFound
	}
	inherit := input.XAgentsCore == nil || input.XAgentsCore.ModelProvider == nil
	var resource store.SavedAgent
	var provider *v1.ModelProviderInput
	var err error
	if source, ok := h.store.(agentDefaultsStore); ok {
		resource, provider, err = source.GetAgentForSession(ctx, tenant, *input.AgentID, inherit)
	} else {
		resource, err = h.lookupAgent(ctx, tenant, *input.AgentID)
	}
	if err != nil {
		return nil, nil, err
	}
	saved := &v1.SavedAgent{ID: resource.ID}
	if err := json.Unmarshal(resource.Configuration, &saved.SavedAgentConfiguration); err != nil {
		return nil, nil, err
	}
	if inherit && saved.XAgentsCore != nil && saved.XAgentsCore.ModelProvider != nil && provider == nil {
		return nil, nil, store.ErrCredentialStorageUnavailable
	}
	return saved, provider, nil
}

func (h *Handler) resolveSessionExecution(ctx context.Context, input sessionRequest, inherited *v1.ModelProviderInput, raw json.RawMessage) (string, *v1.ModelProviderInput, map[string]any, error) {
	engine, err := h.sessionHarness(raw)
	if err != nil {
		return "", nil, nil, err
	}
	provider := inherited
	var options map[string]any
	if extension := input.XAgentsCore; extension != nil {
		if extension.ModelProvider == nil && !input.modelProviderNull {
			return "", nil, nil, errors.New("x_agents_core requires an execution option")
		}
		if extension.ModelProvider != nil {
			provider = extension.ModelProvider
		}
	}
	if provider == nil && input.Environment.Type == "openai_hosted" && h.modelProviderDefaults != nil {
		var cfg configuration
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return "", nil, nil, err
		}
		provider, options, err = h.modelProviderDefaults(ctx, engine, cfg.Agent.Model)
		if err != nil {
			return "", nil, nil, errors.New("deployment model provider configuration is unavailable")
		}
	}
	if provider != nil {
		if !v1.ModelProviderEnvironmentSupported(input.Environment.Type) {
			return "", nil, nil, errors.New("caller model credentials currently require a hosted environment")
		}
		if err := provider.ValidateHarness(engine); err != nil {
			return "", nil, nil, err
		}
	}
	return engine, provider, options, nil
}
