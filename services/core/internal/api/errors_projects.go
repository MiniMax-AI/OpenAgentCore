package api

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
)

// writeProjectsError maps projects errors to the Core administration
// responses.
func writeProjectsError(w http.ResponseWriter, r *http.Request, err error) {
	var name *projects.NameError
	switch {
	case errors.As(err, &name):
		// The name limit is Core administration detail; other routes keep the
		// generic response.
		if !isCoreErrorWriter(w) {
			writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
			return
		}
		writeCoreError(w, http.StatusBadRequest, "invalid_name", invalidNameMessage, CoreErrorDetails{"max_length": CoreErrorNumber(float64(name.MaxLength))}, "name")
	case errors.Is(err, projects.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found_error", "Resource not found.")
	case errors.Is(err, projects.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	case errors.Is(err, projects.ErrArchived):
		writeError(w, http.StatusConflict, "project_archived", "The target Project is archived.")
	case errors.Is(err, projects.ErrExists):
		writeError(w, http.StatusConflict, "project_exists", "This Project ID already exists.")
	case errors.Is(err, projects.ErrAPIKeyExists):
		writeError(w, http.StatusConflict, "project_api_key_exists", "This API key ID already exists. List its metadata and revoke it explicitly if the secret was not saved.")
	default:
		if writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
			return
		}
		writeInternalError(w, r)
	}
}
