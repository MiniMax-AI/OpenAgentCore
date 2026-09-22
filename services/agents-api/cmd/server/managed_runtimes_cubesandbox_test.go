package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// cubeFixture writes a valid cubesandbox entry for one provider key and returns
// the configuration plus a writer that publishes further mutations.
func cubeFixture(t *testing.T) (managedRuntimeConfig, func(managedRuntimeConfig)) {
	t.Helper()
	root := t.TempDir()
	key := uuid.NewString()
	keyFile := filepath.Join(root, "cube.key")
	if err := os.WriteFile(keyFile, []byte("synthetic-cube-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "providers.json")
	config := managedRuntimeConfig{
		CoreURL:         "http://core.example/api/v1",
		DefaultProvider: key,
		CubeSandbox: map[string]managedCubeConfig{key: {
			APIURL: "http://cubeapi.example/cubeapi/v1", ProxyNodeIP: "10.0.0.20", SandboxDomain: "cube.app", ProxyScheme: "http",
			Template: "tpl-pinned", LeaseSeconds: 43200, APIKeyFile: keyFile, HostMountRoot: "/data/shared/parsar",
		}},
	}
	return config, func(c managedRuntimeConfig) {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", file)
	}
}

// A Cube-only deployment is valid: no Docker section is required, and the pinned
// key file is the only credential source.
func TestManagedRuntimesAcceptCubeOnlyConfiguration(t *testing.T) {
	config, write := cubeFixture(t)
	write(config)
	t.Setenv("AGENTS_API_DAEMON_WS_URL", "ws://core.example/api/v1/agent-daemon/ws")
	result, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	key := config.DefaultProvider
	if result.DefaultProvider != key || len(result.Providers) != 1 || result.Providers[key] == nil {
		t.Fatal("CubeSandbox provider was not registered under its configured key")
	}
	if result.EngineProviders["codex"] != key {
		t.Fatal("default engine mapping did not follow the default provider")
	}
}

// Every new validation branch: an empty or missing key file, a rejected Cube
// option, a cross-map duplicate key, an unknown field, and a default or engine
// mapping that names an unconfigured key.
func TestManagedRuntimesRejectUnqualifiedCubeConfiguration(t *testing.T) {
	base, write := cubeFixture(t)
	key := base.DefaultProvider
	t.Setenv("AGENTS_API_DAEMON_WS_URL", "ws://core.example/api/v1/agent-daemon/ws")

	empty := filepath.Join(t.TempDir(), "empty.key")
	if err := os.WriteFile(empty, []byte("  \n"), 0600); err != nil {
		t.Fatal(err)
	}
	lease := base.CubeSandbox[key]
	lease.LeaseSeconds = 60
	rootless := base.CubeSandbox[key]
	rootless.HostMountRoot = "/"
	scheme := base.CubeSandbox[key]
	scheme.ProxyScheme = "ftp"
	relative := base.CubeSandbox[key]
	relative.APIURL = "not-a-url"
	egress := base.CubeSandbox[key]
	egress.PlatformEgress = []string{"bad host"}
	missing := base.CubeSandbox[key]
	missing.APIKeyFile = filepath.Join(t.TempDir(), "absent.key")
	blank := base.CubeSandbox[key]
	blank.APIKeyFile = empty

	for name, entry := range map[string]managedCubeConfig{"missing key file": missing, "empty key file": blank, "lease below the floor": lease, "host root without a prefix": rootless, "unsupported proxy scheme": scheme, "relative API url": relative, "egress entry with whitespace": egress} {
		candidate := base
		candidate.CubeSandbox = map[string]managedCubeConfig{key: entry}
		write(candidate)
		if _, close, err := managedRuntimes(); err == nil {
			close()
			t.Fatalf("%s: unqualified CubeSandbox configuration accepted", name)
		}
	}
	for name, mutate := range map[string]func(*managedRuntimeConfig){
		"duplicate key": func(c *managedRuntimeConfig) {
			c.Docker = map[string]managedDockerConfig{key: {Host: "unix:///var/run/docker.sock"}}
		},
		"default provider not configured": func(c *managedRuntimeConfig) { c.DefaultProvider = uuid.NewString() },
		"engine mapping not configured":   func(c *managedRuntimeConfig) { c.EngineProviders = map[string]string{"codex": uuid.NewString()} },
	} {
		candidate := base
		candidate.CubeSandbox = map[string]managedCubeConfig{key: base.CubeSandbox[key]}
		mutate(&candidate)
		write(candidate)
		if _, close, err := managedRuntimes(); err == nil {
			close()
			t.Fatalf("%s: unqualified managed configuration accepted", name)
		}
	}

	// An unknown section is rejected instead of silently ignored.
	unknown := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"core_url":"http://core.example/api/v1","cubesandboxx":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", unknown)
	if _, close, err := managedRuntimes(); err == nil {
		close()
		t.Fatal("unknown managed Runtime section accepted")
	}

	// A mixed deployment keeps one provider per key and registers both backends.
	seccomp := filepath.Join(t.TempDir(), "seccomp.json")
	if err := os.WriteFile(seccomp, []byte(`{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	dockerKey := uuid.NewString()
	mixed := base
	mixed.CubeSandbox = map[string]managedCubeConfig{key: base.CubeSandbox[key]}
	mixed.Docker = map[string]managedDockerConfig{dockerKey: {Host: "unix:///var/run/docker.sock", Image: "sha256:" + strings.Repeat("a", 64), Network: "bridge", SeccompFile: seccomp}}
	mixed.DefaultProvider = ""
	write(mixed)
	result, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if len(result.Providers) != 2 || result.Providers[key] == nil || result.Providers[dockerKey] == nil {
		t.Fatal("mixed managed deployment lost a backend")
	}
}
