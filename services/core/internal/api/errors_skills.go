package api

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
)

// writeSkillsError maps the skills domain's errors to the Skills responses.
// The /v1 Skills routes keep their own observed error fields; the /core/v1
// Project routes use the Beta ones.
func writeSkillsError(w http.ResponseWriter, r *http.Request, err error) {
	skillsRoute := listFamilyOf(r) == skillsList
	var cursor *skills.CursorError
	switch {
	case errors.Is(err, skills.ErrDefaultVersion):
		writeError(w, http.StatusBadRequest, "invalid_value", "Cannot delete the default skill version.", "version")
	case errors.As(err, &cursor):
		// Observed official fields for an unresolved version cursor.
		if skillsRoute {
			writeError(w, http.StatusBadRequest, "invalid_value", cursor.Message, "after")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request_error", cursor.Message)
		}
	case errors.Is(err, skills.ErrNotFound):
		code := "not_found_error"
		if skillsRoute {
			code = ""
		}
		writeError(w, http.StatusNotFound, code, "Resource not found.")
	case errors.Is(err, skills.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	case writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err):
	default:
		writeInternalError(w, r)
	}
}
