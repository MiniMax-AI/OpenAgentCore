package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// Native is the node configuration's native object for Docker. Image is the
// local ID of the release's Runtime image: the host's image store decides
// whether the config digest (image_id) or the manifest digest
// (image_manifest_digest) names the loaded image, so the installer records the
// one this host resolves.
type Native struct {
	Host          string   `json:"host"`
	Image         string   `json:"image"`
	Network       string   `json:"network"`
	SeccompFile   string   `json:"seccomp_file"`
	ExtraHosts    []string `json:"extra_hosts"`
	NestedSandbox bool     `json:"nested_sandbox"`
}

func decodeNative(config sandbox.NodeConfig) (Native, error) {
	var entry Native
	if sandbox.DecodeConfigurationObject(config.Native, &entry, "host", "image", "network", "seccomp_file", "extra_hosts", "nested_sandbox") != nil {
		return entry, errors.New("invalid managed Docker node configuration")
	}
	release := config.Specification.Runtime
	if entry.Image != release.ImageID && entry.Image != release.ImageManifestDigest {
		return entry, errors.New("Docker Runtime image differs from the deployment release")
	}
	return entry, nil
}

// BuildNode constructs the node-local Docker adapter from a validated node configuration.
func BuildNode(config sandbox.NodeConfig, _ sandbox.LocalOptions, result *sandbox.Built) (func(), error) {
	closeProvider := func() {}
	entry, err := decodeNative(config)
	if err != nil {
		return closeProvider, err
	}
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
	provider, err := New(c, Config{InstallationID: config.InstallationID, Image: entry.Image, Network: entry.Network, Seccomp: string(seccomp), ExtraHosts: entry.ExtraHosts, NestedSandbox: entry.NestedSandbox, Resources: &config.Specification.Resources})
	if err != nil {
		closeProvider()
		return func() {}, errors.New("invalid managed Docker provider configuration")
	}
	result.Provider = provider
	result.Probe = dockerProbe(c, entry.Image, config.Specification.Resources)
	result.BackendFingerprint = sandbox.BackendFingerprint(config.Provider, entry.Host)
	return closeProvider, nil
}

// The probe reports its first failed check as its readiness class: the Docker
// daemon, then limit support, then host capacity for one sandbox of the
// deployment specification, then the pinned Runtime image. Unclassified
// failures stay provider_unavailable. The returned text is local; only its
// class is reported.
func dockerProbe(c *client.Client, image string, resources sandbox.Resources) func(context.Context) error {
	return func(ctx context.Context) error {
		if _, err := c.Ping(ctx, client.PingOptions{}); err != nil {
			return fmt.Errorf("%w: Docker daemon is unreachable", sandbox.ErrProviderUnavailable)
		}
		host, err := c.Info(ctx, client.InfoOptions{})
		if err != nil {
			return fmt.Errorf("%w: cannot inspect Docker host resource support", sandbox.ErrProviderUnavailable)
		}
		if !host.Info.MemoryLimit || !host.Info.CPUCfsQuota {
			return fmt.Errorf("%w: Docker does not enforce CPU and memory limits", sandbox.ErrHostUnsupported)
		}
		if host.Info.MemTotal <= 0 {
			return errors.New("Docker host memory capacity is unavailable")
		}
		if err := sandbox.CheckCapacity(resources, host.Info.NCPU, uint64(host.Info.MemTotal)); err != nil {
			return err
		}
		if _, err = c.ImageInspect(ctx, image); errdefs.IsNotFound(err) {
			return sandbox.ErrRuntimeImageUnavailable
		} else if err != nil {
			// The daemon did not answer; the image may still be present.
			return fmt.Errorf("%w: cannot inspect the pinned Runtime image", sandbox.ErrProviderUnavailable)
		}
		return nil
	}
}
