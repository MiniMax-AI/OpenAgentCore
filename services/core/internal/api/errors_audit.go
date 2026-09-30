package api

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// writeAuditError maps an audit query error to its Core error response.
func writeAuditError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, writeaudit.ErrInvalidQuery), errors.Is(err, adminaudit.ErrInvalidQuery):
		writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	default:
		if writeTextValueError(w, r, err) {
			return
		}
		writeInternalError(w, r)
	}
}
