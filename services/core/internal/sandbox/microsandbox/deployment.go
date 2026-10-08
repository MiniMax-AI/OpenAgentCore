package microsandbox

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func Policy() sandbox.DeploymentPolicy {
	return sandbox.DeploymentPolicy{Disk: true, Artifacts: map[string]sandbox.ArtifactRule{
		"microsandbox_ref": {Pattern: "oac-runtime@sha256:[0-9a-f]{64}", ManifestPath: []string{"runtime_ref"}},
		"runtime_sha256":   {Pattern: "[0-9a-f]{64}", ManifestPath: []string{"microsandbox", "runtime_sha256"}},
		"firmware_sha256":  {Pattern: "[0-9a-f]{64}", ManifestPath: []string{"microsandbox", "firmware_sha256"}},
	},
		DefaultResources: &sandbox.Resources{CPUs: 2, MemoryMiB: 4096, RootDiskMiB: 8192, EnvironmentDiskMiB: 8192}}
}

func ValidateResources(r sandbox.Resources) error { return r.ValidatePolicy("microsandbox", Policy()) }
func ValidateSpecification(s sandbox.DeploymentSpec) error {
	return s.ValidatePolicy("microsandbox", Policy())
}
