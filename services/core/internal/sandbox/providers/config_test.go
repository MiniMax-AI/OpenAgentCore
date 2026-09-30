package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestNodeRejectsCoreConfigurationAndUnknownProvider(t *testing.T) {
	for _, field := range []string{`"nodes":{"local":false}`, `"maintenance":true`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"provider":"docker",`+field+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("accepted retired Core configuration")
		}
	}
	if BackendFingerprint("docker", "socket") == BackendFingerprint("microsandbox", "socket") {
		t.Fatal("provider namespaces collide")
	}
}
func TestNodeSpecificationCannotBeOverridden(t *testing.T) {
	if err := validateSpecification(Config{Generation: 1, Provider: "e2b", Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}, Docker: &Docker{}}); err == nil {
		t.Fatal("accepted a cloud provider on a node")
	}
	release := sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("b", 64), ImageManifestDigest: "sha256:" + strings.Repeat("c", 64), MicrosandboxRef: "oac-runtime@sha256:" + strings.Repeat("d", 64), RuntimeSHA256: strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64)}
	c := Config{Generation: 1, Provider: "docker", Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}, Runtime: &release}, Docker: &Docker{Image: release.ImageID}}
	for _, image := range []string{release.ImageID, release.ImageManifestDigest} {
		c.Docker.Image = image
		if err := validateSpecification(c); err != nil {
			t.Fatal(err)
		}
	}
	c.Docker.Image = "sha256:" + strings.Repeat("a", 64)
	if err := validateSpecification(c); err == nil {
		t.Fatal("accepted different Runtime")
	}
	c.Provider = "microsandbox"
	c.Docker = nil
	c.Specification.Resources.RootDiskMiB = 8192
	c.Specification.Resources.EnvironmentDiskMiB = 8192
	c.Microsandbox = &Microsandbox{CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 8192, EnvironmentDiskMiB: 8192, Image: release.MicrosandboxRef, RuntimeSHA256: release.RuntimeSHA256, FirmwareSHA256: release.FirmwareSHA256}
	if err := validateSpecification(c); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Microsandbox){func(m *Microsandbox) { m.CPUs = 1 }, func(m *Microsandbox) { m.MemoryMiB = 1024 }, func(m *Microsandbox) { m.EnvironmentDiskMiB = 4096 }, func(m *Microsandbox) { m.RootDiskMiB = 4096 }, func(m *Microsandbox) { m.FirmwareSHA256 = strings.Repeat("a", 64) }} {
		copy := *c.Microsandbox
		change(&copy)
		other := c
		other.Microsandbox = &copy
		if err := validateSpecification(other); err == nil {
			t.Fatal("accepted local specification override")
		}
	}
	c.Generation = 0
	if err := validateSpecification(c); err == nil {
		t.Fatal("accepted unbound node")
	}
}
