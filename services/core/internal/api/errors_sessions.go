package api

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// writeInputError reports Session input admission failures. Input that the
// Session cannot accept in its current state is the official conflict_error;
// Idempotency-Key reuse keeps Core's local idempotency_conflict code.
func writeInputError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sessions.ErrInputPending):
		writeError(w, http.StatusConflict, "conflict_error", "Earlier input to this Session is still pending.")
	case errors.Is(err, sessions.ErrTurnConflict):
		writeError(w, http.StatusConflict, "conflict_error", "The Turn cannot accept this input in its current state.")
	default:
		writeStoreError(w, r, err)
	}
}

// writeSessionsError maps the errors of Session operations: the sessions
// domain's errors, the execution admission errors they meet, and the Project
// and Environment configuration checks they perform.
func writeSessionsError(w http.ResponseWriter, r *http.Request, err error) {
	var cursor *sessions.CursorError
	switch {
	case errors.Is(err, projects.ErrArchived):
		// Executor credential management checks the Project in its own
		// transaction.
		writeProjectsError(w, r, err)
	case errors.Is(err, sessions.ErrInstallationAuthorization):
		writeError(w, http.StatusUnauthorized, "installation_authorization_invalid", sessions.ErrInstallationAuthorization.Error())
	case errors.Is(err, sessions.ErrExecutorCredentialExists):
		writeError(w, http.StatusConflict, "executor_credential_exists", "This executor key ID already exists. Explicitly rotate it to replace the secret.")
	case errors.Is(err, execution.ErrModelProviderRequired):
		writeError(w, http.StatusBadRequest, "model_provider_required", "This Session was created without a model provider and cannot run. Create a new Session with x_agents_core.model_provider or an Agent that has one saved.")
	case errors.Is(err, sessions.ErrHostedEnvironmentFailed):
		// Observed official status, type, code, null param and message.
		writeError(w, http.StatusConflict, "conflict_error", "the hosted environment failed to provision")
	case errors.Is(err, sessions.ErrEnvironmentUnavailable):
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
	case errors.Is(err, sessions.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found_error", "Resource not found.")
	case errors.Is(err, sessions.ErrNotIdle):
		// Observed official status, type, code, null param and message.
		writeError(w, http.StatusConflict, "conflict_error", "session must be durably idle or failed without required actions before deletion")
	case errors.Is(err, sessions.ErrUnknownFunctionCall):
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Unknown pending tool call.")
	case errors.Is(err, sessions.ErrFunctionCallTurnMismatch):
		writeError(w, http.StatusBadRequest, "invalid_request_error", "The tool call belongs to a different Turn.")
	case errors.Is(err, sessions.ErrFunctionResultConflict):
		writeError(w, http.StatusConflict, "conflict_error", "The tool call already has a different result.")
	case errors.Is(err, sessions.ErrTurnConflict):
		writeError(w, http.StatusConflict, "turn_conflict", "The Turn cannot accept this input in its current state.")
	case errors.Is(err, sessions.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "This idempotency key was used with different input.")
	case errors.Is(err, sessions.ErrInvalidInput), errors.Is(err, environmentconfig.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", invalidInputMessage)
	default:
		if writeAuditSourceError(w, r, err) || writeTextValueError(w, r, err) || writeCredentialUnavailableError(w, r, err) {
			return
		}
		writeInternalError(w, r)
	}
}
