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

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

// Installation reports Core's installation facts. Core reads them from its
// environment and build; configuration is the process settings it loaded.
type Installation struct {
	Object string `json:"object" enums:"core.installation"`
	// The ID in OAC_INSTALLATION_ID_FILE; null when Core runs without the sandbox manager.
	InstallationID *string `json:"installation_id" extensions:"x-nullable"`
	// OAC_PUBLIC_URL: the origin applications, nodes, sandboxes and self-hosted executors use. Null when unset.
	PublicURL *string `json:"public_url" extensions:"x-nullable"`
	// public_url followed by /v1; null when public_url is null.
	APIBaseURL *string `json:"api_base_url" extensions:"x-nullable"`
	// True when public_url names a loopback host, reachable only from the Core host.
	LocalOnly bool `json:"local_only"`
	// Full source commit Core was built from; null for development builds.
	SourceCommit *string `json:"source_commit" extensions:"x-nullable"`
	// The process settings Core loaded. path and apply_command are empty, and applied_at is null, because Core reports its environment rather than an installer file.
	Configuration   *InstallationConfiguration `json:"configuration" extensions:"x-nullable"`
	AddressBindings deployment.AddressBindings `json:"address_bindings"`
}

// InstallationConfiguration is the process settings Core loaded. Path and
// ApplyCommand are empty, and AppliedAt is null, unless a caller built a
// snapshot itself.
type InstallationConfiguration struct {
	// Absolute host path of config.json. Empty when Core reports its own environment.
	Path string `json:"path"`
	// Command that applies config.json changes. Empty when Core reports its own environment.
	ApplyCommand string `json:"apply_command"`
	// Null when Core reports its own environment.
	AppliedAt *time.Time            `json:"applied_at" extensions:"x-nullable"`
	Settings  []InstallationSetting `json:"settings"`
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
	invalid := errors.New("installation configuration is invalid")
	if len(raw) > maxInstallationSettings {
		return nil, invalid
	}
	var value InstallationConfiguration
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, invalid
	}
	if value.Path != "" && !filepath.IsAbs(value.Path) || len(value.ApplyCommand) > 4096 || value.Settings == nil {
		return nil, invalid
	}
	if value.Path != "" && (value.ApplyCommand == "" || value.AppliedAt == nil || value.AppliedAt.IsZero()) {
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
