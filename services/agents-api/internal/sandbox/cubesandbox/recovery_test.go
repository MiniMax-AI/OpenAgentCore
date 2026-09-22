package cubesandbox

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

// The opt-in real-cluster checks of the implementation specification §11.2. They
// are gated on explicit environment variables, exactly like the Docker fixture
// image, and are not part of the offline acceptance: when any variable is absent
// every check skips.
//
//	AGENTS_RUNTIME_CUBE_API_URL          CubeAPI base URL, including /cubeapi/v1
//	AGENTS_RUNTIME_CUBE_PROXY_NODE_IP    CubeProxy address, "host" or "host:port"
//	AGENTS_RUNTIME_CUBE_TEMPLATE         Pinned template id
//	AGENTS_RUNTIME_CUBE_API_KEY_FILE     Private file holding the CubeAPI key
//	AGENTS_RUNTIME_CUBE_DOMAIN           Sandbox domain, for example cube.app
//
// Optional: AGENTS_RUNTIME_CUBE_PROXY_SCHEME (default http) and
// AGENTS_RUNTIME_CUBE_HOST_MOUNT_ROOT (defaults to a check-specific prefix that
// CubeMaster must allow).
func cubeCluster(t *testing.T) *Provider {
	t.Helper()
	apiURL := os.Getenv("AGENTS_RUNTIME_CUBE_API_URL")
	proxyNode := os.Getenv("AGENTS_RUNTIME_CUBE_PROXY_NODE_IP")
	template := os.Getenv("AGENTS_RUNTIME_CUBE_TEMPLATE")
	keyFile := os.Getenv("AGENTS_RUNTIME_CUBE_API_KEY_FILE")
	domain := os.Getenv("AGENTS_RUNTIME_CUBE_DOMAIN")
	if apiURL == "" || proxyNode == "" || template == "" || keyFile == "" || domain == "" {
		t.Skip("explicit CubeSandbox cluster configuration required")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil || strings.TrimSpace(string(key)) == "" {
		t.Fatal("cannot read the CubeSandbox API key file")
	}
	scheme := os.Getenv("AGENTS_RUNTIME_CUBE_PROXY_SCHEME")
	if scheme == "" {
		scheme = "http"
	}
	root := os.Getenv("AGENTS_RUNTIME_CUBE_HOST_MOUNT_ROOT")
	if root == "" {
		root = "/data/shared/parsar-cube-check"
	}
	provider, err := New(Config{
		InstallationID: uuid.NewString(), APIURL: apiURL, ProxyNodeIP: proxyNode, SandboxDomain: domain,
		ProxyScheme: scheme, Template: template, APIKey: strings.TrimSpace(string(key)), LeaseSeconds: 7200, HostMountRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.Close)
	return provider
}

// One full lifecycle: create, inspect, run a command, extend the TTL, kill, and
// kill again as a no-op.
func TestCubesandboxClusterLifecycle(t *testing.T) {
	provider := cubeCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	bootstrap := sandbox.Bootstrap{
		Reference: sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()},
		SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "http://127.0.0.1:1/api/v1",
		Credential: "cluster-check-credential", NetworkAccess: "enabled",
	}
	t.Cleanup(func() {
		stop, release := context.WithTimeout(context.Background(), 2*time.Minute)
		defer release()
		if err := provider.Kill(stop, bootstrap.Reference); err != nil {
			t.Error(err)
		}
	})
	info, err := provider.Create(ctx, bootstrap)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.ProviderID == "" || info.State != "running" {
		t.Fatalf("create did not report a running sandbox: %+v", info)
	}
	result, err := provider.RunCommand(ctx, bootstrap.Reference, sandbox.Command{Args: []string{"/bin/sh", "-c", "set -eu; printf cube-check > /environment/workspace/witness; printf \"$(cat /workspace/witness)\"; mkdir -p /environment/staging/atomic; mv /environment/staging/atomic /environment/workspace/atomic"}})
	if err != nil || result.ExitCode != 0 || result.Stdout != "cube-check" {
		t.Fatalf("native command or atomic rename failed: %+v %v", result, err)
	}
	// The workspace is a second view of the same host directory.
	result, err = provider.RunCommand(ctx, bootstrap.Reference, sandbox.Command{Args: []string{"/bin/sh", "-c", "set -eu; printf cross-view > /workspace/second; cat /environment/workspace/second"}})
	if err != nil || result.Stdout != "cross-view" {
		t.Fatalf("/workspace and /environment do not share a store: %+v %v", result, err)
	}
	if _, err := provider.Renew(ctx, bootstrap.Reference); err != nil {
		t.Fatalf("renew: %v", err)
	}
	// A sandbox carrying only foreign metadata is neither adopted nor deleted by
	// this allocation.
	foreignReference := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	foreignMetadata := provider.metadata(foreignReference)
	foreign, err := provider.create(ctx, map[string]any{"templateID": provider.config.Template, "timeout": provider.config.LeaseSeconds, "metadata": foreignMetadata})
	if err != nil {
		t.Fatalf("foreign sandbox: %v", err)
	}
	defer func() { _ = provider.delete(context.Background(), foreign.SandboxID) }()
	if _, err := provider.GetInfo(ctx, bootstrap.Reference); err != nil {
		t.Fatalf("owner became invisible after a foreign sandbox appeared: %v", err)
	}
	isForeign := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	if err := provider.Kill(ctx, isForeign); err != nil {
		t.Fatalf("foreign cleanup was not a no-op: %v", err)
	}
	if _, err := provider.inspectID(ctx, foreign.SandboxID, isForeign); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("foreign sandbox was adopted: %v", err)
	}
	if err := provider.Kill(ctx, bootstrap.Reference); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := provider.Kill(ctx, bootstrap.Reference); err != nil {
		t.Fatalf("repeated kill was not a no-op success: %v", err)
	}
	if _, err := provider.GetInfo(ctx, bootstrap.Reference); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("killed sandbox still observable: %v", err)
	}
}

// The host-mount descriptor must place both views under the configured prefix.
func TestCubesandboxClusterStorageDescriptor(t *testing.T) {
	provider := cubeCluster(t)
	reference := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	raw, err := provider.hostMounts(reference)
	if err != nil {
		t.Fatal(err)
	}
	root := provider.storePath(reference)
	if !strings.HasPrefix(root, provider.config.HostMountRoot+"/") {
		t.Fatalf("store path escaped the configured prefix: %s", root)
	}
	if !strings.Contains(raw, path.Join(root, "workspace")) || !strings.Contains(raw, environmentMount) || !strings.Contains(raw, workspaceMount) {
		t.Fatalf("storage descriptor does not describe both views: %s", raw)
	}
}
