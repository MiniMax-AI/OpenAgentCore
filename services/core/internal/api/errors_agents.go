package api

import (
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
)

// writeAgentsError reports an error of the Agent operations.
func writeAgentsError(w http.ResponseWriter, r *http.Request, err error) {
	if writeStoredDataError(w, r, err) || writeTextValueError(w, r, err) || writeAuditSourceError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
		return
	}
	var provider *v1.ModelProviderError
	switch {
	case errors.As(err, &provider):
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", provider.Error())
	case errors.Is(err, agents.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found_error", "Resource not found.")
	case errors.Is(err, agents.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid resource identifier or request limits.")
	default:
		log.Ctx(r.Context()).Error("oac-core persistence operation failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be completed.")
	}
}
