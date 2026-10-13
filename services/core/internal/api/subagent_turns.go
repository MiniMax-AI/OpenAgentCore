package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) getSubagentTurn(w http.ResponseWriter, r *http.Request) {
	value, err := h.Subagents.GetSubagentTurn(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "subagent_id"), chi.URLParam(r, "turn_id"))
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) listSubagentTurns(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	page, err := h.Subagents.ListSubagentTurns(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "subagent_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, turnListResponse(page.Data, page.HasMore))
}

func (h *Handler) listSubagentTurnItems(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r)
	if !ok {
		return
	}
	page, err := h.Subagents.ListSubagentTurnItems(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "subagent_id"), chi.URLParam(r, "turn_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemListResponse(page.Data, page.HasMore))
}
