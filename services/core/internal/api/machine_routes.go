package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerMachineRoutes owns every machine HTTP route. Handlers retain their
// credential checks and method precedence; the API only composes them.
func (h *Handler) registerMachineRoutes(router chi.Router) {
	for _, prefix := range []string{"/api/v1/agent-daemon", "/api/v1/agent-daemon/install"} {
		router.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			target := prefix + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		})
	}
	router.Route("/api/v1", func(r chi.Router) {
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "", "404 page not found")
		})
		r.Post("/sandbox-node/enroll", h.enrollSandboxNode)
		r.Get("/sandbox-node/identity", h.sandboxNodeIdentity)
		r.Get("/sandbox-node/configuration", h.sandboxNodeConfiguration)

		// @Summary Open a sandbox node connection
		// @Description Authenticates a node credential before upgrading to the sandbox node wire protocol. Standard HTTP methods other than GET return 503 without authentication or upgrade. Unsupported extension methods are rejected by the shared router with 405 before authentication or upgrade.
		// @Tags Sandbox Nodes
		// @Param Authorization header string true "Bearer node credential"
		// @Param node_id query string true "Node UUID"
		// @Success 101 "Switching Protocols"
		// @Failure 400,401,403,409,500,503 {object} v1.ErrorResponse
		// @Router /api/v1/sandbox-node/connect [get]
		r.Handle("/sandbox-node/connect", h.Sandboxes.NodeConnect)

		// @Summary Bootstrap an agent-host Runtime
		// @Description Authenticates the agent-host credential and returns its WebSocket URL and heartbeat interval.
		// @Tags Runtime Daemon
		// @Accept json
		// @Produce json
		// @Param Authorization header string true "Bearer agent-host credential"
		// @Param body body runtimegateway.BootstrapRequest true "Agent-host identity"
		// @Success 200 {object} runtimegateway.BootstrapResponse
		// @Failure 400,401,403,500 {object} v1.ErrorResponse
		// @Router /api/v1/agent-daemon/bootstrap [post]
		r.Method(http.MethodPost, "/agent-daemon/bootstrap", h.Execution.Bootstrap)

		// @Summary Open an agent-host Runtime connection
		// @Description Authenticates the agent-host credential and exact Runtime protocol version before upgrading to the Core–Runtime wire protocol.
		// @Tags Runtime Daemon
		// @Param Authorization header string true "Bearer agent-host credential"
		// @Param device_id query string true "Agent-host device ID"
		// @Param version query string true "Runtime protocol version"
		// @Success 101 "Switching Protocols"
		// @Failure 400,401,403,426,500 {object} v1.ErrorResponse
		// @Router /api/v1/agent-daemon/ws [get]
		r.Method(http.MethodGet, "/agent-daemon/ws", h.Execution.RuntimeConnect)
		r.Head("/agent-daemon/ws", methodNotAllowed)

		// @Summary Enroll a self-hosted sandbox
		// @Description Accepts an executor credential and exactly one environment_id. Returns the Environment's Link URL and resource without issuing a credential. Queries and bodies over 4096 bytes are rejected.
		// @Tags Runtime Daemon
		// @Accept json
		// @Produce json
		// @Param Authorization header string true "Bearer executor credential"
		// @Param body body runtimeenrollment.EnrollmentRequest true "Environment identity"
		// @Success 200 {object} runtimeenrollment.EnrollmentResponse
		// @Failure 400,401,409,503 {object} v1.ErrorResponse
		// @Router /api/v1/agent-daemon/enroll [post]
		r.Handle("/agent-daemon/enroll", h.Execution.Enrollment)

		// @Summary Observe a self-hosted sandbox connection
		// @Description Rechecks executor authority around the live Link resource read. Never enrolls the sandbox or starts execution. Responses carry Cache-Control no-store.
		// @Tags Runtime Daemon
		// @Produce json
		// @Param Authorization header string true "Bearer executor credential"
		// @Param environment_id query string true "Environment UUID"
		// @Success 200 {object} runtimeenrollment.ConnectionResponse
		// @Failure 400,401,409,503 {object} v1.ErrorResponse
		// @Router /api/v1/agent-daemon/connection [get]
		r.Handle("/agent-daemon/connection", h.Execution.Connection)

		if installer := h.Execution.NativeInstaller; installer != nil {
			if installer.Catalog.NativeAvailable() {
				// @Summary Download native installation content
				// @Description Public versioned bootstrap script, checksum or Linux amd64 archive. An archive may redirect to its qualified release URL; local archives support conditional and range requests. GET and HEAD share the download headers.
				// @Tags Native Installation
				// @Produce plain,octet-stream
				// @Param path path string true "Version and filename: {version}/bootstrap.sh, {version}/linux-amd64.sha256 or {version}/linux-amd64.tar.gz"
				// @Success 200 {file} file
				// @Success 206 {file} file
				// @Success 304 "Not Modified"
				// @Success 307 "Temporary Redirect"
				// @Failure 400,403,404,405,416,500 {object} v1.ErrorResponse
				// @Router /api/v1/agent-daemon/install/{path} [get]
				// @Router /api/v1/agent-daemon/install/{path} [head]
				r.Handle("/agent-daemon/install/*", installer.Catalog)
			}
			r.Post("/agent-daemon/installation", h.prepareNativeInstallation)
			r.Post("/agent-daemon/installation/claim", h.claimNativeInstallation)
		}

		if h.Distribution != nil {
			// @Summary Download sandbox node installation content
			// @Description Public matched Linux amd64 installer, metadata or declared artifact. Versioned releases remain addressable; artifacts may redirect to pinned HTTPS release URLs. Local downloads support conditional and range requests.
			// @Tags Sandbox Nodes
			// @Produce octet-stream
			// @Param path path string true "Metadata filename, artifacts/{filename}, or releases/{source_commit}/{filename}"
			// @Success 200 {file} file
			// @Success 206 {file} file
			// @Success 304 "Not Modified"
			// @Success 307 "Temporary Redirect"
			// @Failure 400,403,404,405,416,500 {object} v1.ErrorResponse
			// @Router /api/v1/sandbox-node/install/{path} [get]
			// @Router /api/v1/sandbox-node/install/{path} [head]
			r.Get("/sandbox-node/install/*", h.Distribution.ServeNodeHTTP)
			r.Head("/sandbox-node/install/*", h.Distribution.ServeNodeHTTP)
		}

		// @Summary Open a sandbox Link
		// @Description Upgrades to a WebSocket that carries the Sandbox link protocol. The Sandbox I/O service connects as the serve peer and the agent-host Runtime as the attach peer. Each peer authenticates in its Link Hello after the upgrade.
		// @Tags Sandbox Link
		// @Success 101 "Switching Protocols; the connection carries the Link"
		// @Failure 400,403,500 {object} v1.ErrorResponse
		// @Router /api/v1/sandbox-link [get]
		r.Method(http.MethodGet, "/sandbox-link", h.Execution.Links)
		r.Head("/sandbox-link", methodNotAllowed)
	})
}
