//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	wire "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

const resourceProofLabel = "io.oac.resource-proof"

func resourceProof(config wire.Config) string {
	raw, _ := json.Marshal(struct {
		Image     string
		Resources sandbox.Resources
	}{config.Image, sandbox.Resources{CPUs: uint32(config.CPUs), MemoryMiB: config.MemoryMiB,
		RootDiskMiB: config.RootDiskMiB, EnvironmentDiskMiB: config.EnvironmentDiskMiB}})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// The SDK decodes native CPU/memory/rootfs configuration. Its v0.7.2 projection
// omits mounts, so only the owned disk inventory is projected separately here.
// Restored roots have no configured size: a verified full snapshot supplies that
// proof, retained as a label only after inspecting the restored target.
func qualifyConfiguration(config wire.Config, compute wire.Compute, raw string, inheritedRoot bool) error {
	var actual sdk.SandboxConfig
	if json.Unmarshal([]byte(raw), &actual) != nil || actual.Image != config.Image ||
		actual.CPUs != config.CPUs || actual.MaxCPUs != config.CPUs ||
		actual.MemoryMiB != config.MemoryMiB || actual.MaxMemoryMiB != config.MemoryMiB ||
		actual.RootDisk == nil || actual.RootDisk.Kind() != sdk.RootDiskKindManaged {
		return sandbox.ErrInvalid
	}
	if actual.RootDisk.SizeMiB != config.RootDiskMiB {
		if actual.RootDisk.SizeMiB != 0 || compute.RestoredFrom == nil ||
			(!inheritedRoot && actual.Labels[resourceProofLabel] != resourceProof(config)) {
			return sandbox.ErrInvalid
		}
	}
	var inventory struct {
		Mounts []struct {
			Type    string `json:"type"`
			Guest   string `json:"guest"`
			Storage struct {
				Kind        string `json:"kind"`
				CapacityMiB uint32 `json:"capacity_mib"`
			} `json:"storage"`
		} `json:"mounts"`
	}
	if json.Unmarshal([]byte(raw), &inventory) != nil || len(inventory.Mounts) != 1 {
		return sandbox.ErrInvalid
	}
	mount := inventory.Mounts[0]
	if mount.Type != "Owned" || mount.Guest != "/environment" || mount.Storage.Kind != "disk" || mount.Storage.CapacityMiB != config.EnvironmentDiskMiB {
		return sandbox.ErrInvalid
	}
	return nil
}

func qualifySnapshotResources(config wire.Config, labels map[string]string) error {
	if labels[resourceProofLabel] != resourceProof(config) {
		return sandbox.ErrInvalid
	}
	return nil
}
