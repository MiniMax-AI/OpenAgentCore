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
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

// selfHostedCredentialPath is the daemon's credential file, relative to /home.
const selfHostedCredentialPath = "runtime/.parsar/parsar-daemon/executor-key.json"

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
	if !validID(v.InstallationID) || !validID(v.EnvironmentID) || !v.Credential.restrictedTo(v.EnvironmentID) {
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

func (c ExecutorCredential) restrictedTo(environment string) bool {
	return validID(c.KeyID) && c.EnvironmentID == environment && c.Token != "" && len(c.Token) <= 16384 && !strings.ContainsAny(c.Token, " \t\r\n\x00")
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
	// --self-hosted-install makes the daemon name this installer's rerun as the fix for a rejection.
	options.Config.Cmd = []string{"connect", "--profile", "default", "--remote", v.RemoteURL, "--environment-id", v.EnvironmentID, "--credential-file", "/home/" + selfHostedCredentialPath, "--self-hosted-install"}
	options.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	created, err := c.ContainerCreate(ctx, options)
	if err != nil {
		return name, errors.New("cannot create Runtime container; retain partial state")
	}
	credential, _ := json.Marshal(v.Credential)
	if err = copyRuntimeFiles(ctx, c, created.ID, "/home", []entry{
		{name: "runtime", directory: true}, {name: "runtime/.parsar", directory: true},
		{name: "runtime/.parsar/parsar-daemon", directory: true},
		{name: selfHostedCredentialPath, content: credential},
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

// ReplaceSelfHostedCredential writes a rotated executor credential into a
// stopped user-owned Runtime, keeping its container, volumes and native history.
// The container must carry this installation's labels and name for the
// credential's Environment; a running or unlabeled container is refused.
func ReplaceSelfHostedCredential(ctx context.Context, c *client.Client, name string, credential ExecutorCredential) error {
	if c == nil || !validID(credential.EnvironmentID) || !credential.restrictedTo(credential.EnvironmentID) {
		return sandbox.ErrInvalid
	}
	inspected, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return errors.New("cannot inspect Docker Runtime")
	}
	current := inspected.Container
	if err = selfHostedOwned(current, name, credential.EnvironmentID); err != nil {
		return err
	}
	if current.State.Status != container.StateExited && current.State.Status != container.StateCreated {
		return errors.New("stop the Runtime before replacing its executor credential")
	}
	// A planted directory symlink would redirect the write out of the private home.
	for _, path := range []string{"/home/runtime", "/home/runtime/.parsar", "/home/runtime/.parsar/parsar-daemon"} {
		stat, err := c.ContainerStatPath(ctx, current.ID, client.ContainerStatPathOptions{Path: path})
		if err != nil || !stat.Stat.Mode.IsDir() || stat.Stat.LinkTarget != "" {
			return errors.New("the Runtime's private credential directory is missing or redirected")
		}
	}
	raw, _ := json.Marshal(credential)
	if err = copyRuntimeFiles(ctx, c, current.ID, "/home", []entry{{name: selfHostedCredentialPath, content: raw}}); err != nil {
		return errors.New("cannot replace the Runtime executor credential; inspect the retained container")
	}
	return nil
}

// selfHostedOwned requires the labels, name and exactly the volume layout that
// LaunchSelfHosted creates, so a container copying the name and labels cannot
// direct the credential into another volume.
func selfHostedOwned(current container.InspectResponse, name, environment string) error {
	if current.Config == nil || current.State == nil || current.HostConfig == nil {
		return sandbox.ErrOwnership
	}
	labels := current.Config.Labels
	owner := SelfHostedLaunch{InstallationID: labels[labelPrefix+"installation"], EnvironmentID: labels[labelPrefix+"environment"]}
	if labels[labelPrefix+"user-owned"] != "true" || !validID(owner.InstallationID) || owner.EnvironmentID != environment || owner.Name() != name {
		return sandbox.ErrOwnership
	}
	want := map[string]string{"/home": name + "-home", "/environment": name + "-environment", "/workspace": name + "-environment"}
	if len(current.Mounts) != len(want) {
		return sandbox.ErrOwnership
	}
	for _, m := range current.Mounts {
		if m.Type != mount.TypeVolume || m.Name == "" || want[m.Destination] != m.Name {
			return sandbox.ErrOwnership
		}
		delete(want, m.Destination)
	}
	for path := range current.HostConfig.Tmpfs {
		if path != "/tmp" {
			return sandbox.ErrOwnership
		}
	}
	return nil
}
