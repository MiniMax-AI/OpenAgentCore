package api

import (
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
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

// writeStoreError reports a failure of an operation that can meet a sandbox
// reset or a deployment's sandbox resources, then the Session errors.
func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
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
	writeSessionsError(w, r, err)
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
