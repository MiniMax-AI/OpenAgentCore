package v1

// SessionCore exposes optional Core additions without changing official fields.
type SessionCore struct {
	Installation *EnvironmentInstallation `json:"installation,omitempty"`
}

// EnvironmentInstallation contains short-lived, Environment-scoped commands.
// Only authenticated creation and detail responses include this authorization.
type EnvironmentInstallation struct {
	Status    string            `json:"status" enums:"available,unavailable"`
	Version   string            `json:"version"`
	ExpiresAt int64             `json:"expires_at,omitempty"`
	Commands  map[string]string `json:"commands,omitempty"`
	Message   string            `json:"message,omitempty"`
}

// NativeInstallationContext is bootstrap metadata, not an execution protocol.
type NativeInstallationContext struct {
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocol_version"`
	EnvironmentID   string `json:"environment_id"`
	RemoteURL       string `json:"remote_url"`
	Workspace       string `json:"workspace_directory"`
	Harness         string `json:"harness"`
}
