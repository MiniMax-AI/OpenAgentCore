package api

import (
	"context"
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/go-chi/chi/v5"
)

type sessionExecutionConfigurationStore interface {
	GetSessionExecutionConfiguration(context.Context, string, string) (v1.SessionExecutionConfiguration, error)
}

// Record selection sources at resolution time. Null Agent extensions reset the
// harness to deployment defaults but do not clear inherited provider bundles.
func sessionExecutionProjection(input sessionRequest, saved *v1.SavedAgent, inherited, provider *v1.ModelProviderInput, engine string, raw json.RawMessage) v1.SessionExecutionConfiguration {
	var configuration struct {
		Agent struct {
			Model string `json:"model"`
		} `json:"agent"`
	}
	_ = json.Unmarshal(raw, &configuration) // The resolved configuration was already validated.
	modelSource := "session"
	if saved != nil && (input.Agent == nil || input.Agent.Model == nil) {
		modelSource = "agent"
	}
	harnessSource := "deployment"
	if _, overridden := input.agentFields["x_agents_core"]; overridden {
		if input.Agent != nil && input.Agent.XAgentsCore != nil && input.Agent.XAgentsCore.Harness != "" {
			harnessSource = "session"
		}
	} else if saved != nil && saved.XAgentsCore != nil && saved.XAgentsCore.Harness != "" {
		harnessSource = "agent"
	}
	selection := v1.ExecutionProviderSelection{Source: "unknown", Status: "unavailable"}
	if provider != nil {
		selection.Source, selection.Status = "deployment", "redacted"
		if input.XAgentsCore != nil && input.XAgentsCore.ModelProvider != nil {
			selection.Source = "session"
		} else if inherited != nil {
			selection.Source = "agent"
		}
		if selection.Source != "deployment" {
			selection.Status, selection.Configuration = "available", provider.SafeView()
		}
	}
	return v1.SessionExecutionConfiguration{
		Object: "agent.session.execution_configuration", SchemaVersion: 1,
		Model:   v1.ExecutionSelection{Value: &configuration.Agent.Model, Source: modelSource},
		Harness: v1.ExecutionSelection{Value: &engine, Source: harnessSource}, ModelProvider: selection,
	}
}

// @Summary Retrieve frozen Session execution configuration
// @Description Returns the committed model, harness and safe provider selection with recorded sources. This read never decrypts credentials, resolves current defaults or probes execution health. Deployment provider details remain redacted for project callers. Historical provenance and missing provider projections are explicitly unknown/unavailable.
// @Tags Core extensions
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.SessionExecutionConfiguration
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/execution-configuration [get]
func (h *Handler) getSessionExecutionConfiguration(w http.ResponseWriter, r *http.Request) {
	source, ok := h.store.(sessionExecutionConfigurationStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "execution_configuration_unavailable", "Session execution configuration is unavailable.")
		return
	}
	configuration, err := source.GetSessionExecutionConfiguration(r.Context(), tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, configuration)
}
