package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
)

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r)
	if !ok {
		return
	}
	page, err := h.AgentsReader.ListAgents(r.Context(), agents.ListQuery{TenantID: tenantID(r), After: options.after, Limit: options.limit, Ascending: options.ascending})
	if err != nil {
		writeAgentsError(w, r, err)
		return
	}
	response := v1.SavedAgentList{Object: "list", Data: make([]v1.SavedAgent, 0, len(page.Agents)), HasMore: page.NextCursor != ""}
	for _, agent := range page.Agents {
		item, err := agentResponse(agent)
		if err != nil {
			writeAgentsError(w, r, err)
			return
		}
		response.Data = append(response.Data, item)
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
