package api

import (
	"context"
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/go-chi/chi/v5"
)

// Agents runs the saved Agent writes.
type Agents interface {
	Create(context.Context, agents.CreateCommand) (agents.Agent, error)
	Update(context.Context, agents.UpdateCommand) (agents.Agent, error)
	Delete(context.Context, agents.DeleteCommand) (string, error)
}

// AgentsReader reads saved Agents. Session creation reads an Agent with
// GetAgentWithModelProvider when the Session inherits its model provider.
type AgentsReader interface {
	GetAgent(ctx context.Context, tenantID, agentID string) (agents.Agent, error)
	ListAgents(context.Context, agents.ListQuery) (agents.Page, error)
	GetAgentWithModelProvider(ctx context.Context, tenantID, agentID string) (agents.Agent, *v1.ModelProviderInput, error)
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObject(w, r)
	if !ok {
		return
	}
	if writeFieldError(w, validateSavedAgentBody(raw, createAgentParams)) {
		return
	}
	if err := validateSavedCoreInput(raw); err != nil {
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		return
	}
	var request v1.CreateAgentRequest
	// The walks bound members and types, not integer ranges.
	if json.Unmarshal(raw, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		return
	}
	command, err := resolveSavedAgent(request)
	if err != nil {
		if !writeStoredDataError(w, r, err) && !writeFieldError(w, err) {
			writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		}
		return
	}
	command.TenantID = tenantID(r)
	agent, err := h.Agents.Create(r.Context(), command)
	if err != nil {
		writeAgentsError(w, r, err)
		return
	}
	h.respondAgentStatus(w, r, agent, http.StatusCreated)
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request) {
	agent, err := h.AgentsReader.GetAgent(r.Context(), tenantID(r), chi.URLParam(r, "agent_id"))
	if err != nil {
		writeAgentsError(w, r, err)
		return
	}
	h.respondAgent(w, r, agent)
}

func (h *Handler) respondAgent(w http.ResponseWriter, r *http.Request, agent agents.Agent) {
	h.respondAgentStatus(w, r, agent, http.StatusOK)
}

func (h *Handler) respondAgentStatus(w http.ResponseWriter, r *http.Request, agent agents.Agent, status int) {
	response, err := agentResponse(agent)
	if err != nil {
		writeAgentsError(w, r, err)
		return
	}
	writeJSON(w, status, response)
}

func agentResponse(agent agents.Agent) (v1.SavedAgent, error) {
	var response v1.SavedAgent
	if err := json.Unmarshal(agent.Configuration, &response.SavedAgentConfiguration); err != nil {
		return response, &storedDataError{err}
	}
	response.ID, response.Object = agent.ID, "agent"
	response.Metadata = agent.Metadata
	response.CreatedAt, response.UpdatedAt = agent.CreatedAt.Unix(), agent.UpdatedAt.Unix()
	return response, nil
}
