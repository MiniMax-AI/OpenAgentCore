package api

import (
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// sessionCreationRequest records caller intent before mutable sources resolve:
// saved Agents, templates, credentials and hosted deployment defaults. A retry
// then returns the committed Session even after those sources change. Other
// inline requests keep the resolved-request retry rule; the store leaves the
// deployment default out of that hash, so it cannot change their identity.
func sessionCreationRequest(input sessionRequest, initial []store.Input) (json.RawMessage, error) {
	if input.Agent != nil && input.Agent.Model != nil && (input.Environment == nil || input.Environment.Type != "openai_hosted") && input.XAgentsCore == nil && input.AgentID == nil && input.templateID == "" && len(input.initialFiles) == 0 && input.initialization.Empty() && !inlineCredentialIntent(input) && input.agentFields["x_agents_core"] == nil {
		return nil, nil
	}
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
		InitialInputs []store.Input              `json:"initial_inputs,omitempty"`
	}{input.XAgentsCore, agentID, input.agentFields, environment, input.Metadata, input.VaultIDs, initial})
}

func (h *Handler) recoverSessionCreation(w http.ResponseWriter, r *http.Request, key string, request json.RawMessage, stream bool) bool {
	if len(request) == 0 {
		return false
	}
	result, err := h.Sessions.FindSessionCreation(r.Context(), tenantID(r), key, request, sessionCreator(r))
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		writeStoreError(w, r, err)
		return true
	}
	if stream {
		if !h.auditSessionOperation(w, r, result.Session.ID, "create") {
			return true
		}
		// Recorded-intent lookup finds an existing creation, which sends no events.
		h.respondSessionCreationStream(w, r, result)
	} else {
		session, err := h.Sessions.GetSession(r.Context(), tenantID(r), result.Session.ID)
		if err != nil {
			writeStoreError(w, r, err)
		} else if h.auditSessionOperation(w, r, session.ID, "create") {
			h.respondSessionStatus(w, r, session, http.StatusCreated)
		}
	}
	return true
}
