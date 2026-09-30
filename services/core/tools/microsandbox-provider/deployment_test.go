//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

func deploymentFixture() (wire.Config, map[string]any) {
	config := wire.Config{Image: "runtime@sha256:" + strings.Repeat("a", 64), CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 8192, EnvironmentDiskMiB: 4096}
	actual := map[string]any{
		"image":     map[string]any{"Oci": map[string]any{"reference": config.Image, "root_disk": map[string]any{"kind": "managed", "size_mib": 8192}}},
		"resources": map[string]any{"cpus": 2, "max_cpus": 2, "memory_mib": 2048, "max_memory_mib": 2048},
		"mounts":    []any{map[string]any{"type": "Owned", "guest": "/environment", "storage": map[string]any{"kind": "disk", "capacity_mib": 4096}}},
	}
	return config, actual
}

func TestNativeDeploymentConfigurationRejectsDrift(t *testing.T) {
	for _, field := range []string{"match", "cpus", "max_cpus", "memory_mib", "max_memory_mib", "image", "root", "environment", "extra_mount", "missing_mount"} {
		t.Run(field, func(t *testing.T) {
			config, actual := deploymentFixture()
			switch field {
			case "cpus", "max_cpus", "memory_mib", "max_memory_mib":
				actual["resources"].(map[string]any)[field] = 1
			case "image":
				actual["image"] = "runtime:latest"
			case "root":
				actual["image"].(map[string]any)["Oci"].(map[string]any)["root_disk"].(map[string]any)["size_mib"] = 16384
			case "environment":
				actual["mounts"].([]any)[0].(map[string]any)["storage"].(map[string]any)["capacity_mib"] = 8192
			case "extra_mount":
				actual["mounts"] = append(actual["mounts"].([]any), map[string]any{"type": "Bind", "guest": "/other"})
			case "missing_mount":
				delete(actual, "mounts")
			}
			raw, _ := json.Marshal(actual)
			err := qualifyConfiguration(config, wire.Compute{}, string(raw), false)
			if (field == "match" && err != nil) || (field != "match" && !errors.Is(err, sandbox.ErrInvalid)) {
				t.Fatalf("configuration=%s error=%v", raw, err)
			}
		})
	}
}

func TestRestoredRootRequiresVerifiedInheritedProof(t *testing.T) {
	config, actual := deploymentFixture()
	actual["image"].(map[string]any)["Oci"].(map[string]any)["root_disk"].(map[string]any)["size_mib"] = nil
	compute := wire.Compute{RestoredFrom: &wire.SnapshotIdentity{ID: "verified-parent"}}
	for _, proof := range []string{"", "foreign", resourceProof(config)} {
		actual["labels"] = map[string]string{resourceProofLabel: proof}
		raw, _ := json.Marshal(actual)
		err := qualifyConfiguration(config, compute, string(raw), false)
		if (proof == resourceProof(config)) != (err == nil) {
			t.Fatalf("proof=%s error=%v", proof, err)
		}
		if err = qualifyConfiguration(config, wire.Compute{}, string(raw), true); err == nil {
			t.Fatal("initial root accepted missing size")
		}
		if err = qualifyConfiguration(config, compute, string(raw), true); err != nil {
			t.Fatal("verified snapshot cannot qualify restored root", err)
		}
	}
}

func TestSnapshotProofBindsResourcesWithoutChangingCleanupOwnership(t *testing.T) {
	config, actual := deploymentFixture()
	ref := sandbox.Reference{TenantID: "tenant", EnvironmentID: "environment", AllocationID: "allocation"}
	labels := wire.Labels(config, ref)
	labels[resourceProofLabel] = resourceProof(config)
	if err := qualifySnapshotResources(config, labels); err != nil {
		t.Fatal(err)
	}
	config.RootDiskMiB++
	if err := qualifySnapshotResources(config, labels); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("snapshot accepted different bound", err)
	}
	actual["labels"] = labels
	raw, _ := json.Marshal(actual)
	if _, err := qualifyCompute(config, ref, wire.Compute{ID: "owned"}, "owned", "running", string(raw)); err != nil {
		t.Fatal("resource drift changed cleanup ownership", err)
	}
	if err := qualifySnapshotResources(config, nil); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("missing snapshot proof accepted", err)
	}
}
