//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

const workspaceModeLabel = "io.oac.workspace-mode"
const workspaceObjectLabel = "io.oac.workspace-object"
const workspacePathLabel = "io.oac.workspace-path"

const resourceProofLabel = "io.oac.resource-proof"

func resourceProof(config wire.Config) string {
	raw, _ := json.Marshal(struct {
		ExternalWorkspace bool
		Image             string
		Resources         sandbox.Resources
	}{config.ExternalWorkspace, config.Image, sandbox.Resources{CPUs: uint32(config.CPUs), MemoryMiB: config.MemoryMiB,
		RootDiskMiB: config.RootDiskMiB, EnvironmentDiskMiB: config.EnvironmentDiskMiB}})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// The SDK decodes native CPU/memory/rootfs configuration. Its v0.7.2 projection
// omits mounts, so the explicitly owned or external inventory is projected here.
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
			Host    string `json:"host"`
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
	if config.ExternalWorkspace != (actual.Labels[workspaceModeLabel] == "external") {
		return sandbox.ErrInvalid
	}
	switch actual.Labels[workspaceModeLabel] {
	case "external":
		id, err := uuid.Parse(actual.Labels[workspaceObjectLabel])
		path := actual.Labels[workspacePathLabel]
		if err != nil || id == uuid.Nil || id.String() != actual.Labels[workspaceObjectLabel] || !filepath.IsAbs(path) || filepath.Clean(path) != path || mount.Type != "Bind" || mount.Guest != "/environment" || mount.Host != path {
			return sandbox.ErrInvalid
		}
		return nil
	case "owned":
		if actual.Labels[workspaceObjectLabel] != "" || actual.Labels[workspacePathLabel] != "" || config.EnvironmentDiskMiB == 0 {
			return sandbox.ErrInvalid
		}
	default:
		return sandbox.ErrInvalid
	}
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

func workspaceLabels(workspace *wire.WorkspaceDirectory) map[string]string {
	if workspace == nil {
		return map[string]string{workspaceModeLabel: "owned"}
	}
	return map[string]string{workspaceModeLabel: "external", workspaceObjectLabel: workspace.ObjectID, workspacePathLabel: workspace.Path}
}
func qualifyWorkspace(labels map[string]string, workspace *wire.WorkspaceDirectory) error {
	for _, key := range []string{workspaceModeLabel, workspaceObjectLabel} {
		if labels[key] != workspaceLabels(workspace)[key] {
			return sandbox.ErrOwnership
		}
	}
	return nil
}
