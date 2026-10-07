package api

import (
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
)

// harnessRequiredMessage reports native parameters on an Agent or Session
// without a selected Harness.
const harnessRequiredMessage = "harness_config parameters require an explicit x_agents_core.harness."

// writeAgentsError reports an error of the Agent operations.
func writeAgentsError(w http.ResponseWriter, r *http.Request, err error) {
	if writeStoredDataError(w, r, err) || writeTextValueError(w, r, err) || writeAuditSourceError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
		return
	}
	var provider *v1.ModelProviderError
	switch {
	case writeSelectionError(w, err):
	case errors.Is(err, harnessconfig.ErrHarnessRequired):
		writeError(w, http.StatusBadRequest, "invalid_request_error", harnessRequiredMessage, "x_agents_core.harness")
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

// writeSelectionError reports a selection the Harness declaration rejects,
// with the rejected field path as param.
func writeSelectionError(w http.ResponseWriter, err error) bool {
	var selection *proto.SelectionError
	if !errors.As(err, &selection) {
		return false
	}
	var param []string
	if selection.Param != "" {
		param = append(param, selection.Param)
	}
	writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error(), param...)
	return true
}
