package providers

import (
	"errors"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	sandboxmicro "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

// Single-host providers pin the helper, runtime, firmware, image and resource
// limits explicitly. The helper owns local paths; no ambient backend is selected.
type Microsandbox struct {
	HelperLeaseGeneration uint64  `json:"-"`
	HelperLeasePath       string  `json:"-"`
	HelperPath            string  `json:"helper_path"`
	RuntimeHome           string  `json:"runtime_home"`
	RuntimePath           string  `json:"runtime_path"`
	FirmwarePath          string  `json:"firmware_path"`
	RuntimeSHA256         string  `json:"runtime_sha256"`
	FirmwareSHA256        string  `json:"firmware_sha256"`
	Image                 string  `json:"image"`
	MemoryMiB             uint32  `json:"memory_mib"`
	CPUs                  uint8   `json:"cpus"`
	RootDiskMiB           uint32  `json:"root_disk_mib"`
	EnvironmentDiskMiB    uint32  `json:"environment_disk_mib"`
	Network               Network `json:"network"`
}

type Network struct {
	DefaultEgress  string `json:"default_egress"`
	DefaultIngress string `json:"default_ingress"`
	Rules          []Rule `json:"rules"`
}

type Rule struct {
	Action      string `json:"action"`
	Direction   string `json:"direction"`
	Destination string `json:"destination"`
	Protocol    string `json:"protocol"`
	Port        string `json:"port"`
}

func configureMicrosandbox(entry Microsandbox, resources sandbox.Resources, result *Built) error {
	if !filepath.IsAbs(entry.RuntimeHome) || filepath.Clean(entry.RuntimeHome) != entry.RuntimeHome {
		return errors.New("managed microsandbox runtime_home must be a canonical absolute path")
	}
	network := sandboxmicro.NetworkPolicy{DefaultEgress: entry.Network.DefaultEgress, DefaultIngress: entry.Network.DefaultIngress}
	for _, rule := range entry.Network.Rules {
		network.Rules = append(network.Rules, sandboxmicro.NetworkRule{Action: rule.Action, Direction: rule.Direction, Destination: rule.Destination, Protocol: rule.Protocol, Port: rule.Port})
	}
	provider, err := sandboxmicro.NewWithCaller(sandboxmicro.Config{
		InstallationID: result.InstallationID, HelperPath: entry.HelperPath, RuntimeHome: entry.RuntimeHome, RuntimePath: entry.RuntimePath, FirmwarePath: entry.FirmwarePath,
		RuntimeSHA256: entry.RuntimeSHA256, FirmwareSHA256: entry.FirmwareSHA256, Image: entry.Image,
		MemoryMiB: entry.MemoryMiB, CPUs: entry.CPUs, RootDiskMiB: entry.RootDiskMiB, EnvironmentDiskMiB: entry.EnvironmentDiskMiB, Network: network,
	}, &sandboxmicro.ProcessCaller{LeasePath: entry.HelperLeasePath, LeaseIdentity: sandboxmicro.LeaseIdentity{InstallationID: result.InstallationID, Generation: entry.HelperLeaseGeneration, SpecificationDigest: result.SpecificationDigest}})
	if err != nil {
		return errors.New("invalid managed microsandbox provider configuration")
	}
	result.Provider = provider
	result.Probe = microsandboxProbe(entry, resources)
	result.BackendFingerprint = BackendFingerprint("microsandbox", entry.RuntimeHome)
	return nil
}

func buildMicrosandbox(c Config, result *Built) (func(), error) {
	closeProvider := func() {}
	if c.Microsandbox == nil || c.Docker != nil {
		return closeProvider, errors.New("managed microsandbox requires only the microsandbox configuration object")
	}
	return closeProvider, configureMicrosandbox(*c.Microsandbox, c.Specification.Resources, result)
}
