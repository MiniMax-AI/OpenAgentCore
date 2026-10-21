//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

const workspaceObjectLabel = "io.oac.workspace-object"
const workspacePathLabel = "io.oac.workspace-path"

const resourceProofLabel = "io.oac.resource-proof"

func resourceProof(config wire.Config) string {
	raw, _ := json.Marshal(struct {
		Image     string
		Resources sandbox.Resources
	}{config.Image, sandbox.Resources{CPUs: uint32(config.CPUs), MemoryMiB: config.MemoryMiB, RootDiskMiB: config.RootDiskMiB}})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// The SDK decodes native CPU/memory/rootfs configuration. Its v0.7.8 projection
// omits mounts, so the external directory inventory is projected here.
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
			Type  string `json:"type"`
			Host  string `json:"host"`
			Guest string `json:"guest"`
		} `json:"mounts"`
	}
	if json.Unmarshal([]byte(raw), &inventory) != nil || len(inventory.Mounts) != 1 {
		return sandbox.ErrInvalid
	}
	mount := inventory.Mounts[0]
	id, err := uuid.Parse(actual.Labels[workspaceObjectLabel])
	path := actual.Labels[workspacePathLabel]
	if err != nil || id == uuid.Nil || id.String() != actual.Labels[workspaceObjectLabel] || !filepath.IsAbs(path) || filepath.Clean(path) != path || mount.Type != "Bind" || mount.Guest != "/environment" || mount.Host != path {
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

// Checkpoints exclude the image. The restored root must resolve from this node's
// installed image by the captured digest; a missing image record fails before
// Restore. This metadata check cannot verify the SDK's physical image cache.
func qualifyRuntimeImage(ctx context.Context, config wire.Config, captured string) error {
	_, digest, _ := strings.Cut(config.Image, "@")
	if captured != digest {
		return sandbox.ErrInvalid
	}
	image, err := sdk.Image.Get(ctx, config.Image)
	if sdk.IsKind(err, sdk.ErrImageNotFound) {
		return sandbox.ErrInvalid
	}
	if err != nil {
		return err
	}
	if image.ManifestDigest() != digest {
		return sandbox.ErrInvalid
	}
	return nil
}

func workspaceLabels(workspace *wire.WorkspaceDirectory) map[string]string {
	return map[string]string{workspaceObjectLabel: workspace.ObjectID, workspacePathLabel: workspace.Path}
}
func qualifyWorkspace(labels map[string]string, workspace *wire.WorkspaceDirectory) error {
	if labels[workspaceObjectLabel] != workspace.ObjectID {
		return sandbox.ErrOwnership
	}
	return nil
}
