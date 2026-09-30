package api

import (
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
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
	writeAPIError(w, status, code, message, nil, param...)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, details CoreErrorDetails, param ...string) {
	reportAPIError(w, code)
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
	if isCoreErrorWriter(w) {
		writeJSON(w, status, CoreErrorResponse{Error: CoreAPIError{Message: message, Type: kind, Code: errorCode, Param: errorParam, Details: validCoreDetails(details)}})
		return
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

// writeContentTooLarge reports uploaded or copied content beyond the
// operation's limit.
func writeContentTooLarge(w http.ResponseWriter) {
	writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "File exceeds this operation's content limit.")
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
	// A reset's resource check unwraps to deployment.ErrConflict and admission
	// paused by a reset is Session admission's, so both precede the deployment errors.
	var inUse *store.SandboxInUseError
	switch {
	case errors.As(err, &inUse):
		writeCoreError(w, http.StatusConflict, "sandbox_in_use", "Hosted sandbox resources still belong to this deployment.", CoreErrorDetails{"allocations": CoreErrorNumber(float64(inUse.Resources.Allocations)), "pending": CoreErrorNumber(float64(inUse.Resources.Pending))})
		return
	case errors.Is(err, store.ErrSandboxResetAdmission):
		writeError(w, http.StatusServiceUnavailable, "sandbox_reset_in_progress", "A sandbox reset is in progress.")
		return
	}
	if writeSandboxError(w, err) {
		return
	}
	var cursor *store.InvalidCursorError
	switch {
	case errors.Is(err, projects.ErrArchived):
		// Executor credential management checks the Project in its own
		// transaction.
		writeProjectsError(w, r, err)
	case errors.Is(err, store.ErrInstallationAuthorization):
		writeError(w, http.StatusUnauthorized, "installation_authorization_invalid", store.ErrInstallationAuthorization.Error())
	case errors.Is(err, store.ErrExecutorCredentialExists):
		writeError(w, http.StatusConflict, "executor_credential_exists", "This executor key ID already exists. Explicitly rotate it to replace the secret.")
	case errors.Is(err, execution.ErrModelProviderRequired):
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
		// Observed official fields for an unresolved Beta list cursor, with a null param.
		writeError(w, http.StatusBadRequest, "invalid_request_error", cursor.Message)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found_error", "Resource not found.", notFoundParam...)
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
	case errors.Is(err, store.ErrInvalidInput), errors.Is(err, environmentconfig.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	default:
		if writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
			return
		}
		writeInternalError(w, r)
	}
}

// invalidInputMessage accompanies the 400 invalid_request for invalid
// identifiers, limits, audit queries and audit provenance.
const invalidInputMessage = "Invalid resource identifier or request limits."

// writeInternalError reports an unmapped failure. Driver errors can include
// submitted values, so the raw error is never logged.
func writeInternalError(w http.ResponseWriter, r *http.Request) {
	log.Ctx(r.Context()).Error("oac-core persistence operation failed")
	writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be completed.")
}

// writeTextValueError reports request text that PostgreSQL cannot store and
// returns false for any other error. It is a documented local limit: text and
// jsonb cannot store U+0000, and text parameters, including query filters,
// reject invalid UTF-8.
func writeTextValueError(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, textvalue.ErrUnstorable) && !store.UnstorableText(err) {
		return false
	}
	writeError(w, http.StatusBadRequest, "invalid_request_error", unstorableTextMessage)
	return true
}

// writeCredentialUnavailableError reports a request that needs credential
// encryption on a service without a credential key, and returns false for any
// other error.
func writeCredentialUnavailableError(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, credentialcrypto.ErrUnavailable) {
		return false
	}
	writeError(w, http.StatusServiceUnavailable, "credential_storage_unavailable", "Credential encryption is not configured on this service.")
	return true
}

// writeAuditSourceError reports write or administrator provenance that cannot
// be recorded, so the write failed closed, and returns false for any other
// error.
func writeAuditSourceError(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, writeaudit.ErrInvalidSource) && !errors.Is(err, adminaudit.ErrInvalidSource) {
		return false
	}
	writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	return true
}
