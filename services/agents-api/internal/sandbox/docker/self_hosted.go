package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// SelfHostedLaunch is local installation input, never a public execution request.
// The caller retains the container and volumes; Core acquires no compute authority.
type SelfHostedLaunch struct {
	InstallationID, EnvironmentID, RemoteURL, Image, Seccomp string
	Credential                                               ExecutorCredential
}

type ExecutorCredential struct {
	KeyID         string `json:"key_id"`
	EnvironmentID string `json:"environment_id"`
	Token         string `json:"executor_token"`
}

func (v SelfHostedLaunch) Validate() error {
	u, err := url.Parse(v.RemoteURL)
	if err != nil || u.Scheme != "wss" || u.Hostname() == "" || u.User != nil || u.Path != "/api/v1/agent-daemon/ws" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.TrimSpace(v.RemoteURL) != v.RemoteURL {
		return errors.New("Docker Runtime requires the unchanged HTTPS-reachable wss Environment remote_url")
	}
	if !validID(v.InstallationID) || !validID(v.EnvironmentID) || !validID(v.Credential.KeyID) || v.Credential.EnvironmentID != v.EnvironmentID || v.Credential.Token == "" || len(v.Credential.Token) > 16384 || strings.ContainsAny(v.Credential.Token, " \t\r\n\x00") {
		return errors.New("invalid Environment identity or restricted executor credential")
	}
	if !strings.HasPrefix(v.Image, "sha256:") || len(v.Image) != 71 {
		return errors.New("Runtime image must be the distribution image digest")
	}
	if raw, err := hex.DecodeString(strings.TrimPrefix(v.Image, "sha256:")); err != nil || len(raw) != 32 || !json.Valid([]byte(v.Seccomp)) {
		return errors.New("invalid Runtime image digest or seccomp profile")
	}
	return nil
}

func (v SelfHostedLaunch) Name() string {
	hash := sha256.Sum256([]byte(v.InstallationID + ":" + v.EnvironmentID))
	return "parsar-selfhost-" + hex.EncodeToString(hash[:16])
}

// LaunchSelfHosted starts the packaged daemon's existing connect path using the
// same container isolation as managed V1. It never retries an uncertain launch,
// replaces compute, imports history or enrolls a sandbox node.
func LaunchSelfHosted(ctx context.Context, c *client.Client, v SelfHostedLaunch) (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	if c == nil {
		return "", sandbox.ErrInvalid
	}
	name := v.Name()
	if _, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); err == nil {
		return name, sandbox.ErrExists
	} else if !errdefs.IsNotFound(err) {
		return name, errors.New("cannot inspect Docker Runtime")
	}
	labels := map[string]string{labelPrefix + "user-owned": "true", labelPrefix + "installation": v.InstallationID, labelPrefix + "environment": v.EnvironmentID}
	for _, suffix := range []string{"-home", "-environment"} {
		if _, err := c.VolumeInspect(ctx, name+suffix, client.VolumeInspectOptions{}); err == nil {
			return name, sandbox.ErrExists
		} else if !errdefs.IsNotFound(err) {
			return name, errors.New("cannot inspect Runtime volumes")
		}
	}
	for _, suffix := range []string{"-home", "-environment"} {
		volume, err := c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name + suffix, Labels: labels})
		if err != nil {
			return name, errors.New("cannot create Runtime volume; retain partial state")
		}
		for key, value := range labels {
			if volume.Volume.Labels[key] != value {
				return name, sandbox.ErrOwnership
			}
		}
	}
	options := runtimeContainerOptions(Config{Image: v.Image, Network: "bridge", Seccomp: v.Seccomp, NestedSandbox: true}, name, labels, nil)
	options.Config.Cmd = []string{"connect", "--profile", "default", "--remote", v.RemoteURL, "--environment-id", v.EnvironmentID, "--credential-file", "/home/runtime/.parsar/parsar-daemon/executor-key.json"}
	options.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	created, err := c.ContainerCreate(ctx, options)
	if err != nil {
		return name, errors.New("cannot create Runtime container; retain partial state")
	}
	credential, _ := json.Marshal(v.Credential)
	if err = copyRuntimeFiles(ctx, c, created.ID, "/home", []entry{
		{name: "runtime", directory: true}, {name: "runtime/.parsar", directory: true},
		{name: "runtime/.parsar/parsar-daemon", directory: true},
		{name: "runtime/.parsar/parsar-daemon/executor-key.json", content: credential},
	}); err != nil {
		return name, errors.New("cannot initialize private Runtime credential; retain partial state")
	}
	if err = copyRuntimeFiles(ctx, c, created.ID, "/environment", []entry{
		{name: "workspace", directory: true}, {name: "staging", directory: true},
		{name: "initialization", directory: true}, {name: "packages", directory: true},
	}); err != nil {
		return name, errors.New("cannot initialize Runtime workspace; retain partial state")
	}
	if _, err = c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return name, errors.New("Runtime start outcome uncertain; inspect retained container")
	}
	return name, nil
}
