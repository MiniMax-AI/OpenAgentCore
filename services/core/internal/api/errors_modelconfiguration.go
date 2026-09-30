package api

import (
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/go-chi/chi/v5"
)

// writeModelConfigurationError reports a failure from the deployment default
// model configuration operations. A configuration the Harness declaration
// rejects names its field; anything unmapped is an internal error.
func writeModelConfigurationError(w http.ResponseWriter, r *http.Request, err error) {
	var field *v1.ModelProviderError
	if errors.As(err, &field) {
		if !writeCoreModelProviderError(w, err, chi.URLParam(r, "harness")) {
			writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		}
		return
	}
	if writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
		return
	}
	writeInternalError(w, r)
}
