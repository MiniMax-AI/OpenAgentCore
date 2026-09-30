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

// @Summary Create a reusable Agent
// @Description Persists configuration independently of execution. Names over 128 characters and metadata outside 16 string pairs with 64-character keys and 512-character values return invalid_request_error with the official param; U+0000 in stored strings is rejected as a local storage limit. As on every Agents API JSON route, a non-JSON Content-Type, invalid UTF-8, malformed JSON, a repeated key at any depth or a non-object root returns invalid_request_error with a null param and the official message before other checks; an empty or null body is {}. Missing, unknown, wrongly typed or unsupported enum members of the pinned configuration shapes (tools, text, reasoning, service_tier, multi_agent) return invalid_request_error with the JSON path as param; duplicate function names, repeated web_search or tool_search and non-object schema root types return it with a null param. Supports model/name/instructions/metadata, explicit reasoning and service tiers, multi_agent, text/json_schema, function/tool_search/programmatic_tool_calling/web_search and HTTP MCP with nullable credential_id, service origin (omitted or null on HTTP transport is saved as service) and boolean required defaulting to false. Saving credential_id grants no access: Session admission checks attached Vault ownership and destination. MCP allowed_tools preserves null versus empty; saved HTTP transport includes empty headers. Model-derived reasoning defaults, other MCP variants and public retry conformance remain incomplete. web_search saves every pinned mode: omitted or null mode is saved as live and omitted or null context_size as medium; allowed_domains preserves null versus empty and a present location, including {}, includes all four keys with null for omitted ones, as observed officially (req_db41d2f6261b4abfb69465eafe719ab5, req_165d53b88445490b9146d8272c54134d). Session execution accepts only explicit disabled web_search and disabled programmatic_tool_calling through qualified Runtime controls; saved enabled forms reject at Session admission. Session execution admits only its supported configuration subset.
// @Tags Agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param body body v1.CreateAgentRequest true "Reusable Agent configuration"
// @Success 201 {object} v1.SavedAgent
// @Failure 400,401,413,500 {object} v1.ErrorResponse
// @Router /agents [post]
func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONObject(w, r)
	if !ok {
		return
	}
	if writeFieldError(w, metadataTypeError(raw)) || writeFieldError(w, validateSavedAgentBody(raw, savedAgentCreate)) {
		return
	}
	if err := validateSavedCoreInput(raw); err != nil {
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		return
	}
	var request v1.CreateAgentRequest
	if decodeInputObject(raw, &request, "model", "name", "instructions", "metadata", "multi_agent", "reasoning", "service_tier", "text", "tools", "x_agents_core") != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		return
	}
	command, err := resolveSavedAgent(request)
	if err != nil {
		if !writeFieldError(w, err) {
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

// @Summary Retrieve a reusable Agent
// @Description Reads the saved resource owned by the authenticated tenant, independently of execution Sessions.
// @Tags Agents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param agent_id path string true "Agent ID"
// @Success 200 {object} v1.SavedAgent
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/{agent_id} [get]
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
		return response, err
	}
	response.ID, response.Object = agent.ID, "agent"
	response.Metadata = agent.Metadata
	response.CreatedAt, response.UpdatedAt = agent.CreatedAt.Unix(), agent.UpdatedAt.Unix()
	return response, nil
}
