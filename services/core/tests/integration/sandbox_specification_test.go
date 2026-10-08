package integration

import (
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func SandboxDeploymentTestSpec(provider string) sandbox.DeploymentSpec {
	s := sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}
	if provider == "e2b" {
		return s
	}
	s.Runtime = &sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), Artifacts: map[string]string{"image_id": "sha256:" + strings.Repeat("b", 64), "image_manifest_digest": "sha256:" + strings.Repeat("c", 64)}}
	if provider == "microsandbox" {
		s.Runtime.Artifacts = map[string]string{"microsandbox_ref": "oac-runtime@sha256:" + strings.Repeat("d", 64), "runtime_sha256": strings.Repeat("e", 64), "firmware_sha256": strings.Repeat("f", 64)}
		s.Resources.RootDiskMiB = 8192
		s.Resources.EnvironmentDiskMiB = 8192
	}
	return s
}

// EnrollmentTestToken keeps only the secret token of an issued node enrollment.
func EnrollmentTestToken(issued deployment.EnrollmentToken, err error) (string, error) {
	return issued.Token, err
}
