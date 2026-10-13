package docker

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func Policy() sandbox.DeploymentPolicy {
	return sandbox.DeploymentPolicy{Runtime: true, DefaultResources: &sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}
}

func ValidateResources(r sandbox.Resources) error { return r.ValidatePolicy("docker", Policy()) }
func ValidateSpecification(s sandbox.DeploymentSpec) error {
	return s.ValidatePolicy("docker", Policy())
}
