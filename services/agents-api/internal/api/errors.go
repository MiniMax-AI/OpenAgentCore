package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError reports every 409 with type conflict_error, as every observed
// official conflict does (ERR-27); Core-only conflicts keep their own code.
// Every 401 has type invalid_request_error, as every observed official 401
// does (HP-07); an empty code serializes as null.
func writeError(w http.ResponseWriter, status int, code, message string, param ...string) {
	if observer, ok := w.(interface{ reportAPIError(string) }); ok {
		observer.reportAPIError(code)
	}
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	} else if status == http.StatusConflict {
		kind = "conflict_error"
	} else if code == "not_found_error" || code == "invalid_beta" {
		kind = code
	}
	var errorCode *string
	if code != "" {
		errorCode = &code
	}
	var errorParam *string
	if len(param) > 0 {
		errorParam = &param[0]
	}
	writeJSON(w, status, v1.ErrorResponse{Error: v1.APIError{Message: message, Type: kind, Code: errorCode, Param: errorParam}})
}

// writeInputError reports Session input admission failures. Input that the
// Session cannot accept in its current state is the official conflict_error;
// Idempotency-Key reuse keeps Core's local idempotency_conflict code.
func writeInputError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrSessionInputPending):
		writeError(w, http.StatusConflict, "conflict_error", "Earlier input to this Session is still pending.")
	case errors.Is(err, store.ErrTurnConflict):
		writeError(w, http.StatusConflict, "conflict_error", "The Turn cannot accept this input in its current state.")
	default:
		writeStoreError(w, r, err)
	}
}

const unstorableTextMessage = "Request text contains characters this service cannot store or compare, such as U+0000 or invalid UTF-8."

// fieldError is a request validation failure reported with the official
// invalid_request_error code. An empty param serializes as null.
type fieldError struct {
	param, message string
}

func (e *fieldError) Error() string { return e.message }

// writeFieldError reports a fieldError and returns false for any other error,
// which keeps its caller's existing local code.
func writeFieldError(w http.ResponseWriter, err error) bool {
	var field *fieldError
	if !errors.As(err, &field) {
		return false
	}
	if field.param == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", field.message)
	} else {
		writeError(w, http.StatusBadRequest, "invalid_request_error", field.message, field.param)
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error, notFoundParam ...string) {
	var cursor *store.InvalidCursorError
	var selection *store.MCPCredentialSelectionError
	var sandboxConfiguration *store.SandboxConfigurationError
	switch {
	case errors.As(err, &sandboxConfiguration):
		writeError(w, http.StatusBadRequest, "invalid_sandbox_configuration", sandboxConfiguration.Message)
	case errors.Is(err, store.ErrSandboxPublicURLUnreachable):
		writeError(w, http.StatusConflict, "sandbox_configuration_error", err.Error())
	case errors.Is(err, store.ErrRuntimeNodeAddressMismatch):
		writeError(w, http.StatusConflict, "sandbox_node_address_mismatch", "This node uses a different Core address than the installation public URL. Generate a new command on the Nodes page and run it on the host.")
	case errors.Is(err, store.ErrRuntimeSpecificationMismatch):
		writeError(w, http.StatusConflict, "sandbox_specification_mismatch", "The node resource limits or Runtime release do not match the active deployment. Restore its installed configuration or remove and enroll the node again after a drained deployment change.")
	case errors.Is(err, store.ErrProjectArchived):
		writeError(w, http.StatusConflict, "project_archived", "The target Project is archived.")
	case errors.Is(err, store.ErrProjectExists):
		writeError(w, http.StatusConflict, "project_exists", "This Project ID already exists.")
	case errors.Is(err, store.ErrProjectAPIKeyExists):
		writeError(w, http.StatusConflict, "project_api_key_exists", "This API key ID already exists. List its metadata and revoke it explicitly if the secret was not saved.")
	case errors.Is(err, store.ErrExecutorCredentialExists):
		writeError(w, http.StatusConflict, "executor_credential_exists", "This executor key ID already exists. Explicitly rotate it to replace the secret.")
	case errors.Is(err, store.ErrSandboxCredentialUnavailable):
		writeError(w, http.StatusServiceUnavailable, "sandbox_credential_unavailable", "Sandbox credentials are unavailable. Check the service credential encryption configuration.")
	case errors.Is(err, store.ErrSandboxDeploymentConflict):
		writeError(w, http.StatusConflict, "sandbox_deployment_conflict", "The sandbox deployment cannot change in its current state. Refresh the configuration, enter maintenance and finish resource cleanup before switching.")
	case errors.Is(err, store.ErrRuntimeNodeCredential):
		writeError(w, http.StatusUnauthorized, "invalid_node_credential", "A valid sandbox node enrollment or node credential is required.")
	case errors.Is(err, store.ErrRuntimeNodeInUse):
		writeError(w, http.StatusConflict, "runtime_node_in_use", "The sandbox node retains allocations, snapshots, reservations or pending cleanup.")
	case errors.Is(err, store.ErrRuntimeLocalNodeConfigured):
		writeError(w, http.StatusConflict, "runtime_local_node_configured", "The local sandbox node is enabled in deployment configuration. Disable it through provider maintenance and restart Core before removing it.")
	case errors.Is(err, store.ErrRuntimeNodeUnavailable):
		writeError(w, http.StatusServiceUnavailable, "runtime_node_unavailable", "The selected sandbox node is unavailable or has no capacity.")

	case errors.Is(err, store.ErrDefaultSkillVersion):
		writeError(w, http.StatusBadRequest, "invalid_value", "Cannot delete the default skill version.", "version")
	case errors.Is(err, store.ErrSourceFileTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "File exceeds this operation's content limit.")
	case errors.Is(err, store.ErrCredentialStorageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "credential_storage_unavailable", "Credential encryption is not configured on this service.")
	case errors.Is(err, store.ErrModelProviderRequired):
		writeError(w, http.StatusBadRequest, "model_provider_required", "This Session was created without a model provider and cannot run. Create a new Session with x_agents_core.model_provider or an Agent that has one saved.")
	case errors.Is(err, store.ErrHostedEnvironmentFailed):
		// Observed official status, type, code, null param and message.
		writeError(w, http.StatusConflict, "conflict_error", "the hosted environment failed to provision")
	case errors.Is(err, store.ErrEnvironmentUnavailable):
		writeError(w, http.StatusConflict, "environment_unavailable", "The environment is no longer available for new input.")
	case errors.Is(err, execution.ErrEnvironmentInputExpired):
		writeError(w, http.StatusConflict, "environment_input_expired", "The environment input deadline elapsed before admission.")
	case errors.Is(err, execution.ErrEnvironmentInputCancelled):
		writeError(w, http.StatusConflict, "environment_input_cancelled", "The environment input was cancelled before admission.")
	case errors.Is(err, execution.ErrWhitespaceOnlyText):
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", "This Session's harness does not accept a message whose text is only whitespace. Include non-whitespace text or an image, or use a harness that supports whitespace-only text.")
	case errors.Is(err, execution.ErrExecutionUnavailable):
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution is not available on this service.")
	case errors.As(err, &cursor):
		// Observed official fields for an unresolved list cursor: Skill versions
		// use invalid_value on after, Beta lists invalid_request_error with a null param.
		if listFamilyOf(r) == skillsList {
			writeError(w, http.StatusBadRequest, "invalid_value", cursor.Message, "after")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request_error", cursor.Message)
		}
	case errors.As(err, &selection):
		// Observed official fields for Session MCP credential selection (MV-03),
		// all with a null param.
		if selection.Conflict {
			writeError(w, http.StatusConflict, "conflict_error", selection.Message)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request_error", selection.Message)
		}
	case errors.Is(err, store.ErrNotFound):
		code := "not_found_error"
		// Files and Skills retain their non-beta error envelope.
		if strings.HasPrefix(r.URL.Path, "/v1/files/") || strings.HasPrefix(r.URL.Path, "/v1/skills/") || r.URL.Path == "/v1/files" || r.URL.Path == "/v1/skills" {
			code = ""
		}
		writeError(w, http.StatusNotFound, code, "Resource not found.", notFoundParam...)
	case errors.Is(err, store.ErrSessionNotIdle):
		// Observed official status, type, code, null param and message.
		writeError(w, http.StatusConflict, "conflict_error", "session must be durably idle or failed without required actions before deletion")
	case errors.Is(err, store.ErrUnknownFunctionCall):
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Unknown pending tool call.")
	case errors.Is(err, store.ErrFunctionCallTurnMismatch):
		writeError(w, http.StatusBadRequest, "invalid_request_error", "The tool call belongs to a different Turn.")
	case errors.Is(err, store.ErrFunctionResultConflict):
		writeError(w, http.StatusConflict, "conflict_error", "The tool call already has a different result.")
	case errors.Is(err, store.ErrTurnConflict):
		writeError(w, http.StatusConflict, "turn_conflict", "The Turn cannot accept this input in its current state.")
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "This idempotency key was used with different input.")
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid resource identifier or request limits.")
	case store.UnstorableText(err):
		// A documented local limit: PostgreSQL text and jsonb cannot store U+0000,
		// and text parameters, including query filters, reject invalid UTF-8.
		writeError(w, http.StatusBadRequest, "invalid_request_error", unstorableTextMessage)
	default:
		// Driver errors can include submitted values; do not log the raw error.
		log.Ctx(r.Context()).Error("oac-core persistence operation failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be completed.")
	}
}
