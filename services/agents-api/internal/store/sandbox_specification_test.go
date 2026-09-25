package store

import (
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"strings"
)

func SandboxDeploymentTestSpec(provider string) sandbox.DeploymentSpec {
	s := sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}
	if provider == "e2b" {
		return s
	}
	s.Runtime = &sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("b", 64), ImageManifestDigest: "sha256:" + strings.Repeat("c", 64), MicrosandboxRef: "parsar-core-runtime@sha256:" + strings.Repeat("d", 64), RuntimeSHA256: strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64)}
	if provider == "microsandbox" {
		s.Resources.RootDiskMiB = 8192
		s.Resources.EnvironmentDiskMiB = 8192
	}
	return s
}

// EnrollmentTestToken keeps only the secret token of an issued node enrollment.
func EnrollmentTestToken(issued RuntimeNodeEnrollmentToken, err error) (string, error) {
	return issued.Token, err
}
