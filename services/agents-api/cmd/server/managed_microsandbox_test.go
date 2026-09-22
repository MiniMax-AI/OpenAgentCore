package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

func managedMicrosandboxFixture(t *testing.T) (managedRuntimeConfig, string, func(managedRuntimeConfig)) {
	t.Helper()
	key := uuid.NewString()
	config := managedRuntimeConfig{
		CoreURL: "http://core.example/api/v1", DefaultProvider: key,
		Microsandbox: map[string]managedMicrosandboxConfig{key: {
			HelperPath: "/opt/parsar/microsandbox-provider", RuntimeHome: "/var/lib/parsar/microsandbox", RuntimePath: "/opt/parsar/msb", FirmwarePath: "/opt/parsar/libkrunfw.so",
			RuntimeSHA256: strings.Repeat("a", 64), FirmwareSHA256: strings.Repeat("b", 64), Image: "registry.example/parsar-runtime@sha256:" + strings.Repeat("c", 64),
			MemoryMiB: 1024, CPUs: 1, RootDiskMiB: 4096,
			Network:     managedMicrosandboxNetwork{DefaultEgress: "deny", DefaultIngress: "deny", Rules: []managedMicrosandboxRule{{Action: "allow", Direction: "egress", Destination: "core.example", Protocol: "tcp", Port: "443"}}},
			IdleSeconds: 300, RetentionSeconds: 86400, MaxActive: 4, MaxRetained: 8,
		}},
	}
	file := filepath.Join(t.TempDir(), "providers.json")
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", file)
	t.Setenv("AGENTS_API_DAEMON_WS_URL", "ws://core.example/api/v1/agent-daemon/ws")
	t.Setenv("AGENTS_API_ENGINE", "codex")
	write := func(c managedRuntimeConfig) {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(config)
	return config, key, write
}

func TestManagedMicrosandboxConfigurationAndPolicy(t *testing.T) {
	_, key, _ := managedMicrosandboxFixture(t)
	result, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if result.DefaultProvider != key || result.EngineProviders["codex"] != key || len(result.Providers) != 1 {
		t.Fatal("microsandbox mapping lost")
	}
	if _, ok := result.Providers[key].(sandbox.CheckpointProvider); !ok {
		t.Fatal("microsandbox checkpoint capability missing")
	}
	policy, ok := result.Suspension[key]
	if !ok || policy.IdleTimeout != 5*time.Minute || policy.Retention != 24*time.Hour || policy.MaxActive != 4 || policy.MaxRetained != 8 {
		t.Fatalf("incorrect suspension policy: %+v", policy)
	}
}

func TestManagedMicrosandboxRejectsUnboundedOrImplicitConfiguration(t *testing.T) {
	config, key, write := managedMicrosandboxFixture(t)
	cases := map[string]func(*managedMicrosandboxConfig){
		"retained_missing":      func(c *managedMicrosandboxConfig) { c.MaxRetained = 0 },
		"retained_below_active": func(c *managedMicrosandboxConfig) { c.MaxRetained = c.MaxActive - 1 },
		"retained_negative":     func(c *managedMicrosandboxConfig) { c.MaxRetained = -1 },
		"idle_missing":          func(c *managedMicrosandboxConfig) { c.IdleSeconds = 0 },
		"retention_missing":     func(c *managedMicrosandboxConfig) { c.RetentionSeconds = 0 },
		"capacity_missing":      func(c *managedMicrosandboxConfig) { c.MaxActive = 0 },
		"negative_capacity":     func(c *managedMicrosandboxConfig) { c.MaxActive = -1 },
		"negative_idle":         func(c *managedMicrosandboxConfig) { c.IdleSeconds = -1 },
		"idle_overflow":         func(c *managedMicrosandboxConfig) { c.IdleSeconds = 1<<63 - 1 },
		"retention_overflow":    func(c *managedMicrosandboxConfig) { c.RetentionSeconds = 1<<63 - 1 },
		"memory_missing":        func(c *managedMicrosandboxConfig) { c.MemoryMiB = 0 },
		"cpus_missing":          func(c *managedMicrosandboxConfig) { c.CPUs = 0 },
		"disk_missing":          func(c *managedMicrosandboxConfig) { c.RootDiskMiB = 0 },
		"relative_helper":       func(c *managedMicrosandboxConfig) { c.HelperPath = "./helper" },
		"relative_home":         func(c *managedMicrosandboxConfig) { c.RuntimeHome = ".cache" },
		"runtime_missing":       func(c *managedMicrosandboxConfig) { c.RuntimePath = "" },
		"firmware_missing":      func(c *managedMicrosandboxConfig) { c.FirmwarePath = "" },
		"runtime_hash_missing":  func(c *managedMicrosandboxConfig) { c.RuntimeSHA256 = "" },
		"firmware_hash_missing": func(c *managedMicrosandboxConfig) { c.FirmwareSHA256 = "" },
		"image_unpinned":        func(c *managedMicrosandboxConfig) { c.Image = "registry.example/parsar-runtime:latest" },
		"network_missing":       func(c *managedMicrosandboxConfig) { c.Network = managedMicrosandboxNetwork{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := config
			entry := config.Microsandbox[key]
			mutate(&entry)
			changed.Microsandbox = map[string]managedMicrosandboxConfig{key: entry}
			write(changed)
			if _, close, err := managedRuntimes(); err == nil {
				close()
				t.Fatal("invalid provider configuration accepted")
			}
		})
	}
}

func TestManagedMicrosandboxAndDockerKeepDistinctOwnership(t *testing.T) {
	config, microKey, write := managedMicrosandboxFixture(t)
	dockerKey := uuid.NewString()
	seccomp := filepath.Join(t.TempDir(), "seccomp.json")
	if err := os.WriteFile(seccomp, []byte(`{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	docker := managedDockerConfig{Host: "unix:///var/run/docker.sock", Image: "sha256:" + strings.Repeat("d", 64), Network: "bridge", SeccompFile: seccomp}
	config.Docker = map[string]managedDockerConfig{dockerKey: docker}
	write(config)
	result, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if len(result.Providers) != 2 || result.Providers[dockerKey] == nil || result.Providers[microKey] == nil || len(result.Suspension) != 1 {
		t.Fatal("mixed provider ownership lost")
	}
	if _, exists := result.Suspension[dockerKey]; exists {
		t.Fatal("Docker unexpectedly acquired suspension policy")
	}
	config.Docker = map[string]managedDockerConfig{microKey: docker}
	write(config)
	if _, close, err := managedRuntimes(); err == nil {
		close()
		t.Fatal("same provider identity accepted for two backends")
	}
}

func TestManagedMicrosandboxMappingsAndStrictJSON(t *testing.T) {
	config, key, write := managedMicrosandboxFixture(t)
	config.DefaultProvider = ""
	write(config)
	result, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	close()
	if result.DefaultProvider != "" || len(result.Providers) != 1 || len(result.Suspension) != 1 {
		t.Fatal("cleanup-only microsandbox configuration lost")
	}
	config.EngineProviders = map[string]string{"codex": key}
	write(config)
	if _, close, err := managedRuntimes(); err != nil {
		t.Fatal(err)
	} else {
		close()
	}
	config.EngineProviders = map[string]string{"codex": uuid.NewString()}
	write(config)
	if _, close, err := managedRuntimes(); err == nil {
		close()
		t.Fatal("unconfigured engine provider accepted")
	}
	config.EngineProviders = nil
	config.DefaultProvider = uuid.NewString()
	write(config)
	if _, close, err := managedRuntimes(); err == nil {
		close()
		t.Fatal("unconfigured default accepted")
	}
	config.DefaultProvider = key
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"idle_seconds":300`, `"idle_timeout":300`, 1))
	if err := os.WriteFile(os.Getenv("AGENTS_API_MANAGED_RUNTIMES_FILE"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, close, err := managedRuntimes(); err == nil {
		close()
		t.Fatal("misspelled policy field accepted")
	}
}
