package api

import (
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

func (h *Handler) auditSessionOperation(w http.ResponseWriter, r *http.Request, sessionID, action string) bool {
	if _, ok := writeaudit.FromContext(r.Context()); !ok {
		return true
	}
	if err := h.Sessions.AuditSessionOperation(r.Context(), tenantID(r), sessionID, action); err != nil {
		writeStoreError(w, r, err)
		return false
	}
	return true
}
