package api

import (
	"net/http"
	"slices"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// WithStartupConfiguration exposes a defensive copy of startup facts already
// validated by composition. It must not contain raw operator configuration.
func WithStartupConfiguration(configuration v1.CoreStartupConfiguration) Option {
	return func(h *Handler) {
		copy := configuration
		copy.Supported.Harnesses = slices.Clone(configuration.Supported.Harnesses)
		copy.Supported.ManagedSandboxProviders = slices.Clone(configuration.Supported.ManagedSandboxProviders)
		copy.Configured.EnabledHarnesses = slices.Clone(configuration.Configured.EnabledHarnesses)
		copy.Configured.ModelProviders = slices.Clone(configuration.Configured.ModelProviders)
		if configuration.Configured.ManagedSandbox.Provider != nil {
			provider := *configuration.Configured.ManagedSandbox.Provider
			copy.Configured.ManagedSandbox.Provider = &provider
		}
		h.startup = &copy
	}
}

// getStartupConfiguration serves the administrator startup configuration read.
func (h *Handler) getStartupConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Core startup configuration does not accept query parameters.")
		return
	}
	if h.startup == nil {
		writeError(w, http.StatusServiceUnavailable, "startup_configuration_unavailable", "Core startup configuration is unavailable.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, h.startup)
}
