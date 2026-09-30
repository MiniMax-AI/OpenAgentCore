package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	sandboxdocker "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/moby/moby/client"
)

func buildDocker(config Config, _ LocalOptions, result *Built) (func(), error) {
	closeProvider := func() {}
	if config.Docker == nil || config.Microsandbox != nil {
		return closeProvider, errors.New("managed Docker requires only the docker configuration object")
	}
	entry := config.Docker
	host, err := url.Parse(entry.Host)
	if err != nil || host.Scheme != "unix" || host.Host != "" || host.User != nil || host.RawQuery != "" || host.Fragment != "" || host.RawPath != "" || host.Path == "/" || !filepath.IsAbs(host.Path) || filepath.Clean(host.Path) != host.Path || entry.Host != "unix://"+host.Path {
		return closeProvider, errors.New("managed Docker host must be an explicit canonical unix socket")
	}
	seccomp, err := os.ReadFile(entry.SeccompFile)
	if err != nil {
		return closeProvider, fmt.Errorf("cannot read managed Docker seccomp JSON: %w", err)
	}
	if !json.Valid(seccomp) {
		return closeProvider, errors.New("invalid managed Docker seccomp JSON")
	}
	c, err := client.New(client.WithHost(entry.Host))
	if err != nil {
		return closeProvider, errors.New("invalid managed Docker endpoint")
	}
	closeProvider = func() { _ = c.Close() }
	provider, err := sandboxdocker.New(c, sandboxdocker.Config{InstallationID: config.InstallationID, Image: entry.Image, Network: entry.Network, Seccomp: string(seccomp), ExtraHosts: entry.ExtraHosts, NestedSandbox: entry.NestedSandbox, Resources: &config.Specification.Resources})
	if err != nil {
		closeProvider()
		return func() {}, errors.New("invalid managed Docker provider configuration")
	}
	result.Provider = provider
	result.Probe = dockerProbe(c, entry.Image, config.Specification.Resources)
	result.BackendFingerprint = BackendFingerprint(config.Provider, entry.Host)
	return closeProvider, nil
}
