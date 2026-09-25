package main

import (
	"net/http"
	"strings"
)

// coreDirectRequest reports the application (/v1) and machine connection
// (/api/v1) namespaces. The reverse proxy routes them straight to Core; the
// console never forwards them, whatever credential they carry.
func coreDirectRequest(r *http.Request) bool {
	for _, prefix := range []string{"/v1", "/api/v1"} {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			return true
		}
	}
	return false
}

// Only deployment administration is exposed through the console.
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
			return r.Method == http.MethodGet || r.Method == http.MethodPatch || r.Method == http.MethodDelete
		}
		return len(parts) == 3 && parts[2] == "allocations" && r.Method == http.MethodGet
	}
	return false
}
