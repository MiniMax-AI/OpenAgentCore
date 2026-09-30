package main

import (
	"net/http"
	"strings"
)

// Console failures have fixed safe text, not transport errors or request values.
// Core's own error responses pass through the proxy without reinterpretation.
type consoleCoreAPIError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    string  `json:"code"`
	Param   *string `json:"param"`
}

func consoleCoreError(w http.ResponseWriter, status int, code, message string) {
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	}
	authJSON(w, status, struct {
		Error consoleCoreAPIError `json:"error"`
	}{
		Error: consoleCoreAPIError{Message: message, Type: kind, Code: code},
	})
}

// The namespace controls local error formatting only. It does not authorize a
// request or classify a bare/retired path as a proxyable Core operation.
func consoleCoreNamespace(r *http.Request) bool {
	return r.URL.Path == "/core" || strings.HasPrefix(r.URL.Path, "/core/")
}
