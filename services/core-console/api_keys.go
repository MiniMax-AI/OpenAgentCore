package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const consoleAPIKeysPath = "/console/api-keys"
const coreAPIKeysPath = "/core/v1/project-api-keys/"

func consoleAPIKeysRequest(r *http.Request) bool {
	return r.URL.Path == consoleAPIKeysPath || strings.HasPrefix(r.URL.Path, consoleAPIKeysPath+"/")
}

// This predicate is used only by the server-side bridge's rewritten request.
// Incoming /core paths still pass the existing finite route admission first.
func projectKeyAdminRequest(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, coreAPIKeysPath) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, coreAPIKeysPath), "/")
	if len(parts) == 1 {
		return r.Method == http.MethodGet || r.Method == http.MethodPost
	}
	return len(parts) == 2 && r.Method == http.MethodDelete
}

func (h *console) serveAPIKeys(w http.ResponseWriter, r *http.Request) {
	if h.adminToken == "" {
		authError(w, http.StatusServiceUnavailable, "API key management requires a paired deployment administrator")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		authError(w, http.StatusBadRequest, "API key management does not accept query parameters")
		return
	}
	suffix := ""
	if r.URL.Path == consoleAPIKeysPath {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			authError(w, http.StatusMethodNotAllowed, "Use GET or POST")
			return
		}
	} else {
		id := strings.TrimPrefix(r.URL.Path, consoleAPIKeysPath+"/")
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			authError(w, http.StatusNotFound, "Unknown API key")
			return
		}
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			authError(w, http.StatusMethodNotAllowed, "Use DELETE")
			return
		}
		suffix = "/" + id
	}
	digest := sha256.Sum256([]byte(h.token))
	forward := r.Clone(r.Context())
	forward.URL.Path = coreAPIKeysPath + hex.EncodeToString(digest[:]) + suffix
	forward.URL.RawPath, forward.URL.RawQuery = "", ""
	forward.URL.ForceQuery = false
	forward.Body = http.MaxBytesReader(w, r.Body, 4096)
	h.proxy.ServeHTTP(w, forward)
}
