package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerCoreRoutes serves /core/v1 to Core Web's server and operator scripts.
// Every request, including one for an unknown path, must carry the Core key;
// Project API keys and machine credentials are rejected here.
func (h *Handler) registerCoreRoutes(router chi.Router) {
	router.Route("/core/v1", func(r chi.Router) {
		r.Use(coreErrorResponses)
		r.Use(h.CoreKeys.authenticate)
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not_found", "This Core operation does not exist.")
		})
		r.Get("/installation", h.getInstallation)
		h.registerProjectAPIKeyRoutes(r)
		h.registerAdminResourceRoutes(r)
		h.registerExecutorCredentialRoutes(r)
		h.registerSandboxManagerRoutes(r)
		h.registerHarnessRoutes(r)
	})
}
