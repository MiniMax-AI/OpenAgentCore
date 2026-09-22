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
		CoreURL: "http://core.example/api/v1", Provider: "microsandbox", InstallationID: key,
		Microsandbox: &managedMicrosandboxConfig{
			HelperPath: "/opt/parsar/microsandbox-provider", RuntimeHome: "/var/lib/parsar/microsandbox", RuntimePath: "/opt/parsar/msb", FirmwarePath: "/opt/parsar/libkrunfw.so",
			RuntimeSHA256: strings.Repeat("a", 64), FirmwareSHA256: strings.Repeat("b", 64), Image: "registry.example/parsar-runtime@sha256:" + strings.Repeat("c", 64),
			MemoryMiB: 1024, CPUs: 1, RootDiskMiB: 4096,
			Network:     managedMicrosandboxNetwork{DefaultEgress: "deny", DefaultIngress: "deny", Rules: []managedMicrosandboxRule{{Action: "allow", Direction: "egress", Destination: "core.example", Protocol: "tcp", Port: "443"}}},
			IdleSeconds: 300, RetentionSeconds: 86400, MaxActive: 4, MaxRetained: 8,
		},
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
	if result.InstallationID != key || result.Provider == nil || result.Maintenance {
		t.Fatal("microsandbox identity lost")
	}
	if _, ok := result.Provider.(sandbox.CheckpointProvider); !ok {
		t.Fatal("microsandbox checkpoint capability missing")
	}
	policy := result.Suspension
	if policy == nil || policy.IdleTimeout != 5*time.Minute || policy.Retention != 24*time.Hour || policy.MaxActive != 4 || policy.MaxRetained != 8 {
		t.Fatalf("incorrect suspension policy: %+v", policy)
	}
}

func TestManagedMicrosandboxRejectsUnboundedOrImplicitConfiguration(t *testing.T) {
	config, _, write := managedMicrosandboxFixture(t)
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
		"unclean_home":          func(c *managedMicrosandboxConfig) { c.RuntimeHome = "/private/state/../msb" },
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
			entry := *config.Microsandbox
			mutate(&entry)
			changed.Microsandbox = &entry
			write(changed)
			if _, close, err := managedRuntimes(); err == nil {
				close()
				t.Fatal("invalid provider configuration accepted")
			}
		})
	}
}

func TestManagedMicrosandboxFingerprintPinsOnlyBackendNamespace(t *testing.T) {
	config, _, write := managedMicrosandboxFixture(t)
	original, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	close()
	config.Maintenance = true
	config.Microsandbox.Image = "registry.example/parsar-runtime@sha256:" + strings.Repeat("d", 64)
	config.Microsandbox.MaxActive++
	config.Microsandbox.IdleSeconds++
	write(config)
	changed, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	close()
	if !changed.Maintenance || changed.BackendFingerprint != original.BackendFingerprint {
		t.Fatal("policy/image change replaced backend identity")
	}
	config.Microsandbox.RuntimeHome = "/var/lib/parsar/another-installation"
	write(config)
	changed, close, err = managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	close()
	if changed.BackendFingerprint == original.BackendFingerprint || len(changed.BackendFingerprint) != 64 {
		t.Fatal("backend namespace change was not fenced")
	}
}
