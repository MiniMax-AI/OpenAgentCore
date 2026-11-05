package api

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/automations"
)

// writeAutomationsError maps the errors of Automation operations.
func writeAutomationsError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *automations.InvalidError
	var conflict *automations.ConflictError
	switch {
	case errors.Is(err, automations.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found_error", "Resource not found.")
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, "invalid_request_error", invalid.Message, invalid.Param)
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, "conflict_error", conflict.Message)
	case writeStoredDataError(w, r, err), writeTextValueError(w, r, err), writeAuditSourceError(w, r, err):
	default:
		writeInternalError(w, r)
	}
}
