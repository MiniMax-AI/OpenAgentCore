package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Items reads a Session's root Items.
type Items interface {
	ListItems(context.Context, string, string, string, int, bool) (sessions.ItemPage, error)
}

func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) {
	options, ok := readClampedPage(w, r)
	if !ok {
		return
	}
	page, err := h.Items.ListItems(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemListResponse(page.Items, page.HasMore))
}
