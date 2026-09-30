package providers

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestDeploymentValidationFieldsPreserveMessages(t *testing.T) {
	registry := Builtin()
	for _, tc := range []struct {
		provider       string
		resources      sandbox.Resources
		param, message string
		min, max       *uint32
	}{
		{"docker", sandbox.Resources{}, "resources.cpus", "cpus must be 1..255 and memory_mib must be 512..1048576", validationBound(1), validationBound(255)},
		{"docker", sandbox.Resources{CPUs: 1, MemoryMiB: 511}, "resources.memory_mib", "cpus must be 1..255 and memory_mib must be 512..1048576", validationBound(512), validationBound(1048576)},
		{"microsandbox", sandbox.Resources{CPUs: 1, MemoryMiB: 512}, "resources.root_disk_mib", "microsandbox requires root_disk_mib and environment_disk_mib of at least 1024 MiB", validationBound(1024), nil},
		{"microsandbox", sandbox.Resources{CPUs: 1, MemoryMiB: 512, RootDiskMiB: 1024}, "resources.environment_disk_mib", "microsandbox requires root_disk_mib and environment_disk_mib of at least 1024 MiB", validationBound(1024), nil},
		{"docker", sandbox.Resources{CPUs: 1, MemoryMiB: 512, RootDiskMiB: 1}, "resources.root_disk_mib", "docker does not support independent disk capacity limits", validationBound(0), validationBound(0)},
		{"e2b", sandbox.Resources{CPUs: 1, MemoryMiB: 512, EnvironmentDiskMiB: 1}, "resources.environment_disk_mib", "e2b does not support independent disk capacity limits", validationBound(0), validationBound(0)},
	} {
		err := registry.ValidateResources(tc.provider, tc.resources)
		var field *sandbox.ValidationError
		if !errors.As(err, &field) || !errors.Is(err, sandbox.ErrInvalid) || err.Error() != sandbox.ErrInvalid.Error()+": "+tc.message || field.Param != tc.param {
			t.Fatalf("wrong error: %#v", err)
		}
		for i, bound := range []*uint32{field.Min, field.Max} {
			want := []*uint32{tc.min, tc.max}[i]
			if (bound == nil) != (want == nil) || (bound != nil && *bound != *want) {
				t.Fatal("wrong fixed bounds", field)
			}
		}
	}
	for _, tc := range []struct {
		provider string
		runtime  *sandbox.RuntimeRelease
		message  string
	}{
		{"docker", nil, "managed nodes require a pinned Runtime release"},
		{"docker", &sandbox.RuntimeRelease{}, "Runtime must reference one immutable distribution"},
		{"e2b", &sandbox.RuntimeRelease{}, "E2B Runtime is selected by its immutable template build"},
	} {
		err := registry.ValidateSpecification(tc.provider, sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 1, MemoryMiB: 512}, Runtime: tc.runtime})
		var field *sandbox.ValidationError
		if !errors.As(err, &field) || field.Param != "runtime" || err.Error() != sandbox.ErrInvalid.Error()+": "+tc.message || !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := registry.ValidateResources("docker", sandbox.Resources{CPUs: 255, MemoryMiB: 1048576}); err != nil {
		t.Fatal(err)
	}
	var field *sandbox.ValidationError
	err := registry.ValidateResources("private-provider", sandbox.Resources{CPUs: 1, MemoryMiB: 512})
	if errors.As(err, &field) || err.Error() != sandbox.ErrInvalid.Error()+": unsupported sandbox provider" {
		t.Fatal("unknown provider reclassified", err)
	}
}

func validationBound(v uint32) *uint32 { return &v }
