package api

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Resolve mutable defaults once, before constructing the immutable Agent. Model
// or provider replacements discard inherited native parameters unless supplied.
func (h *Handler) prepareSessionModelConfiguration(ctx context.Context, input *sessionRequest, saved *v1.SavedAgent, inherited *v1.ModelProviderInput) error {
	var extension *v1.AgentsCore
	if saved != nil && saved.XAgentsCore != nil {
		extension = &v1.AgentsCore{Harness: saved.XAgentsCore.Harness}
	}
	if _, supplied := input.agentFields["x_agents_core"]; supplied {
		extension = nil
		if input.Agent != nil {
			extension = input.Agent.XAgentsCore
			if extension != nil && extension.Harness == "" && saved != nil && saved.XAgentsCore != nil {
				copy := *extension
				copy.Harness = saved.XAgentsCore.Harness
				extension = &copy
			}
		}
	}
	var inline, session json.RawMessage
	if input.Agent != nil && input.Agent.XAgentsCore != nil {
		inline = input.Agent.XAgentsCore.HarnessConfig
	}
	if input.XAgentsCore != nil {
		session = input.XAgentsCore.HarnessConfig
	}
	harness := ""
	if extension != nil {
		harness = extension.Harness
	}
	for _, native := range []json.RawMessage{inline, session} {
		if errors.Is(v1.ValidateHarnessConfig(harness, native), harnessconfig.ErrHarnessRequired) {
			return &fieldError{param: "agent.x_agents_core.harness", message: harnessRequiredMessage}
		}
	}
	selected, _ := json.Marshal(configuration{Agent: v1.Agent{XAgentsCore: extension}})
	engine, err := h.sessionHarness(selected)
	if err != nil {
		return err
	}
	explicitModel := input.Agent != nil && input.Agent.Model != nil
	explicitProvider := input.XAgentsCore != nil && input.XAgentsCore.ModelProvider != nil
	needsModel := !explicitModel && saved == nil
	if (inherited == nil && !explicitProvider) || needsModel {
		input.deploymentDefaults, err = h.ModelProviders.Resolve(ctx, engine)
		if err != nil {
			var configurationError *v1.ModelProviderError
			if errors.As(err, &configurationError) {
				return configurationError
			}
			return &storedDataError{err}
		}
	}
	input.modelSource = "session"
	if !explicitModel && saved != nil {
		input.modelSource = "agent"
	}
	if needsModel && input.deploymentDefaults != nil {
		if input.Agent == nil {
			input.Agent = &v1.InlineAgent{}
		} else {
			copied := *input.Agent
			input.Agent = &copied
		}
		model := input.deploymentDefaults.Model
		input.Agent.Model = &model
		input.modelSource = "deployment"
	}
	raw := json.RawMessage(`{}`)
	source := "unknown"
	supplied := inline
	if len(session) > 0 {
		supplied = session
	}
	if len(supplied) > 0 {
		raw, source = supplied, "session"
	} else if explicitModel || explicitProvider {
		source = "session"
	} else if saved != nil {
		source = "agent"
		// Changing the harness also discards the former adapter's parameters.
		if _, overridden := input.agentFields["x_agents_core"]; !overridden && saved.XAgentsCore != nil {
			raw = v1.ResolvedHarnessConfig(saved.XAgentsCore.HarnessConfig)
		}
	} else if input.deploymentDefaults != nil {
		raw, source = v1.ResolvedHarnessConfig(input.deploymentDefaults.HarnessConfig), "deployment"
	}
	model := ""
	if input.Agent != nil && input.Agent.Model != nil {
		model = *input.Agent.Model
	} else if saved != nil {
		model = saved.Model
	}
	if err := v1.ValidateNativeModelConfiguration(engine, model, raw); err != nil {
		return err
	}
	input.resolvedHarnessConfig, input.harnessConfigSource = raw, source
	return nil
}

func freezeSessionHarnessConfig(raw json.RawMessage, native json.RawMessage) (json.RawMessage, error) {
	var cfg configuration
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg.Agent.XAgentsCore == nil && string(native) == "{}" {
		return raw, nil
	}
	if cfg.Agent.XAgentsCore == nil {
		cfg.Agent.XAgentsCore = &v1.AgentsCore{}
	}
	cfg.Agent.XAgentsCore.HarnessConfig = v1.ResolvedHarnessConfig(native)
	return json.Marshal(cfg)
}

func validateSessionModelConfiguration(engine string, provider *v1.ModelProviderInput, raw json.RawMessage) error {
	var cfg configuration
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	var native json.RawMessage
	if cfg.Agent.XAgentsCore != nil {
		native = cfg.Agent.XAgentsCore.HarnessConfig
	}
	return provider.ValidateConfiguration(engine, native)
}
