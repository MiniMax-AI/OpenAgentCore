package main

import (
	"net/http"
	"strings"
)

func publicAPIRequest(r *http.Request) bool {
	return r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/")
}

// Only deployment administration is exposed through the console. Enrollment and
// node identity/transport calls connect directly to Core with their own credentials.
func sandboxAdminRequest(r *http.Request) bool {
	const base = "/core/v1/sandbox/"
	if !strings.HasPrefix(r.URL.Path, base) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, base), "/")
	if len(parts) == 1 {
		switch parts[0] {
		case "deployment":
			return r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodPut
		case "nodes":
			return r.Method == http.MethodGet
		case "enrollment-tokens":
			return r.Method == http.MethodPost
		}
	}
	if len(parts) == 2 && parts[0] == "deployment" && parts[1] == "maintenance" {
		return r.Method == http.MethodPatch
	}
	if len(parts) >= 2 && parts[0] == "nodes" && parts[1] != "" {
		if len(parts) == 2 {
			return r.Method == http.MethodPatch || r.Method == http.MethodDelete
		}
		return len(parts) == 3 && parts[2] == "allocations" && r.Method == http.MethodGet
	}
	return false
}

func explicitBearer(r *http.Request) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	return len(r.Header.Values("Authorization")) == 1 && len(parts) == 2 && strings.EqualFold(parts[0], "Bearer")
}
