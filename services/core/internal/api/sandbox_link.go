package api

import "net/http"

// @Summary Open a sandbox Link
// @Description Upgrades to a WebSocket that carries the Sandbox link protocol. The Sandbox I/O service connects as the serve peer and the agent-host Runtime as the attach peer. The route takes no credential: each peer authenticates in its Link Hello, and the relay ends a link the Hello does not authenticate.
// @Tags Sandbox Link
// @Success 101 "Switching Protocols; the connection carries the Link"
// @Failure 400 "The request is not a WebSocket upgrade"
// @Failure 403 "The request carries an Origin other than its Host"
// @Router /api/v1/sandbox-link [get]
func (h *Handler) sandboxLink(w http.ResponseWriter, r *http.Request) {
	h.Execution.Links.ServeHTTP(w, r)
}
