package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	sandboxdocker "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/docker"
	sandboxmicrosandbox "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
	"github.com/google/uuid"
	"github.com/moby/moby/client"
)

type managedRuntimeConfig struct {
	CoreURL        string                     `json:"core_url"`
	Provider       string                     `json:"provider"`
	InstallationID string                     `json:"installation_id"`
	Maintenance    bool                       `json:"maintenance"`
	Docker         *managedDockerConfig       `json:"docker,omitempty"`
	Microsandbox   *managedMicrosandboxConfig `json:"microsandbox,omitempty"`
}

type managedDockerConfig struct {
	Host          string   `json:"host"`
	Image         string   `json:"image"`
	Network       string   `json:"network"`
	SeccompFile   string   `json:"seccomp_file"`
	ExtraHosts    []string `json:"extra_hosts"`
	NestedSandbox bool     `json:"nested_sandbox"`
}

// One deployment owns one explicit backend. Maintenance keeps that adapter
// available for existing resources while Core blocks new compute admission.
func managedRuntimes() (*execution.RuntimeProvider, func(), error) {
	closeProvider := func() {}
	file := os.Getenv("AGENTS_API_MANAGED_RUNTIMES_FILE")
	if file == "" {
		return nil, closeProvider, nil
	}
	if os.Getenv("AGENTS_API_DAEMON_WS_URL") == "" {
		return nil, closeProvider, errors.New("managed Runtime configuration requires AGENTS_API_DAEMON_WS_URL")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, closeProvider, errors.New("cannot read AGENTS_API_MANAGED_RUNTIMES_FILE")
	}
	var config managedRuntimeConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, closeProvider, errors.New("invalid managed Runtime configuration")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, closeProvider, errors.New("invalid managed Runtime configuration")
	}
	_, hasDocker := fields["docker"]
	_, hasMicrosandbox := fields["microsandbox"]
	if maintenance, exists := fields["maintenance"]; exists && bytes.Equal(bytes.TrimSpace(maintenance), []byte("null")) {
		return nil, closeProvider, errors.New("managed maintenance must be a boolean")
	}
	id, err := uuid.Parse(config.InstallationID)
	if err != nil || id == uuid.Nil || id.String() != config.InstallationID {
		return nil, closeProvider, errors.New("managed Runtime requires a canonical installation_id UUID")
	}
	result := &execution.RuntimeProvider{CoreURL: config.CoreURL, InstallationID: config.InstallationID, Maintenance: config.Maintenance}
	switch config.Provider {
	case "docker":
		if config.Docker == nil || !hasDocker || hasMicrosandbox {
			return nil, closeProvider, errors.New("managed Docker requires only the docker configuration object")
		}
		entry := config.Docker
		host, err := url.Parse(entry.Host)
		if err != nil || host.Scheme != "unix" || host.Host != "" || host.User != nil || host.RawQuery != "" || host.Fragment != "" || host.RawPath != "" || host.Path == "/" || !filepath.IsAbs(host.Path) || filepath.Clean(host.Path) != host.Path || entry.Host != "unix://"+host.Path {
			return nil, closeProvider, errors.New("managed Docker host must be an explicit canonical unix socket")
		}
		seccomp, err := os.ReadFile(entry.SeccompFile)
		if err != nil || !json.Valid(seccomp) {
			return nil, closeProvider, errors.New("cannot read managed Docker seccomp JSON")
		}
		c, err := client.New(client.WithHost(entry.Host))
		if err != nil {
			return nil, closeProvider, errors.New("invalid managed Docker endpoint")
		}
		closeProvider = func() { _ = c.Close() }
		provider, err := sandboxdocker.New(c, sandboxdocker.Config{InstallationID: config.InstallationID, Image: entry.Image, Network: entry.Network, Seccomp: string(seccomp), ExtraHosts: entry.ExtraHosts, NestedSandbox: entry.NestedSandbox})
		if err != nil {
			closeProvider()
			return nil, func() {}, errors.New("invalid managed Docker provider configuration")
		}
		result.Provider = provider
		result.BackendFingerprint = managedBackendFingerprint(config.Provider, entry.Host)
	case "microsandbox":
		if config.Microsandbox == nil || !hasMicrosandbox || hasDocker {
			return nil, closeProvider, errors.New("managed microsandbox requires only the microsandbox configuration object")
		}
		if err := configureManagedMicrosandbox(*config.Microsandbox, result); err != nil {
			return nil, closeProvider, err
		}
	default:
		return nil, closeProvider, errors.New("managed provider must be docker or microsandbox")
	}
	return result, closeProvider, nil
}

func managedRuntimeProviderKind(runtime *execution.RuntimeProvider) string {
	if runtime == nil {
		return ""
	}
	switch runtime.Provider.(type) {
	case *sandboxdocker.Provider:
		return "docker"
	case *sandboxmicrosandbox.Provider:
		return "microsandbox"
	default:
		return ""
	}
}

func managedBackendFingerprint(kind, namespace string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + namespace))
	return hex.EncodeToString(digest[:])
}
