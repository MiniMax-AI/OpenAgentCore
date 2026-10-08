package v1

import (
	"encoding/json"
	"net/http"
)

// NewAPIError constructs the shared HTTP error, including its nullable fields.
func NewAPIError(status int, code, message string, param ...string) APIError {
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	} else if status == http.StatusConflict {
		kind = "conflict_error"
	} else if code == "not_found_error" || code == "invalid_beta" {
		kind = code
	}
	var errorCode, errorParam *string
	if code != "" {
		errorCode = &code
	}
	if len(param) > 0 {
		errorParam = &param[0]
	}
	return APIError{Message: message, Type: kind, Code: errorCode, Param: errorParam}
}

// WriteHTTPError writes the shared envelope without changing route-owned cache
// or authentication headers.
func WriteHTTPError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: NewAPIError(status, code, message)})
}

// WebSocketError is the HTTP failure hook for machine WebSocket upgrades.
func WebSocketError(w http.ResponseWriter, _ *http.Request, status int, _ error) {
	w.Header().Set("Sec-Websocket-Version", "13")
	WriteHTTPError(w, status, "", http.StatusText(status))
}
