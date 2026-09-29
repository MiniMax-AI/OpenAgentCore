package main

import (
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
)

// daemonRoutes are the Runtime transport handlers served beside the API. Each
// authenticates its own callers.
type daemonRoutes struct {
	gateway, enrollment, connection, nodeConnect http.Handler
}

// serverHandler composes the daemon transport routes with the API handler.
// Path canonicalization wraps the whole composition, so the ServeMux, the API
// router and every middleware decide on the same canonical path, and the
// ServeMux never redirects a non-canonical path. Its only remaining redirect is
// the exact daemon prefix /api/v1/agent-daemon to /api/v1/agent-daemon/. The API
// handler canonicalizes again when served alone; the operation is idempotent.
func serverHandler(apiHandler http.Handler, daemon *daemonRoutes) http.Handler {
	if daemon == nil {
		return apiHandler
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/agent-daemon/", daemon.gateway)
	mux.Handle("/api/v1/agent-daemon/enroll", daemon.enrollment)
	mux.Handle("/api/v1/agent-daemon/connection", daemon.connection)
	mux.Handle("/api/v1/agent-daemon/install/", apiHandler)
	mux.Handle("/api/v1/agent-daemon/installation", apiHandler)
	mux.Handle("/api/v1/agent-daemon/installation/", apiHandler)
	if daemon.nodeConnect != nil {
		mux.Handle("/api/v1/sandbox-node/connect", daemon.nodeConnect)
	}
	mux.Handle("/", apiHandler)
	return api.CanonicalPaths(mux)
}
