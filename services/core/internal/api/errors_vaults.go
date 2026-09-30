package api

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// writeVaultsError maps a Vault, Credential or Session credential selection
// error to its public error response.
func writeVaultsError(w http.ResponseWriter, r *http.Request, err error) {
	if writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
		return
	}
	var selection *vaults.MCPCredentialSelectionError
	switch {
	case errors.As(err, &selection):
		// Observed official fields for Session MCP credential selection (MV-03),
		// all with a null param.
		if selection.Conflict {
			writeError(w, http.StatusConflict, "conflict_error", selection.Message)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request_error", selection.Message)
		}
	case errors.Is(err, vaults.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found_error", "Resource not found.")
	case errors.Is(err, vaults.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	default:
		writeInternalError(w, r)
	}
}
