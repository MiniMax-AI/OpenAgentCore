package api

import (
	"context"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

// Installation reports Core's installation facts. Core reads them from its
// environment and build; configuration is the process settings it loaded.
type Installation struct {
	Object string `json:"object" enums:"core.installation" binding:"required"`
	// The ID in OAC_INSTALLATION_ID_FILE.
	InstallationID string `json:"installation_id" binding:"required"`
	// OAC_PUBLIC_URL: the origin applications, nodes, sandboxes and self-hosted executors use.
	PublicURL string `json:"public_url" binding:"required"`
	// public_url followed by /v1.
	APIBaseURL string `json:"api_base_url" binding:"required"`
	// True when public_url names a loopback host, reachable only from the Core host.
	LocalOnly bool `json:"local_only" binding:"required"`
	// Full source commit Core was built from; null for development builds.
	SourceCommit *string `json:"source_commit" extensions:"x-nullable" binding:"required"`
	// The process settings Core loaded.
	Configuration   InstallationConfiguration  `json:"configuration" binding:"required"`
	AddressBindings deployment.AddressBindings `json:"address_bindings" binding:"required"`
}

// InstallationConfiguration is the process settings Core loaded.
type InstallationConfiguration struct {
	Settings []InstallationSetting `json:"settings" binding:"required"`
}

type InstallationSetting struct {
	// Dotted config.json key, such as ports.core.
	Key string `json:"key" binding:"required"`
	// Applied value; always null for a sensitive setting.
	Value   any `json:"value" extensions:"x-nullable" binding:"required"`
	Default any `json:"default" extensions:"x-nullable" binding:"required"`
	// Present only for a sensitive setting: whether it has a value.
	Configured *bool `json:"configured,omitempty"`
	// False for settings fixed at installation.
	Changeable bool `json:"changeable" binding:"required"`
	Sensitive  bool `json:"sensitive" binding:"required"`
	// Services that restart when the setting changes.
	Restarts []string `json:"restarts" enums:"core,web" binding:"required"`
}

// InstallationBindings counts what is bound to the current public URL.
type InstallationBindings interface {
	AddressBindings(context.Context) (deployment.AddressBindings, error)
}

// @Summary Retrieve installation facts and process settings
// @Description Core key only; available before any sandbox deployment exists. Reports the public URL that applications, nodes, sandboxes and self-hosted executors use, the API base URL, Core's source commit and installation ID, the process settings Core loaded, and what is bound to the current public URL. Sensitive settings report only whether they are configured.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} api.Installation
// @Failure 401,500 {object} CoreErrorResponse
// @Router /core/v1/installation [get]
func (h *Handler) getInstallation(w http.ResponseWriter, r *http.Request) {
	bindings, err := h.InstallationBindings.AddressBindings(r.Context())
	if err != nil {
		writeDeploymentError(w, r, err)
		return
	}
	value := h.Installation
	value.AddressBindings = bindings
	writeJSON(w, http.StatusOK, value)
}
