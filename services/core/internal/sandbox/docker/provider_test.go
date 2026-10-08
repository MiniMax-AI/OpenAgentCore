package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
	"github.com/containerd/errdefs"
	"github.com/google/uuid"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

func TestProviderRejectsUnsafeOperatorConfiguration(t *testing.T) {
	c, e := client.New()
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	base := Config{InstallationID: uuid.NewString(), Image: "test@sha256:" + strings.Repeat("a", 64), Network: "bridge", Seccomp: `{}`}
	for _, change := range []func(*Config){func(c *Config) { c.Image = "mutable:latest" }, func(c *Config) { c.InstallationID = "" }, func(c *Config) { c.Network = "host" }, func(c *Config) { c.Network = "container:other" }, func(c *Config) { c.Seccomp = "" }} {
		v := base
		change(&v)
		if _, e := New(c, v); !errors.Is(e, sandbox.ErrInvalid) {
			t.Fatalf("accepted invalid configuration: %v", e)
		}
	}
}

// This optional Docker mechanism test runs AGENTS_RUNTIME_DOCKER_TEST_IMAGE,
// an image with oac-sandbox-io, such as the sandbox image. Its Link is
// unreachable, so the service keeps retrying and the container keeps running.
// It is not native or model acceptance.
func TestDockerProviderLifecycle(t *testing.T) {
	image := os.Getenv("AGENTS_RUNTIME_DOCKER_TEST_IMAGE")
	if image == "" {
		t.Skip("explicit Docker fixture image required")
	}
	seccomp, e := os.ReadFile("../../../deploy/codex/seccomp.json")
	if e != nil {
		t.Fatal(e)
	}
	c, e := client.New(client.FromEnv)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	installationID := uuid.NewString()
	p, e := New(c, Config{InstallationID: installationID, Image: image, Network: "bridge", Seccomp: string(seccomp)})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	bootstrap := func() sandbox.Bootstrap {
		b := contracttest.Bootstrap(sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()})
		b.SandboxIO.Credential = "synthetic-serve-credential"
		return b
	}
	b := bootstrap()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := p.Kill(ctx, b.Reference); e != nil {
			t.Error(e)
		}
	})
	info, e := p.Create(ctx, b)
	contracttest.AssertObservation(t, info, e, b.Reference, "", "running")
	resources, e := p.Observe(ctx, runtimeobs.Target{
		TenantID: b.TenantID, EnvironmentID: b.EnvironmentID, Mode: runtimeobs.ModeManaged,
		Instance: runtimeobs.Instance{AllocationID: b.AllocationID, ProviderKey: installationID},
	})
	if e != nil || resources.StartedAt == nil || resources.CPUUsageSecondsTotal == nil || resources.MemoryUsageBytes == nil || resources.CPUCapacityCores == nil || resources.MemoryLimitBytes == nil {
		t.Fatalf("bad resource observation: %+v %v", resources, e)
	}
	inspected, e := p.inspect(ctx, b.Reference)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(inspected.Raw), b.SandboxIO.Credential) || inspected.Container.Config.User != "1000:1000" || !inspected.Container.HostConfig.ReadonlyRootfs || inspected.Container.HostConfig.Privileged {
		t.Fatal("unsafe Docker configuration")
	}
	if got := strings.Join(inspected.Container.Config.Entrypoint, " ") + "|" + strings.Join(inspected.Container.Config.Cmd, " "); got != "/usr/local/bin/oac-sandbox-io --bootstrap-file /home/runtime/sandbox-io-bootstrap.json|" {
		t.Fatalf("container command %q", got)
	}
	changed := b
	changed.SandboxIO.Credential = "must-not-replace-existing"
	if _, e = p.Create(ctx, changed); !errors.Is(e, sandbox.ErrExists) {
		t.Fatalf("duplicate not rejected: %v", e)
	}
	run := func(script string) (string, int) {
		t.Helper()
		return execInContainer(t, ctx, c, info.ProviderID, script)
	}
	out, code := run("cat /home/runtime/sandbox-io-bootstrap.json")
	if serve, err := sandboxbootstrap.Decode([]byte(out)); code != 0 || err != nil || serve != b.SandboxIO {
		t.Fatal("Sandbox I/O bootstrap changed or malformed")
	}
	if out, code = run("ls -A /home/runtime; printf retained > /environment/workspace/history; cat /workspace/history"); code != 0 || out != "sandbox-io-bootstrap.json\nretained" {
		t.Fatalf("home or public workspace view: %q %d", out, code)
	}
	wrong := b.Reference
	wrong.TenantID = uuid.NewString()
	if _, e = p.GetInfo(ctx, wrong); !errors.Is(e, sandbox.ErrNotFound) {
		t.Fatal("foreign allocation visible")
	}
	if e = p.Kill(ctx, wrong); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Renew(ctx, b.Reference); e != nil {
		t.Fatal("wrong tenant removed the owner")
	}
	timeout := 1
	if _, e = c.ContainerRestart(ctx, info.ProviderID, client.ContainerRestartOptions{Timeout: &timeout}); e != nil {
		t.Fatal(e)
	}
	if out, code = run("cat /environment/workspace/history"); code != 0 || out != "retained" {
		t.Fatal("restart lost workspace")
	}
	if _, code = run("touch /cannot-write-root"); code == 0 {
		t.Fatal("root filesystem writable")
	}
	// Container loss must not trigger credential overwrite or state replacement.
	if _, e = c.ContainerRemove(ctx, info.ProviderID, client.ContainerRemoveOptions{Force: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Create(ctx, b); !errors.Is(e, sandbox.ErrExists) {
		t.Fatalf("retained volumes reused: %v", e)
	}
	if e = p.Kill(ctx, b.Reference); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"-home", "-environment"} {
		if _, e = c.VolumeInspect(ctx, p.name(b.Reference)+suffix, client.VolumeInspectOptions{}); !errdefs.IsNotFound(e) {
			t.Fatal("named volume remains")
		}
	}
	// A colliding resource with different ownership cannot be deleted, including
	// when its container is absent after a partial creation.
	foreign := bootstrap()
	volume := p.name(foreign.Reference) + "-home"
	if _, e = c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: volume, Labels: map[string]string{"fixture": "foreign"}}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, e := c.VolumeRemove(context.Background(), volume, client.VolumeRemoveOptions{})
		if e != nil {
			t.Error(e)
		}
	})
	if e = p.Kill(ctx, foreign.Reference); !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatal("foreign volume cleanup accepted")
	}
	if _, e = p.Create(ctx, foreign); !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatal("foreign volume bootstrap accepted")
	}
}

// execInContainer runs a shell script in the container as its user and
// returns its standard output and exit code.
func execInContainer(t *testing.T, ctx context.Context, c *client.Client, id, script string) (string, int) {
	t.Helper()
	created, err := c.ExecCreate(ctx, id, client.ExecCreateOptions{Cmd: []string{"/bin/sh", "-c", script}, AttachStdout: true, AttachStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	attached, err := c.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer attached.Close()
	var stdout bytes.Buffer
	if _, err = stdcopy.StdCopy(&stdout, io.Discard, attached.Reader); err != nil {
		t.Fatal(err)
	}
	status, err := c.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil || status.Running {
		t.Fatal("exec status unknown", err)
	}
	return stdout.String(), status.ExitCode
}
