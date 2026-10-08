package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// createSessionFrom creates the Session a decoded request describes or replays
// the one an earlier request with the same key and intent created. A replay
// holds only the Session ID and admits nothing.
func (h *Handler) createSessionFrom(ctx context.Context, tenant string, creator identity.Subject, key string, input sessionRequest, initial []sessions.Input) (creation sessions.Creation, replayed bool, err error) {
	intent, err := sessionCreationRequest(input, initial)
	if err != nil {
		return sessions.Creation{}, false, err
	}
	if creation, err := h.SessionCreation.FindSessionCreation(ctx, tenant, key, intent, creator); !errors.Is(err, sessions.ErrNotFound) {
		return creation, true, err
	}
	command, err := h.resolveSessionCreation(ctx, tenant, key, input, initial)
	if err != nil {
		// A concurrent request with the same intent may have committed since the
		// first lookup; its Session answers this retry instead of the failure.
		if creation, findErr := h.SessionCreation.FindSessionCreation(ctx, tenant, key, intent, creator); !errors.Is(findErr, sessions.ErrNotFound) {
			return creation, true, findErr
		}
		return sessions.Creation{}, false, err
	}
	command.Creator, command.IdempotencyKey, command.CreationRequest = creator, key, intent
	create := h.SessionCreation.CreateSession
	if len(initial) > 0 || input.Environment.Type == "openai_hosted" {
		create = h.Execution.SessionAdmission.CreateSession
	}
	creation, err = create(ctx, tenant, command)
	return creation, false, err
}

// resolveSessionCreation resolves and validates the Session's configuration.
// Template, saved Agent and credential errors keep their own type; errors of
// the model, Harness and execution selection are a configurationError.
func (h *Handler) resolveSessionCreation(ctx context.Context, tenant, key string, input sessionRequest, initial []sessions.Input) (sessions.CreateSession, error) {
	if input.templateID != "" {
		template, err := h.EnvironmentTemplatesReader.Resolve(ctx, tenant, input.templateID)
		if err != nil {
			return sessions.CreateSession{}, err
		}
		if err := applyTemplateEnvironment(&input, template); err != nil {
			return sessions.CreateSession{}, err
		}
	}
	saved, inheritedProvider, err := h.sessionAgentDefaults(ctx, tenant, input)
	if err != nil {
		return sessions.CreateSession{}, err
	}
	err = h.prepareSessionModelConfiguration(ctx, &input, saved, inheritedProvider)
	var configuration json.RawMessage
	if err == nil {
		configuration, err = resolve(input, tenant, key, saved)
	}
	if err == nil {
		configuration, err = freezeSessionHarnessConfig(configuration, input.resolvedHarnessConfig)
	}
	if err != nil {
		return sessions.CreateSession{}, &configurationError{err}
	}
	if configuration, err = h.bindSessionCredentials(ctx, tenant, configuration); err != nil {
		return sessions.CreateSession{}, err
	}
	engine, provider, providerSource, deploymentRevision, err := h.resolveSessionExecution(ctx, input, inheritedProvider, configuration)
	if err != nil {
		return sessions.CreateSession{}, &configurationError{err}
	}
	if err := execution.ValidateSessionConfiguration(engine, configuration); err != nil {
		return sessions.CreateSession{}, &configurationError{fmt.Errorf("Harness %s does not support the requested Agent/environment configuration: %w", engine, err)}
	}
	executionConfiguration := sessionExecutionProjection(input, saved, inheritedProvider, provider, engine, configuration)
	return sessions.CreateSession{
		ExecutionConfiguration:     &executionConfiguration,
		ModelProvider:              provider,
		ModelProviderSource:        providerSource,
		DeploymentProviderRevision: deploymentRevision,
		InitialFiles:               input.initialFiles, Initialization: input.initialization,
		Engine: engine, Metadata: input.Metadata, Configuration: configuration, InitialInputs: initial,
	}, nil
}

// sessionCreationRequest records caller intent before mutable sources resolve:
// saved Agents, templates, credentials and deployment defaults. A retry then
// returns the committed Session even after those sources change or go away.
func sessionCreationRequest(input sessionRequest, initial []sessions.Input) (json.RawMessage, error) {
	agentID := ""
	if input.AgentID != nil {
		agentID = *input.AgentID
	}
	var environment any = input.Environment
	if input.templateID != "" || len(input.initialFiles) > 0 || !input.initialization.Empty() || (input.XAgentsCore != nil && len(input.XAgentsCore.Environment) > 0) {
		environment = input.originalEnvironment
		if len(input.originalEnvironment) == 0 {
			environment = input.templateEnvironment
		}
	}
	return json.Marshal(struct {
		Execution     *v1.SessionExecutionInput  `json:"x_agents_core,omitempty"`
		AgentID       string                     `json:"agent_id"`
		Agent         map[string]json.RawMessage `json:"agent,omitempty"`
		Environment   any                        `json:"environment"`
		Metadata      map[string]string          `json:"metadata,omitempty"`
		VaultIDs      []string                   `json:"vault_ids,omitempty"`
		InitialInputs []sessions.Input           `json:"initial_inputs,omitempty"`
	}{input.XAgentsCore, agentID, input.agentFields, environment, input.Metadata, input.VaultIDs, initial})
}
