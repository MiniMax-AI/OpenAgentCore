package main

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	sandboxmicro "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
)

// Single-host providers pin the helper, runtime, firmware, image and resource
// limits explicitly. The helper owns local paths; no ambient backend is selected.
type managedMicrosandboxConfig struct {
	HelperPath         string                     `json:"helper_path"`
	RuntimeHome        string                     `json:"runtime_home"`
	RuntimePath        string                     `json:"runtime_path"`
	FirmwarePath       string                     `json:"firmware_path"`
	RuntimeSHA256      string                     `json:"runtime_sha256"`
	FirmwareSHA256     string                     `json:"firmware_sha256"`
	Image              string                     `json:"image"`
	MemoryMiB          uint32                     `json:"memory_mib"`
	CPUs               uint8                      `json:"cpus"`
	RootDiskMiB        uint32                     `json:"root_disk_mib"`
	EnvironmentDiskMiB uint32                     `json:"environment_disk_mib"`
	Network            managedMicrosandboxNetwork `json:"network"`
	IdleSeconds        int64                      `json:"idle_seconds"`
	RetentionSeconds   int64                      `json:"retention_seconds"`
	MaxActive          int                        `json:"max_active"`
	MaxRetained        int                        `json:"max_retained"`
}

type managedMicrosandboxNetwork struct {
	DefaultEgress  string                    `json:"default_egress"`
	DefaultIngress string                    `json:"default_ingress"`
	Rules          []managedMicrosandboxRule `json:"rules"`
}

type managedMicrosandboxRule struct {
	Action      string `json:"action"`
	Direction   string `json:"direction"`
	Destination string `json:"destination"`
	Protocol    string `json:"protocol"`
	Port        string `json:"port"`
}

func configureManagedMicrosandbox(entry managedMicrosandboxConfig, result *execution.RuntimeProvider) error {
	const maxSeconds = int64((1<<63 - 1) / time.Second)
	if entry.IdleSeconds <= 0 || entry.IdleSeconds > maxSeconds || entry.RetentionSeconds <= 0 || entry.RetentionSeconds > maxSeconds || entry.MaxActive <= 0 || entry.MaxRetained < entry.MaxActive {
		return errors.New("managed microsandbox requires positive bounded idle_seconds, retention_seconds and max_active, with max_retained >= max_active")
	}
	if !filepath.IsAbs(entry.RuntimeHome) || filepath.Clean(entry.RuntimeHome) != entry.RuntimeHome {
		return errors.New("managed microsandbox runtime_home must be a canonical absolute path")
	}
	network := sandboxmicro.NetworkPolicy{DefaultEgress: entry.Network.DefaultEgress, DefaultIngress: entry.Network.DefaultIngress}
	for _, rule := range entry.Network.Rules {
		network.Rules = append(network.Rules, sandboxmicro.NetworkRule{Action: rule.Action, Direction: rule.Direction, Destination: rule.Destination, Protocol: rule.Protocol, Port: rule.Port})
	}
	provider, err := sandboxmicro.New(sandboxmicro.Config{
		InstallationID: result.InstallationID, HelperPath: entry.HelperPath, RuntimeHome: entry.RuntimeHome, RuntimePath: entry.RuntimePath, FirmwarePath: entry.FirmwarePath,
		RuntimeSHA256: entry.RuntimeSHA256, FirmwareSHA256: entry.FirmwareSHA256, Image: entry.Image,
		MemoryMiB: entry.MemoryMiB, CPUs: entry.CPUs, RootDiskMiB: entry.RootDiskMiB, EnvironmentDiskMiB: entry.EnvironmentDiskMiB, Network: network,
	})
	if err != nil {
		return errors.New("invalid managed microsandbox provider configuration")
	}
	result.Provider = provider
	result.BackendFingerprint = managedBackendFingerprint("microsandbox", entry.RuntimeHome)
	result.Suspension = &execution.RuntimeSuspensionPolicy{IdleTimeout: time.Duration(entry.IdleSeconds) * time.Second, Retention: time.Duration(entry.RetentionSeconds) * time.Second, MaxActive: entry.MaxActive, MaxRetained: entry.MaxRetained}
	return nil
}
