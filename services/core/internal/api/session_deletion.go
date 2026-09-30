package api

import (
	"bytes"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// @Summary Delete an execution Session
// @Description Removes a durably idle or failed Session and its history from the public API. A Session whose root Turn is queued, in progress or waiting (including required actions) or whose input reservation is pending returns 409 conflict_error and is left unchanged; cancel it and wait until it is idle before deleting. Subagent child Turns and pending Environment file writes are not checked and do not block deletion. Repeating the deletion of the caller's own deleted Session returns the same confirmation; missing and foreign Sessions return 404. Internal records and native history are retained pending separate physical cleanup; overlapping stream timing remains unverified.
// @Tags Sessions
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.SessionDeleted
// @Failure 400,401,404,409,413,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id} [delete]
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
		writeStoreError(w, r, sessions.ErrNotFound)
		return
	}
	if err := h.Sessions.DeleteSession(r.Context(), tenantID(r), id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SessionDeleted{ID: parsed.String(), Object: "agent.session.deleted", Deleted: true})
}
