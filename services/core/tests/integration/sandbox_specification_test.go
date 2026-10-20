package integration

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func SandboxDeploymentTestSpec(provider string) sandbox.DeploymentSpec {
	s := sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}
	if provider == "microsandbox" {
		s.Resources.RootDiskMiB = 8192
		s.Resources.EnvironmentDiskMiB = 8192
	}
	return s
}

// EnrollmentTestToken keeps only the secret token of an issued node enrollment.
func EnrollmentTestToken(issued deployment.EnrollmentToken, err error) (string, error) {
	return issued.Token, err
}
