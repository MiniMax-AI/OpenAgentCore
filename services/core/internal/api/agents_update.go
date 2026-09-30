package api

import (
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/go-chi/chi/v5"
)

// @Summary Update a reusable Agent
// @Description Preserves omitted fields and replaces supplied fields using shared saved-configuration validation. Null name/instructions clear; null or empty metadata clears all pairs. Name, metadata and configuration validation errors return invalid_request_error with the official param, using the Agent create rules before the Agent lookup. Existing Session snapshots are unchanged. Empty updates advance updated_at without changing saved fields. Nested replacement/null defaults, model-derived reasoning and exact hosted error behavior remain incompletely verified.
// @Tags Agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param agent_id path string true "Agent ID"
// @Param body body v1.UpdateAgentRequest true "Supplied reusable Agent fields"
// @Success 200 {object} v1.SavedAgent
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /agents/{agent_id} [post]
func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObject(w, r)
	if !ok {
		return
	}
	command, err := resolveAgentUpdate(raw)
	if err != nil {
		if !writeFieldError(w, err) {
			writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		}
		return
	}
	command.TenantID, command.AgentID = tenantID(r), chi.URLParam(r, "agent_id")
	updated, err := h.Agents.Update(r.Context(), command)
	if err != nil {
		writeAgentsError(w, r, err)
		return
	}
	h.respondAgent(w, r, updated)
}

// resolveAgentUpdate returns the command without its tenant and Agent.
func resolveAgentUpdate(raw []byte) (agents.UpdateCommand, error) {
	if err := metadataTypeError(raw); err != nil {
		return agents.UpdateCommand{}, err
	}
	if err := validateSavedAgentBody(raw, savedAgentUpdate); err != nil {
		return agents.UpdateCommand{}, err
	}
	if err := validateSavedCoreInput(raw); err != nil {
		return agents.UpdateCommand{}, err
	}
	var request v1.UpdateAgentRequest
	if decodeInputObject(raw, &request, "model", "name", "instructions", "metadata", "multi_agent", "reasoning", "service_tier", "text", "tools", "x_agents_core") != nil {
		return agents.UpdateCommand{}, errors.New("Request must be a JSON object containing supported fields.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return agents.UpdateCommand{}, err
	}
	if _, supplied := fields["model"]; supplied && request.Model == nil {
		return agents.UpdateCommand{}, errors.New("model must be a string when supplied.")
	}
	normalized, err := resolveSavedFields(v1.CreateAgentRequest(request))
	if err != nil {
		return agents.UpdateCommand{}, err
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(normalized.Configuration, &patch); err != nil {
		return agents.UpdateCommand{}, err
	}
	if _, supplied := fields["x_agents_core"]; supplied && request.XAgentsCore == nil {
		patch["x_agents_core"] = json.RawMessage(`null`)
	}
	for field := range patch {
		if _, supplied := fields[field]; !supplied {
			delete(patch, field)
		}
	}
	var result agents.UpdateCommand
	if extension, supplied := fields["x_agents_core"]; supplied {
		_, coreFields := orderedMembers(extension)
		_, providerSupplied := coreFields["model_provider"]
		switch {
		case request.XAgentsCore == nil:
			result.ModelProvider = &agents.ModelProviderChange{}
		case providerSupplied:
			result.ModelProvider = &agents.ModelProviderChange{Provider: normalized.ModelProvider}
			if request.XAgentsCore.ModelProvider == nil {
				_, corePatch := orderedMembers(patch["x_agents_core"])
				corePatch["model_provider"] = json.RawMessage(`null`)
				patch["x_agents_core"], err = json.Marshal(corePatch)
				if err != nil {
					return agents.UpdateCommand{}, err
				}
			}
		}
	}
	if _, supplied := fields["metadata"]; supplied {
		result.Metadata = &normalized.Metadata
	}
	result.Configuration, err = json.Marshal(patch)
	return result, err
}
