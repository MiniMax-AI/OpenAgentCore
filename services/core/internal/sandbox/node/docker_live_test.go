package node

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	sandboxdocker "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/google/uuid"
	"github.com/moby/moby/client"
)

// This uses the same pinned sleep-entrypoint image as the Docker mechanism
// tests. It exercises real Docker resources through the node transport, not a
// native harness/model workflow. No provider credentials are required.
func TestDockerNodeTransportLifecycle(t *testing.T) {
	image := os.Getenv("AGENTS_RUNTIME_DOCKER_TEST_IMAGE")
	if image == "" {
		t.Skip("explicit Docker fixture image required")
	}
	seccomp, err := os.ReadFile("../../../deploy/codex/seccomp.json")
	if err != nil {
		t.Fatal(err)
	}
	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer dockerClient.Close()
	id := identity()
	provider, err := sandboxdocker.New(dockerClient, sandboxdocker.Config{InstallationID: id.InstallationID, Image: image, Network: "bridge", Seccomp: string(seccomp)})
	if err != nil {
		t.Fatal(err)
	}
	var credential string
	hub := NewHub(HubOptions{Authenticate: func(ctx context.Context, node, token string) (Identity, error) {
		if node != id.NodeID || token != credential {
			return Identity{}, ErrAuthentication
		}
		return id, nil
	}, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	credential = stored.Credential
	start := func() (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: credential, Provider: provider, Probe: func(ctx context.Context) (Health, error) {
				_, err := dockerClient.Ping(ctx, client.PingOptions{})
				return Health{}, err
			}})
		}()
		return cancel, done
	}
	stop, done := start()
	running := true
	defer func() {
		if running {
			stop()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	proxy := hub.Proxy(id.NodeID, "docker", docker.Operations(), 1)
	r := reference()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := provider.Kill(ctx, r); err != nil {
			t.Error("owned fallback cleanup", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	b := sandbox.Bootstrap{Reference: r, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "http://core.invalid/api/v1", Credential: "synthetic-node-transport-credential", NetworkAccess: "enabled"}
	callCtx, callCancel := context.WithTimeout(ctx, 25*time.Second)
	info, err := proxy.Create(callCtx, b)
	callCancel()
	if err != nil {
		t.Fatal(err)
	}
	if !info.BootstrapComplete || info.State != "running" {
		t.Fatalf("create compute state: %+v", info)
	}
	command := func(args ...string) sandbox.CommandResult {
		t.Helper()
		commandCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		out, err := proxy.RunCommand(commandCtx, r, sandbox.Command{Args: args, Directory: "/workspace"})
		if err != nil || out.ExitCode != 0 {
			t.Fatalf("command failed: err=%v exit=%d", err, out.ExitCode)
		}
		return out
	}
	command("/bin/sh", "-c", "printf node-transport-persisted > /workspace/node-transport-proof")
	hub.mu.Lock()
	previous := hub.peers[id.NodeID]
	hub.mu.Unlock()
	assertDuplicateRejected(t, server.URL, id.NodeID, credential)
	hub.mu.Lock()
	retained := hub.peers[id.NodeID] == previous
	hub.mu.Unlock()
	if !retained {
		t.Fatal("duplicate connection replaced real Docker node")
	}
	if command("/bin/cat", "/workspace/node-transport-proof").Stdout != "node-transport-persisted" {
		t.Fatal("duplicate disrupted live node")
	}
	hub.Disconnect(id.NodeID)
	wait(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.peers[id.NodeID] != nil && hub.peers[id.NodeID] != previous
	})
	observed, err := proxy.GetInfo(ctx, r)
	if err != nil || observed.ProviderID != info.ProviderID {
		t.Fatal("connection loss changed compute", err)
	}
	if command("/bin/cat", "/workspace/node-transport-proof").Stdout != "node-transport-persisted" {
		t.Fatal("workspace changed after disconnect")
	}
	stop()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	running = false
	wait(t, func() bool { return !hub.Online(id.NodeID) })
	persisted, err := LoadIdentity(dir)
	if err != nil || persisted.Credential != credential || persisted.Identity.NodeID != id.NodeID {
		t.Fatal("node restart changed identity", err)
	}
	stop, done = start()
	running = true
	wait(t, func() bool { return hub.Online(id.NodeID) })
	observed, err = proxy.GetInfo(ctx, r)
	if err != nil || observed.ProviderID != info.ProviderID {
		t.Fatal("node restart changed compute", err)
	}
	if strings.TrimSpace(command("/bin/cat", "/workspace/node-transport-proof").Stdout) != "node-transport-persisted" {
		t.Fatal("workspace changed after node restart")
	}
	if err = proxy.Kill(ctx, r); err != nil {
		t.Fatal("node cleanup", err)
	}
	if _, err = provider.GetInfo(ctx, r); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatal("container retained after cleanup", err)
	}
	// Kill verifies removal of both named Runtime volumes before returning.
	t.Logf("real Docker node transport passed: installation=%s allocation=%s compute=%s; duplicate connection rejected; reconnect and node restart retained identity/file; owned container and volumes removed", id.InstallationID, r.AllocationID, info.ProviderID)
}
