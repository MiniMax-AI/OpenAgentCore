package providers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestNodeRejectsCoreConfigurationAndUnknownProvider(t *testing.T) {
	for _, field := range []string{`"nodes":{"local":false}`, `"maintenance":true`, `"docker":{}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"provider":"docker",`+field+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("accepted a field outside the node configuration")
		}
	}
	if sandbox.BackendFingerprint("docker", "socket") == sandbox.BackendFingerprint("microsandbox", "socket") {
		t.Fatal("provider namespaces collide")
	}
}

func TestNodeBuildRequiresNodeProviderAndGeneration(t *testing.T) {
	registry := Builtin()
	options := sandbox.LocalOptions{Standalone: true}
	if _, _, err := registry.Build(sandbox.NodeConfig{Generation: 1, Provider: "e2b", InstallationID: uuid.NewString(), Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}}, options); err == nil {
		t.Fatal("accepted a cloud provider on a node")
	}
	if _, _, err := registry.Build(sandbox.NodeConfig{Provider: "docker", InstallationID: uuid.NewString(), Specification: validRegistrationSpec()}, options); err == nil {
		t.Fatal("accepted unbound node")
	}
}
