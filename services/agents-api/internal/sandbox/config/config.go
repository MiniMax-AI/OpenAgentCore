// Package config constructs the selected node-local sandbox adapter.
package config

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

	"context"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	sandboxdocker "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/docker"
	"github.com/google/uuid"
	"github.com/moby/moby/client"
	"time"
)

type Config struct {
	Nodes          *Nodes        `json:"nodes,omitempty"`
	CoreURL        string        `json:"core_url"`
	Provider       string        `json:"provider"`
	InstallationID string        `json:"installation_id"`
	Maintenance    bool          `json:"maintenance"`
	Docker         *Docker       `json:"docker,omitempty"`
	Microsandbox   *Microsandbox `json:"microsandbox,omitempty"`
}

// Nodes controls local participation and, for remote-only deployments, the
// deployment idle policy. Node capacity remains a per-host reservation bound.
type Nodes struct {
	Local            bool  `json:"local"`
	MaxActive        int   `json:"max_active"`
	MaxRetained      int   `json:"max_retained"`
	IdleSeconds      int64 `json:"idle_seconds,omitempty"`
	RetentionSeconds int64 `json:"retention_seconds,omitempty"`
}

type Docker struct {
	Host          string   `json:"host"`
	Image         string   `json:"image"`
	Network       string   `json:"network"`
	SeccompFile   string   `json:"seccomp_file"`
	ExtraHosts    []string `json:"extra_hosts"`
	NestedSandbox bool     `json:"nested_sandbox"`
}

// Load rejects unknown fields, mixed adapters and explicit null configuration.
func Load(file string) (Config, error) {
	var config Config
	raw, err := os.ReadFile(file)
	if err != nil {
		return config, errors.New("cannot read sandbox configuration")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return config, errors.New("invalid sandbox configuration")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return config, errors.New("invalid sandbox configuration")
	}
	_, docker := fields["docker"]
	_, micro := fields["microsandbox"]
	if docker && micro {
		return config, errors.New("only one sandbox provider can be configured")
	}
	if v, ok := fields["maintenance"]; ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return config, errors.New("maintenance must be a boolean")
	}
	if v, ok := fields["nodes"]; ok {
		var n map[string]json.RawMessage
		if json.Unmarshal(v, &n) != nil || n == nil {
			return config, errors.New("nodes must be an object")
		}
		if local, exists := n["local"]; !exists || bytes.Equal(bytes.TrimSpace(local), []byte("null")) {
			return config, errors.New("nodes.local must be an explicit boolean")
		}
	}
	return config, nil
}

type Policy struct {
	IdleTimeout, Retention time.Duration
	MaxActive, MaxRetained int
}
type Built struct {
	Provider                           sandbox.Provider
	InstallationID, BackendFingerprint string
	Suspension                         *Policy
	Probe                              func(context.Context) error
}

func Build(config Config) (*Built, func(), error) {
	closeProvider := func() {}
	id, err := uuid.Parse(config.InstallationID)
	if err != nil || id == uuid.Nil || id.String() != config.InstallationID {
		return nil, closeProvider, errors.New("sandbox requires a canonical installation_id UUID")
	}
	if config.Provider != "docker" && config.Provider != "microsandbox" {
		return nil, closeProvider, errors.New("sandbox provider must be docker or microsandbox")
	}
	if n := config.Nodes; n != nil {
		if n.MaxActive < 1 || n.MaxRetained < n.MaxActive {
			return nil, closeProvider, errors.New("nodes capacity requires max_retained >= max_active > 0")
		}
		if !n.Local {
			if config.Docker != nil || config.Microsandbox != nil {
				return nil, closeProvider, errors.New("remote-only Core cannot configure a local provider")
			}
			built := &Built{InstallationID: config.InstallationID, BackendFingerprint: BackendFingerprint(config.Provider, "nodes:"+config.InstallationID)}
			if config.Provider == "microsandbox" {
				const maxSeconds = int64((1<<63 - 1) / time.Second)
				if n.IdleSeconds < 1 || n.RetentionSeconds < 1 || n.IdleSeconds > maxSeconds || n.RetentionSeconds > maxSeconds {
					return nil, closeProvider, errors.New("remote microsandbox requires positive bounded idle_seconds and retention_seconds")
				}
				built.Suspension = &Policy{IdleTimeout: time.Duration(n.IdleSeconds) * time.Second, Retention: time.Duration(n.RetentionSeconds) * time.Second, MaxActive: n.MaxActive, MaxRetained: n.MaxRetained}
			} else if n.IdleSeconds != 0 || n.RetentionSeconds != 0 {
				return nil, closeProvider, errors.New("Docker does not support suspension policy")
			}
			return built, closeProvider, nil
		}
		if n.IdleSeconds != 0 || n.RetentionSeconds != 0 {
			return nil, closeProvider, errors.New("local suspension policy belongs to microsandbox configuration")
		}
	}
	hasDocker, hasMicrosandbox := config.Docker != nil, config.Microsandbox != nil
	result := &Built{InstallationID: config.InstallationID}
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
		result.Probe = func(ctx context.Context) error {
			_, err := c.Ping(ctx, client.PingOptions{})
			if err != nil {
				return errors.New("Docker daemon is unavailable")
			}
			_, err = c.ImageInspect(ctx, entry.Image)
			if err != nil {
				return errors.New("pinned Docker image is unavailable")
			}
			return nil
		}
		result.BackendFingerprint = BackendFingerprint(config.Provider, entry.Host)
	case "microsandbox":
		if config.Microsandbox == nil || !hasMicrosandbox || hasDocker {
			return nil, closeProvider, errors.New("managed microsandbox requires only the microsandbox configuration object")
		}
		if err := configureMicrosandbox(*config.Microsandbox, result); err != nil {
			return nil, closeProvider, err
		}
	default:
		return nil, closeProvider, errors.New("managed provider must be docker or microsandbox")
	}
	return result, closeProvider, nil
}

func BackendFingerprint(kind, namespace string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + namespace))
	return hex.EncodeToString(digest[:])
}
