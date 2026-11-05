package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/google/uuid"
)

// sessionPlan is a Session configuration that passed every check Session create
// runs before it creates anything.
type sessionPlan struct {
	saved                       *v1.SavedAgent
	inheritedProvider, provider *v1.ModelProviderInput
	source                      v1.ExecutionSource
	revision                    uuid.UUID
	engine                      string
	configuration               json.RawMessage
}

// planSession resolves the environment template and the saved Agent, freezes
// the configuration, binds its credentials and checks it against the Harness
// and the model provider. Deployment readiness, admission and leases are not
// part of it. On an error, report writes the response for the stage that failed.
func (h *Handler) planSession(ctx context.Context, tenant, key string, input *sessionRequest) (p sessionPlan, report func(http.ResponseWriter, *http.Request, error), err error) {
	if input.templateID != "" {
		template, err := h.EnvironmentTemplatesReader.Resolve(ctx, tenant, input.templateID)
		if err != nil {
			return p, writeEnvironmentTemplatesError, err
		}
		if err := applyTemplateEnvironment(input, template); err != nil {
			return p, func(w http.ResponseWriter, r *http.Request, err error) {
				if !writeFieldError(w, err) {
					writeSessionsError(w, r, err)
				}
			}, err
		}
	}
	if p.saved, p.inheritedProvider, err = h.sessionAgentDefaults(ctx, tenant, *input); err != nil {
		return p, writeAgentsError, err
	}
	err = h.prepareSessionModelConfiguration(ctx, input, p.saved, p.inheritedProvider)
	if err == nil {
		p.configuration, err = resolve(*input, tenant, key, p.saved)
	}
	if err == nil {
		p.configuration, err = freezeSessionHarnessConfig(p.configuration, input.resolvedHarnessConfig)
	}
	if err == nil {
		if p.configuration, err = h.bindSessionCredentials(ctx, tenant, p.configuration); err != nil {
			return p, writeVaultsError, err
		}
		p.engine, p.provider, p.source, p.revision, err = h.resolveSessionExecution(ctx, *input, p.inheritedProvider, p.configuration)
	}
	if err == nil {
		if invalid := h.Policy.ValidateSessionConfiguration(p.engine, p.configuration); invalid != nil {
			err = fmt.Errorf("Harness %s does not support the requested Agent/environment configuration: %w", p.engine, invalid)
		}
	}
	if err == nil {
		err = validateSessionModelConfiguration(p.engine, p.provider, p.configuration)
	}
	return p, writeSessionConfigurationError, err
}

func writeSessionConfigurationError(w http.ResponseWriter, r *http.Request, err error) {
	var required *modelProviderRequiredError
	switch {
	case errors.As(err, &required):
		writeError(w, http.StatusBadRequest, "model_provider_required", required.message, "x_agents_core.model_provider")
	case writeStoredDataError(w, r, err):
	case !writeFieldError(w, err):
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
	}
}
