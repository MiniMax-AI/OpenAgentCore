package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestManagedRuntimeOperatorConfigurationIsExplicit(t *testing.T) {
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", "")
	if result, close, err := managedRuntimes(); err != nil || result != nil {
		t.Fatal("implicit managed deployment", err)
	} else {
		close()
	}
	seccomp := filepath.Join(t.TempDir(), "seccomp.json")
	if err := os.WriteFile(seccomp, []byte(`{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, _, write := managedMicrosandboxFixture(t)
	config.Provider, config.Microsandbox = "docker", nil
	config.Docker = &managedDockerConfig{Host: "unix:///var/run/docker.sock", Image: "sha256:" + strings.Repeat("a", 64), Network: "bridge", SeccompFile: seccomp}
	write(config)
	t.Setenv("AGENTS_API_DAEMON_WS_URL", "")
	if _, close, err := managedRuntimes(); err == nil {
		close()
		t.Fatal("managed configuration without authenticated daemon gateway accepted")
	}
	t.Setenv("AGENTS_API_DAEMON_WS_URL", "ws://core.example/api/v1/agent-daemon/ws")
	t.Setenv("DOCKER_HOST", "not-a-valid-ambient-endpoint")
	t.Setenv("AGENTS_API_ENGINE", "claude_sdk")
	result, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	close()
	if result.InstallationID != config.InstallationID || result.Provider == nil || result.Suspension != nil || result.Maintenance {
		t.Fatal("provider identity or admission policy lost")
	}
	for _, mutate := range []func(*managedRuntimeConfig){
		func(c *managedRuntimeConfig) { c.InstallationID = "invalid" },
		func(c *managedRuntimeConfig) { c.Provider = "unknown" },
		func(c *managedRuntimeConfig) { c.Docker.Host = "tcp://remote:2375" },
		func(c *managedRuntimeConfig) { c.Docker.Host = "unix:///var/run/../docker.sock" },
		func(c *managedRuntimeConfig) { c.Docker.Host = "unix:///docker.sock?other=backend" },
		func(c *managedRuntimeConfig) { c.Docker.Image = "latest" },
		func(c *managedRuntimeConfig) { c.Docker.Network = "host" },
	} {
		changed, entry := config, *config.Docker
		changed.Docker = &entry
		mutate(&changed)
		write(changed)
		if _, close, err := managedRuntimes(); err == nil {
			close()
			t.Fatal("unqualified managed configuration accepted")
		}
	}
	config.Maintenance = true
	config.Docker.Image = "sha256:" + strings.Repeat("b", 64)
	write(config)
	maintenance, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	close()
	if !maintenance.Maintenance || maintenance.Provider == nil || maintenance.BackendFingerprint != result.BackendFingerprint {
		t.Fatal("maintenance lost the original cleanup adapter")
	}
	config.Docker.Host = "unix:///another/docker.sock"
	write(config)
	changed, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	close()
	if changed.BackendFingerprint == result.BackendFingerprint {
		t.Fatal("Docker endpoint change retained backend identity")
	}
}

func TestManagedRuntimeRejectsMixedAndLegacyConfiguration(t *testing.T) {
	config, _, _ := managedMicrosandboxFixture(t)
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(map[string]any){
		"default_provider": func(c map[string]any) { c["default_provider"] = config.InstallationID },
		"engine_providers": func(c map[string]any) { c["engine_providers"] = map[string]string{"codex": config.InstallationID} },
		"mixed_null":       func(c map[string]any) { c["docker"] = nil },
		"selected_null":    func(c map[string]any) { c["microsandbox"] = nil },
		"maintenance_null": func(c map[string]any) { c["maintenance"] = nil },
		"mixed":            func(c map[string]any) { c["docker"] = map[string]any{} },
		"old_micro_map":    func(c map[string]any) { c["microsandbox"] = map[string]any{config.InstallationID: c["microsandbox"]} },
		"old_docker_map": func(c map[string]any) {
			c["provider"] = "docker"
			delete(c, "microsandbox")
			c["docker"] = map[string]any{uuid.NewString(): map[string]any{}}
		},
		"missing_backend":  func(c map[string]any) { delete(c, "microsandbox") },
		"missing_choice":   func(c map[string]any) { delete(c, "provider") },
		"wrong_backend":    func(c map[string]any) { c["provider"] = "docker" },
		"missing_identity": func(c map[string]any) { delete(c, "installation_id") },
		"misspelled_policy": func(c map[string]any) {
			m := c["microsandbox"].(map[string]any)
			m["idle_timeout"] = m["idle_seconds"]
			delete(m, "idle_seconds")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var changed map[string]any
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			mutate(changed)
			data, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(os.Getenv("AGENTS_API_MANAGED_RUNTIMES_FILE"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, close, err := managedRuntimes(); err == nil {
				close()
				t.Fatal("mixed, legacy or incomplete configuration accepted")
			}
		})
	}
}
