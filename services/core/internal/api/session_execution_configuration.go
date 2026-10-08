package api

import (
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/go-chi/chi/v5"
)

// Record selection sources at resolution time. Null Agent extensions reset the
// harness to deployment defaults but do not clear inherited provider bundles.
func sessionExecutionProjection(input sessionRequest, saved *v1.SavedAgent, inherited, provider *v1.ModelProviderInput, engine string, raw json.RawMessage) v1.SessionExecutionConfiguration {
	var configuration struct {
		Agent struct {
			Model string         `json:"model"`
			Core  *v1.AgentsCore `json:"x_agents_core"`
		} `json:"agent"`
	}
	_ = json.Unmarshal(raw, &configuration) // The resolved configuration was already validated.
	harnessSource := v1.ExecutionSourceDeployment
	if _, overridden := input.agentFields["x_agents_core"]; overridden {
		if input.Agent != nil && input.Agent.XAgentsCore != nil && input.Agent.XAgentsCore.Harness != "" {
			harnessSource = v1.ExecutionSourceSession
		} else if input.Agent != nil && input.Agent.XAgentsCore != nil && saved != nil && saved.XAgentsCore != nil && saved.XAgentsCore.Harness != "" {
			harnessSource = v1.ExecutionSourceAgent
		}
	} else if saved != nil && saved.XAgentsCore != nil && saved.XAgentsCore.Harness != "" {
		harnessSource = v1.ExecutionSourceAgent
	}
	// Deployment defaults are readable with the same Core key, so new Sessions
	// record their safe view too; historical rows stay redacted.
	selection := v1.ExecutionProviderSelection{Source: v1.ExecutionSourceDeployment, Status: v1.ExecutionProviderAvailable, Configuration: provider.SafeView()}
	if input.XAgentsCore != nil && input.XAgentsCore.ModelProvider != nil {
		selection.Source = v1.ExecutionSourceSession
	} else if inherited != nil {
		selection.Source = v1.ExecutionSourceAgent
	}
	native := json.RawMessage(`{}`)
	if configuration.Agent.Core != nil {
		native = v1.ResolvedHarnessConfig(configuration.Agent.Core.HarnessConfig)
	}
	return v1.SessionExecutionConfiguration{
		HarnessConfig: v1.ExecutionHarnessConfigSelection{Value: native, Source: input.harnessConfigSource},
		Object:        "agent.session.execution_configuration", SchemaVersion: 1,
		Model:   v1.ExecutionSelection{Value: &configuration.Agent.Model, Source: input.modelSource},
		Harness: v1.ExecutionSelection{Value: &engine, Source: harnessSource}, ModelProvider: selection,
	}
}

// getSessionExecutionConfiguration serves the administrator per-Session read.
func (h *Handler) getSessionExecutionConfiguration(w http.ResponseWriter, r *http.Request) {
	configuration, err := h.SessionAdmin.GetSessionExecutionConfiguration(r.Context(), tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, configuration)
}
