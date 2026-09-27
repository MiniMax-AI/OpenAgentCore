package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Installation reports Core's installation facts. Core reads them from its
// environment and build; configuration is the installer's settings snapshot.
type Installation struct {
	Object string `json:"object" enums:"core.installation"`
	// OAC_INSTALLATION_ID; null when Core runs without the sandbox manager.
	InstallationID *string `json:"installation_id" extensions:"x-nullable"`
	// OAC_PUBLIC_URL: the origin applications, nodes, sandboxes and self-hosted executors use. Null when unset.
	PublicURL *string `json:"public_url" extensions:"x-nullable"`
	// public_url followed by /v1; null when public_url is null.
	APIBaseURL *string `json:"api_base_url" extensions:"x-nullable"`
	// True when public_url names a loopback host, reachable only from the Core host.
	LocalOnly bool `json:"local_only"`
	// Full source commit Core was built from; null for development builds.
	SourceCommit *string `json:"source_commit" extensions:"x-nullable"`
	// The installer's settings snapshot (OAC_SETTINGS_FILE); null when the installer did not start Core.
	Configuration   *InstallationConfiguration `json:"configuration" extensions:"x-nullable"`
	AddressBindings store.AddressBindings      `json:"address_bindings"`
}

// InstallationConfiguration says where process settings are changed and what
// the last applied values were. Sensitive values are never included.
type InstallationConfiguration struct {
	// Absolute host path of the installation's config.json.
	Path string `json:"path"`
	// Command that applies config.json changes.
	ApplyCommand string                `json:"apply_command"`
	AppliedAt    time.Time             `json:"applied_at"`
	Settings     []InstallationSetting `json:"settings"`
}

type InstallationSetting struct {
	// Dotted config.json key, such as ports.core.
	Key string `json:"key"`
	// Applied value; always null for a sensitive setting.
	Value   any `json:"value" extensions:"x-nullable"`
	Default any `json:"default" extensions:"x-nullable"`
	// Present only for a sensitive setting: whether it has a value.
	Configured *bool `json:"configured,omitempty"`
	// False for settings fixed at installation.
	Changeable bool `json:"changeable"`
	Sensitive  bool `json:"sensitive"`
	// Services that restart when the setting changes.
	Restarts []string `json:"restarts"`
}

var installationSettingKey = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

const maxInstallationSettings = 64 << 10

// ParseInstallationConfiguration validates the installer's settings snapshot.
// A sensitive setting that carries a value is rejected, so the snapshot cannot
// leak a secret through this read.
func ParseInstallationConfiguration(raw []byte) (*InstallationConfiguration, error) {
	invalid := errors.New("OAC_SETTINGS_FILE must contain the installer's settings snapshot")
	if len(raw) > maxInstallationSettings {
		return nil, invalid
	}
	var value InstallationConfiguration
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, invalid
	}
	if !filepath.IsAbs(value.Path) || value.ApplyCommand == "" || len(value.ApplyCommand) > 4096 || value.AppliedAt.IsZero() || value.Settings == nil {
		return nil, invalid
	}
	seen := make(map[string]bool, len(value.Settings))
	for _, setting := range value.Settings {
		if !installationSettingKey.MatchString(setting.Key) || seen[setting.Key] || setting.Restarts == nil ||
			setting.Sensitive != (setting.Configured != nil) || (setting.Sensitive && (setting.Value != nil || setting.Default != nil)) {
			return nil, invalid
		}
		for _, service := range setting.Restarts {
			if !slices.Contains([]string{"core", "web", "database"}, service) {
				return nil, invalid
			}
		}
		seen[setting.Key] = true
	}
	return &value, nil
}

// WithInstallation serves GET /core/v1/installation. bindings counts what is
// bound to the current public URL.
func WithInstallation(value Installation, bindings func(context.Context) (store.AddressBindings, error)) Option {
	return func(h *Handler) {
		value.Object = "core.installation"
		h.installation, h.installationBindings = &value, bindings
	}
}

// @Summary Retrieve installation facts and process settings
// @Description Core key only; available before any sandbox deployment exists. Reports the public URL that applications, nodes, sandboxes and self-hosted executors use, the API base URL, Core's source commit and installation ID, the installer's settings snapshot with where to change it, and what is bound to the current public URL. Sensitive settings report only whether they are configured.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} api.Installation
// @Failure 401,500 {object} v1.ErrorResponse
// @Router /core/v1/installation [get]
func (h *Handler) getInstallation(w http.ResponseWriter, r *http.Request) {
	bindings, err := h.installationBindings(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	value := *h.installation
	value.AddressBindings = bindings
	writeJSON(w, http.StatusOK, value)
}
