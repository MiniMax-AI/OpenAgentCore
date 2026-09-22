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

func writeError(w http.ResponseWriter, status int, code, message string, param ...string) {
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	} else if status == http.StatusUnauthorized {
		kind = "authentication_error"
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

func writeStoreError(w http.ResponseWriter, r *http.Request, err error, notFoundParam ...string) {
	switch {
	case errors.Is(err, store.ErrDefaultSkillVersion):
		writeError(w, http.StatusBadRequest, "invalid_request", "Change the default version before deleting this Skill version.")
	case errors.Is(err, store.ErrSourceFileTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "File exceeds this operation's content limit.")
	case errors.Is(err, store.ErrCredentialStorageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "credential_storage_unavailable", "Credential encryption is not configured on this service.")
	case errors.Is(err, store.ErrEnvironmentUnavailable):
		writeError(w, http.StatusConflict, "environment_unavailable", "The environment is no longer available for new input.")
	case errors.Is(err, execution.ErrEnvironmentInputExpired):
		writeError(w, http.StatusConflict, "environment_input_expired", "The environment input deadline elapsed before admission.")
	case errors.Is(err, execution.ErrEnvironmentInputCancelled):
		writeError(w, http.StatusConflict, "environment_input_cancelled", "The environment input was cancelled before admission.")
	case errors.Is(err, execution.ErrExecutionUnavailable):
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution is not available on this service.")
	case errors.Is(err, store.ErrNotFound):
		code := "not_found_error"
		// Files and Skills retain their non-beta error envelope.
		if strings.HasPrefix(r.URL.Path, "/v1/files/") || strings.HasPrefix(r.URL.Path, "/v1/skills/") || r.URL.Path == "/v1/files" || r.URL.Path == "/v1/skills" {
			code = ""
		}
		writeError(w, http.StatusNotFound, code, "Resource not found.", notFoundParam...)
	case errors.Is(err, store.ErrTurnConflict):
		writeError(w, http.StatusConflict, "turn_conflict", "The Turn cannot accept this input in its current state.")
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "This idempotency key was used with different input.")
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid resource identifier or request limits.")
	default:
		// Driver errors can include submitted values; do not log the raw error.
		log.Ctx(r.Context()).Error("agents-api persistence operation failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be completed.")
	}
}
