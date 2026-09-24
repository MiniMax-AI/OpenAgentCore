package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/writeaudit"
)

type sessionWriteAuditor interface {
	AuditSessionOperation(context.Context, string, string, string) error
}

func (h *Handler) auditSessionOperation(w http.ResponseWriter, r *http.Request, sessionID, action string) bool {
	if _, ok := writeaudit.FromContext(r.Context()); !ok {
		return true
	}
	auditor, ok := h.store.(sessionWriteAuditor)
	if !ok {
		writeStoreError(w, r, errors.New("session write audit is unavailable"))
		return false
	}
	if err := auditor.AuditSessionOperation(r.Context(), tenantID(r), sessionID, action); err != nil {
		writeStoreError(w, r, err)
		return false
	}
	return true
}
