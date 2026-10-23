package api

import (
	"context"
	"net/http"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/go-chi/chi/v5"
)

// ConfigurationDiscovery asks a Sandbox Provider which configuration values
// its credential can use.
type ConfigurationDiscovery interface {
	DiscoverConfiguration(ctx context.Context, provider string, input sandbox.ConfigurationDiscoveryInput) (sandbox.ConfigurationOptions, error)
}

// @Summary Discover sandbox provider configuration
// @Description Core key only. Uses transient write-only credentials. The provider validates configuration and query fields and returns the options of one Discovered setup field. Does not save credentials, change a deployment or allocate compute. Discovery is not deployment admission.
// @Tags Sandbox Manager
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param provider path string true "Registered provider kind"
// @Param body body sandbox.ConfigurationDiscoveryInput true "Transient provider connection and query"
// @Success 200 {object} sandbox.ConfigurationOptions
// @Failure 400,401,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/providers/{provider}/discovery [post]
func (h *Handler) discoverSandboxConfiguration(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBodyLimit(w, r, 64*1024, "Configuration discovery request is too large.")
	if !ok {
		return
	}
	var input sandbox.ConfigurationDiscoveryInput
	if decodeInputObject(raw, &input, "configuration", "credential", "query") != nil {
		writeDeploymentError(w, r, deployment.ErrInvalidInput)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := h.Sandboxes.ConfigurationDiscovery.DiscoverConfiguration(ctx, chi.URLParam(r, "provider"), input)
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
