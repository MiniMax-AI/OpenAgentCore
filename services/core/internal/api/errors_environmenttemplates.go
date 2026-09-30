package api

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
)

// writeEnvironmentTemplatesError writes the response for an Environment
// Template operation error.
func writeEnvironmentTemplatesError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, environmenttemplates.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found_error", "Resource not found.")
	case errors.Is(err, environmenttemplates.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	default:
		if writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
			return
		}
		writeInternalError(w, r)
	}
}
