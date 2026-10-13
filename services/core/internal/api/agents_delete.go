package api

import (
	"bytes"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Agent deletion does not accept a request body.")
		return
	}
	deleted, err := h.Agents.Delete(r.Context(), agents.DeleteCommand{TenantID: tenantID(r), AgentID: chi.URLParam(r, "agent_id")})
	if err != nil {
		writeAgentsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.AgentDeleted{ID: deleted, Object: "agent.deleted", Deleted: true})
}
