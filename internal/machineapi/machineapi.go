// Package machineapi is the code of the machine connection API: every request and response body of Core's /api/v1 routes. Core's handlers, the daemon and the sandbox node all use these types. The routes, credentials and errors are in contracts/agents-api/machine-api.md.
package machineapi

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// InstallGrantEnv names the environment variable that carries the short-lived installation grant from Core's command to the installer. It never carries an executor credential.
const InstallGrantEnv = "OAC_INSTALL_GRANT"

// InstallationContext answers POST agent-daemon/installation: the frozen connection constraints of one Session. It is bootstrap metadata, not an execution protocol.
type InstallationContext struct {
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocol_version"`
	EnvironmentID   string `json:"environment_id"`
	RemoteURL       string `json:"remote_url"`
	Workspace       string `json:"workspace_directory"`
	Harness         string `json:"harness"`
}

// InstallationClaim is the body of POST agent-daemon/installation/claim.
type InstallationClaim struct {
	ExecutorToken string `json:"executor_token"`
}

// BootstrapRequest is the body of POST agent-daemon/bootstrap.
type BootstrapRequest struct {
	DeviceID string `json:"device_id"`
}

// BootstrapResponse answers POST agent-daemon/bootstrap.
type BootstrapResponse struct {
	DeviceID         string `json:"device_id"`
	WSURL            string `json:"ws_url"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
}

// DefaultHeartbeat applies when Core reports no positive heartbeat_seconds. A zero heartbeat would never ping and always hit the 60 second timeout.
const DefaultHeartbeat = 15 * time.Second

func (b BootstrapResponse) HeartbeatInterval() time.Duration {
	if b.HeartbeatSeconds <= 0 {
		return DefaultHeartbeat
	}
	return time.Duration(b.HeartbeatSeconds) * time.Second
}

// AuthError is the body of every failed agent-daemon/bootstrap and agent-daemon/ws request.
type AuthError struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

// EnrollRequest is the body of POST agent-daemon/enroll.
type EnrollRequest struct {
	EnvironmentID string `json:"environment_id"`
}

// Enrollment answers POST agent-daemon/enroll: the device bound to the Environment's Session.
type Enrollment struct {
	DeviceID           string `json:"device_id"`
	SessionID          string `json:"session_id"`
	EnvironmentID      string `json:"environment_id"`
	WorkspaceDirectory string `json:"workspace_directory"`
}

// Validate checks that the binding names canonical IDs and the requested Environment.
func (e Enrollment) Validate(environment string) error {
	if !CanonicalUUID(e.DeviceID) || !CanonicalUUID(e.SessionID) || e.EnvironmentID != environment {
		return errors.New("machineapi: invalid enrollment binding")
	}
	return nil
}

// Connection answers GET agent-daemon/connection.
type Connection struct {
	EnvironmentID string `json:"environment_id"`
	Status        string `json:"status"`
}

const (
	ConnectionConnected    = "connected"
	ConnectionDisconnected = "disconnected"
)

// CanonicalUUID reports whether value is a non-nil UUID in canonical lower-case form.
func CanonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// NodeEnrollment is the body of POST sandbox-node/enroll.
type NodeEnrollment struct {
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	NodeID               string `json:"node_id"`
	Credential           string `json:"credential"`
	Name                 string `json:"name"`
	Provider             string `json:"provider"`
	BackendFingerprint   string `json:"backend_fingerprint"`
	// The Core origin this node stores and connects to, such as https://core.example. It must equal the installation public URL; otherwise enrollment gets 409 sandbox_node_address_mismatch and the token stays unused.
	CoreURL string `json:"core_url"`
	// Host measurements as in the node's health reports. Core derives a limit the enrollment token leaves unset from them.
	CPUCount           *int64   `json:"cpu_count,omitempty"`
	EffectiveCPUCores  *float64 `json:"effective_cpu_cores,omitempty"`
	TotalMemoryBytes   *int64   `json:"total_memory_bytes,omitempty"`
	AvailableDiskBytes *int64   `json:"available_disk_bytes,omitempty"`
}

// NodeIdentity answers POST sandbox-node/enroll.
type NodeIdentity struct {
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	NodeID               string `json:"node_id"`
	InstallationID       string `json:"installation_id"`
	Provider             string `json:"provider"`
	MaxActive            int    `json:"max_active"`
	MaxRetained          int    `json:"max_retained"`
}

// NodeStatus answers GET sandbox-node/identity: the authenticated node's identity and Core-owned presence.
type NodeStatus struct {
	NodeIdentity
	Connected     bool `json:"connected"`
	ProviderReady bool `json:"provider_ready"`
}
