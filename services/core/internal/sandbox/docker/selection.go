package docker

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func Policy() sandbox.DeploymentPolicy {
	return sandbox.DeploymentPolicy{Artifacts: map[string]sandbox.ArtifactRule{
		"image_id":              {Pattern: "sha256:[0-9a-f]{64}", ManifestPath: []string{"images", "runtime"}},
		"image_manifest_digest": {Pattern: "sha256:[0-9a-f]{64}", ManifestPath: []string{"image_manifest_digests", "runtime"}},
	}, DefaultResources: &sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}
}

func ValidateResources(r sandbox.Resources) error { return r.ValidatePolicy("docker", Policy()) }
func ValidateSpecification(s sandbox.DeploymentSpec) error {
	return s.ValidatePolicy("docker", Policy())
}
