package api

import (
	"bytes"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Session deletion does not accept a request body.")
		return
	}
	id := chi.URLParam(r, "session_id")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		writeSessionsError(w, r, sessions.ErrNotFound)
		return
	}
	if err := h.Sessions.DeleteSession(r.Context(), sessions.DeleteSessionCommand{TenantID: tenantID(r), SessionID: id}); err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SessionDeleted{ID: parsed.String(), Object: "agent.session.deleted", Deleted: true})
}
