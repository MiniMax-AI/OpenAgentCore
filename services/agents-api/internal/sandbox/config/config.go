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
	Specification  sandbox.DeploymentSpec `json:"specification"`
	Generation     uint64                 `json:"generation"`
	CoreURL        string                 `json:"core_url"`
	Provider       string                 `json:"provider"`
	InstallationID string                 `json:"installation_id"`
	Docker         *Docker                `json:"docker,omitempty"`
	Microsandbox   *Microsandbox          `json:"microsandbox,omitempty"`
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
	return config, nil
}

type Policy struct {
	IdleTimeout, Retention time.Duration
	MaxActive, MaxRetained int
}
type Built struct {
	SpecificationDigest                string
	Provider                           sandbox.Provider
	InstallationID, BackendFingerprint string
	Suspension                         *Policy
	Probe                              func(context.Context) error
}

func Build(config Config) (*Built, func(), error) {
	closeProvider := func() {}
	if err := validateSpecification(config); err != nil {
		return nil, closeProvider, err
	}
	id, err := uuid.Parse(config.InstallationID)
	if err != nil || id == uuid.Nil || id.String() != config.InstallationID {
		return nil, closeProvider, errors.New("sandbox requires a canonical installation_id UUID")
	}
	if config.Provider != "docker" && config.Provider != "microsandbox" {
		return nil, closeProvider, errors.New("sandbox provider must be docker or microsandbox")
	}

	hasDocker, hasMicrosandbox := config.Docker != nil, config.Microsandbox != nil
	result := &Built{InstallationID: config.InstallationID, SpecificationDigest: config.Specification.Digest(config.Provider)}
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
		provider, err := sandboxdocker.New(c, sandboxdocker.Config{InstallationID: config.InstallationID, Image: entry.Image, Network: entry.Network, Seccomp: string(seccomp), ExtraHosts: entry.ExtraHosts, NestedSandbox: entry.NestedSandbox, Resources: &config.Specification.Resources})
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
			host, err := c.Info(ctx, client.InfoOptions{})
			if err != nil {
				return errors.New("cannot inspect Docker host resource support")
			}
			if !host.Info.MemoryLimit || !host.Info.CPUCfsQuota {
				return errors.New("Docker host does not enforce CPU and memory limits")
			}
			if host.Info.MemTotal <= 0 {
				return errors.New("Docker host memory capacity is unavailable")
			}
			if err := checkCapacity(config.Specification.Resources, host.Info.NCPU, uint64(host.Info.MemTotal)); err != nil {
				return err
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
		probe := result.Probe
		result.Probe = func(ctx context.Context) error {
			if err := hostCapacity(config.Specification.Resources); err != nil {
				return err
			}
			return probe(ctx)
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
