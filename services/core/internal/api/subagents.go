package api

import (
	"context"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/go-chi/chi/v5"
)

// Subagents reads tenant-authorized, persisted public resources. Implementations
// must enforce every supplied parent scope, including the pagination cursor.
type Subagents interface {
	GetSubagent(context.Context, string, string, string) (v1.Subagent, error)
	ListSubagents(context.Context, string, string, string, int, bool) (v1.SubagentList, error)
	ListSubagentItems(context.Context, string, string, string, string, int, bool) (v1.ItemList, error)
	GetSubagentTurn(context.Context, string, string, string, string) (v1.Turn, error)
	ListSubagentTurns(context.Context, string, string, string, string, int, bool) (v1.TurnList, error)
	ListSubagentTurnItems(context.Context, string, string, string, string, string, int, bool) (v1.ItemList, error)
}

func (h *Handler) registerSubagentRoutes(r chi.Router) {
	const root = "/agents/sessions/{session_id}/subagents"
	r.Get(root, h.listSubagents)
	r.Get(root+"/{subagent_id}", h.getSubagent)
	r.Get(root+"/{subagent_id}/items", h.listSubagentItems)
	r.Get(root+"/{subagent_id}/turns", h.listSubagentTurns)
	r.Get(root+"/{subagent_id}/turns/{turn_id}", h.getSubagentTurn)
	r.Get(root+"/{subagent_id}/turns/{turn_id}/items", h.listSubagentTurnItems)
}

// @Summary Retrieve a Session Subagent
// @Description Returns this Session's persisted Subagent. Active includes idle between Turns. Resuming preserves opened_at and clears closed_at. Unknown or inaccessible parent scopes return not found.
// @Tags Subagents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param subagent_id path string true "Subagent ID"
// @Success 200 {object} v1.Subagent
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/subagents/{subagent_id} [get]
func (h *Handler) getSubagent(w http.ResponseWriter, r *http.Request) {
	value, err := h.Subagents.GetSubagent(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "subagent_id"))
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// @Summary List Session Subagents
// @Description Includes nested and closed Subagents. Cursors are Subagents of the same tenant and Session. Any other after value, including a malformed one, returns 400 invalid_request_error with the message "Invalid resource ID in `after`". A limit outside 1–100 is rejected.
// @Tags Subagents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param after query string false "Last Subagent ID from the previous page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Resource order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.SubagentList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/subagents [get]
func (h *Handler) listSubagents(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	page, err := h.Subagents.ListSubagents(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, subagentListResponse(page.Data, page.HasMore))
}

// @Summary List a Subagent's Items
// @Description Returns only this Subagent's own Items across all its Turns, not its descendants' Items. Cursors are Items of the same tenant, Session and Subagent. Any other after value, including a malformed one, returns 400 invalid_request_error with the message "Invalid session item ID in `after`".
// @Tags Subagents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param subagent_id path string true "Subagent ID"
// @Param after query string false "Last Item ID from the previous page"
// @Param limit query int false "Page size; 0 is treated as 1 and values above 100 as 100" minimum(0) default(20)
// @Param order query string false "Resource order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.ItemList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/subagents/{subagent_id}/items [get]
func (h *Handler) listSubagentItems(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r)
	if !ok {
		return
	}
	page, err := h.Subagents.ListSubagentItems(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "subagent_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemListResponse(page.Data, page.HasMore))
}
