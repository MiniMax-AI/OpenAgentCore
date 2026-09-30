package api

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// writeDeploymentError reports an error of the sandbox deployment, its nodes
// or the execution owner that changes them.
func writeDeploymentError(w http.ResponseWriter, r *http.Request, err error) {
	if writeSandboxError(w, err) {
		return
	}
	switch {
	case errors.Is(err, execution.ErrExecutionUnavailable):
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution is not available on this service.")
	case errors.Is(err, deployment.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found_error", "Resource not found.")
	case errors.Is(err, deployment.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	default:
		if writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
			return
		}
		writeInternalError(w, r)
	}
}

// writeSandboxError reports a sandbox deployment, node or Sandbox Provider
// error and returns false for any other error. Session and Environment
// admission report the same errors through writeStoreError.
func writeSandboxError(w http.ResponseWriter, err error) bool {
	// Adapter discovery and persisted configuration share the same public error.
	var validation *sandbox.ValidationError
	if errors.As(err, &validation) {
		err = &deployment.ConfigurationError{Message: validation.Message, Validation: validation}
	}
	if writeCoreValidationError(w, err) {
		return true
	}
	var configuration *sandbox.ConfigurationError
	var unsupported *providercontract.UnsupportedError
	var deploymentConfiguration *deployment.ConfigurationError
	var stale *deployment.GenerationStaleError
	var resetRequired *deployment.ResetRequiredError
	switch {
	case errors.As(err, &configuration):
		status := http.StatusInternalServerError
		switch configuration.Class {
		case sandbox.ConfigurationInvalid:
			status = http.StatusBadRequest
		case sandbox.ConfigurationConflict:
			status = http.StatusConflict
		case sandbox.ConfigurationUnconfirmed:
			status = http.StatusServiceUnavailable
		default:
			writeError(w, status, "internal_error", "The operation could not be completed.")
			return true
		}
		if configuration.Param == "" {
			writeError(w, status, configuration.Code, configuration.Message)
		} else {
			writeError(w, status, configuration.Code, configuration.Message, configuration.Param)
		}
	case errors.As(err, &unsupported):
		writeError(w, http.StatusBadRequest, "sandbox_operation_unsupported", "The selected sandbox provider does not support this operation.")
	case errors.Is(err, sandbox.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_sandbox_configuration", "Invalid sandbox provider configuration.", "configuration")
	case errors.As(err, &stale):
		writeCoreError(w, http.StatusConflict, "sandbox_generation_stale", "The sandbox deployment generation changed. Refresh before submitting again.", CoreErrorDetails{"current_generation": CoreErrorNumber(float64(stale.CurrentGeneration))})
	case errors.As(err, &resetRequired):
		writeCoreError(w, http.StatusConflict, "sandbox_reset_required", "Reset the sandbox deployment before changing this configuration.", CoreErrorDetails{"current_provider": CoreErrorString(resetRequired.CurrentProvider), "requested_provider": CoreErrorString(resetRequired.RequestedProvider)})
	case errors.Is(err, deployment.ErrResetInProgress):
		writeError(w, http.StatusConflict, "sandbox_reset_in_progress", "A sandbox reset is in progress.")
	case errors.Is(err, deployment.ErrNotConfigured):
		writeError(w, http.StatusConflict, "sandbox_not_configured", "The sandbox deployment is not configured.")
	case errors.As(err, &deploymentConfiguration):
		writeError(w, http.StatusBadRequest, "invalid_sandbox_configuration", deploymentConfiguration.Message)
	case errors.Is(err, deployment.ErrPublicURLUnreachable):
		writeError(w, http.StatusConflict, "sandbox_configuration_error", err.Error())
	case errors.Is(err, deployment.ErrNodeAddressMismatch):
		writeError(w, http.StatusConflict, "sandbox_node_address_mismatch", "This node uses a different Core address than the installation public URL. Generate a new command on the Nodes page and run it on the host.")
	case errors.Is(err, deployment.ErrSpecificationMismatch):
		writeError(w, http.StatusConflict, "sandbox_specification_mismatch", "The node resource limits or Runtime release do not match the active deployment. Restore its installed configuration or remove and enroll the node again after a drained deployment change.")
	case errors.Is(err, deployment.ErrNodeExists):
		writeError(w, http.StatusConflict, "idempotency_conflict", "This idempotency key was used with different input.")
	case errors.Is(err, deployment.ErrConflict):
		writeError(w, http.StatusConflict, "sandbox_deployment_conflict", "The sandbox deployment cannot change in its current state. Refresh the configuration and inspect its reset and resource state.")
	case errors.Is(err, deployment.ErrNodeCredential):
		writeError(w, http.StatusUnauthorized, "invalid_node_credential", "A valid sandbox node enrollment or node credential is required.")
	case errors.Is(err, deployment.ErrNodeInUse):
		writeError(w, http.StatusConflict, "runtime_node_in_use", "The sandbox node retains allocations, snapshots, reservations or pending cleanup.")
	case errors.Is(err, deployment.ErrLocalNodeConfigured):
		writeError(w, http.StatusConflict, "runtime_local_node_configured", "The local sandbox node is enabled in deployment configuration. Drain it with the previous release and remove its file-managed configuration before replacing it.")
	case errors.Is(err, deployment.ErrNodesPreparing):
		writeError(w, http.StatusServiceUnavailable, "sandbox_nodes_preparing", "Sandbox nodes are preparing the requested Runtime.")
	case errors.Is(err, deployment.ErrNodeUnavailable):
		writeError(w, http.StatusServiceUnavailable, "runtime_node_unavailable", "The selected sandbox node is unavailable or has no capacity.")
	default:
		return false
	}
	return true
}
