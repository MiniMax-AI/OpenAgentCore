package microsandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/internal/providerassets"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// NodeArtifacts are the native files a microsandbox node installs beside the
// shared node payload.
var NodeArtifacts = []providerassets.Artifact{
	{Path: "native/bin/oac-microsandbox-provider", Suffix: "microsandbox-provider", Role: "runtime"},
	{Path: "native/microsandbox/msb", Suffix: "msb", Role: "runtime"},
	{Path: "native/microsandbox/libkrunfw.so.5.6.1", Suffix: "libkrunfw.so.5.6.1", Role: "runtime"},
}

// Native is the node configuration's native object for microsandbox: the
// helper, Runtime and firmware paths, the private store and the network policy.
// The helper owns local paths; no ambient backend is selected. Resources, the
// image and the artifact hashes come from the deployment specification.
type Native struct {
	HelperPath   string  `json:"helper_path"`
	RuntimeHome  string  `json:"runtime_home"`
	RuntimePath  string  `json:"runtime_path"`
	FirmwarePath string  `json:"firmware_path"`
	Network      Network `json:"network"`
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

func configureMicrosandbox(entry Native, spec sandbox.DeploymentSpec, caller *ProcessCaller, result *sandbox.Built) (Config, error) {
	if !filepath.IsAbs(entry.RuntimeHome) || filepath.Clean(entry.RuntimeHome) != entry.RuntimeHome {
		return Config{}, errors.New("managed microsandbox runtime_home must be a canonical absolute path")
	}
	network := NetworkPolicy{DefaultEgress: entry.Network.DefaultEgress, DefaultIngress: entry.Network.DefaultIngress}
	for _, rule := range entry.Network.Rules {
		network.Rules = append(network.Rules, NetworkRule{Action: rule.Action, Direction: rule.Direction, Destination: rule.Destination, Protocol: rule.Protocol, Port: rule.Port})
	}
	release, resources := spec.Runtime, spec.Resources
	config := Config{
		InstallationID: result.InstallationID, HelperPath: entry.HelperPath, RuntimeHome: entry.RuntimeHome, RuntimePath: entry.RuntimePath, FirmwarePath: entry.FirmwarePath,
		RuntimeSHA256: release.RuntimeSHA256, FirmwareSHA256: release.FirmwareSHA256, Image: release.MicrosandboxRef,
		MemoryMiB: resources.MemoryMiB, CPUs: uint8(resources.CPUs), RootDiskMiB: resources.RootDiskMiB, EnvironmentDiskMiB: resources.EnvironmentDiskMiB, Network: network,
	}
	provider, err := NewWithCaller(config, caller)
	if err != nil {
		return Config{}, errors.New("invalid managed microsandbox provider configuration")
	}
	result.Provider = provider
	result.Probe = microsandboxProbe(config, resources)
	result.Quiescent = caller.Quiescent
	result.BackendFingerprint = sandbox.BackendFingerprint("microsandbox", entry.RuntimeHome)
	return config, nil
}

// BuildNode constructs the node-local microsandbox adapter from a validated node configuration.
func BuildNode(c sandbox.NodeConfig, options sandbox.LocalOptions, result *sandbox.Built) (func(), error) {
	closeProvider := func() {}
	var entry Native
	if sandbox.DecodeConfigurationObject(c.Native, &entry, "helper_path", "runtime_home", "runtime_path", "firmware_path", "network") != nil {
		return closeProvider, errors.New("invalid managed microsandbox node configuration")
	}
	caller := &ProcessCaller{}
	if options.GenerationStateDirectory != "" {
		directory := filepath.Join(options.GenerationStateDirectory, "generations")
		if err := os.MkdirAll(directory, 0700); err != nil {
			return closeProvider, err
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			return closeProvider, sandbox.ErrOwnership
		}
		caller.LeasePath = filepath.Join(directory, strconv.FormatUint(c.Generation, 10)+".lease")
		caller.LeaseIdentity = LeaseIdentity{InstallationID: result.InstallationID, Generation: c.Generation, SpecificationDigest: result.SpecificationDigest}
	}
	config, err := configureMicrosandbox(entry, c.Specification, caller, result)
	if err != nil {
		return closeProvider, err
	}
	if options.GenerationStateDirectory != "" {
		result.Probe = microsandboxGenerationProbe(config, result.Probe)
	}
	return closeProvider, nil
}
