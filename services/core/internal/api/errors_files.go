package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
)

// writeFilesError maps a files error to its public response. notFoundParam
// names the parameter a missing File came from.
func writeFilesError(w http.ResponseWriter, r *http.Request, err error, notFoundParam ...string) {
	if writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
		return
	}
	switch {
	case errors.Is(err, files.ErrNotFound):
		code := "not_found_error"
		// The Files routes keep their non-beta error envelope.
		if r.URL.Path == "/v1/files" || strings.HasPrefix(r.URL.Path, "/v1/files/") {
			code = ""
		}
		writeError(w, http.StatusNotFound, code, "Resource not found.", notFoundParam...)
	case errors.Is(err, files.ErrTooLarge):
		writeContentTooLarge(w)
	case errors.Is(err, files.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid resource identifier or request limits.")
	default:
		// Storage failures can include submitted values; do not log the error.
		log.Ctx(r.Context()).Error("oac-core persistence operation failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be completed.")
	}
}
