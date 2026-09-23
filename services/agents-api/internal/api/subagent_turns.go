package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// @Summary Retrieve a Subagent Turn
// @Description Returns a Turn owned by this Subagent. Its agent_id is the Session's Agent ID and its subagent_id identifies the Subagent. Session Turn routes do not return child Turns. Unknown or inaccessible parent scopes return not found.
// @Tags Subagents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param subagent_id path string true "Subagent ID"
// @Param turn_id path string true "Turn ID"
// @Success 200 {object} v1.Turn
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/subagents/{subagent_id}/turns/{turn_id} [get]
func (h *Handler) getSubagentTurn(w http.ResponseWriter, r *http.Request) {
	if !h.subagentsReady(w) {
		return
	}
	value, err := h.subagents.GetSubagentTurn(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "subagent_id"), chi.URLParam(r, "turn_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// @Summary List a Subagent's Turns
// @Description Includes this Subagent's Turns after resume, with the Session's Agent ID as agent_id. Cursors belong to the same tenant, Session and Subagent. Missing recorded usage remains null. A limit outside 1–100 is rejected.
// @Tags Subagents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param subagent_id path string true "Subagent ID"
// @Param after query string false "Last Turn ID from the previous page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.TurnList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/subagents/{subagent_id}/turns [get]
func (h *Handler) listSubagentTurns(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok || !h.subagentsReady(w) {
		return
	}
	page, err := h.subagents.ListSubagentTurns(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "subagent_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, turnListResponse(page.Data, page.HasMore))
}

// @Summary List a Subagent Turn's Items
// @Description Returns Items owned by this exact Subagent Turn. Cursors belong to the same tenant, Session, Subagent and Turn.
// @Tags Subagents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param subagent_id path string true "Subagent ID"
// @Param turn_id path string true "Turn ID"
// @Param after query string false "Last Item ID from the previous page"
// @Param limit query int false "Page size; 0 is treated as 1 and values above 100 as 100" minimum(0) default(20)
// @Param order query string false "Resource order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.ItemList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/subagents/{subagent_id}/turns/{turn_id}/items [get]
func (h *Handler) listSubagentTurnItems(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r)
	if !ok || !h.subagentsReady(w) {
		return
	}
	page, err := h.subagents.ListSubagentTurnItems(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "subagent_id"), chi.URLParam(r, "turn_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemListResponse(page.Data, page.HasMore))
}
