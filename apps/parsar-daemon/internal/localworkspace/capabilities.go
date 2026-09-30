package localworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/runtimefs"
)

// Prepare runs under the admitted executor's lifetime, before native startup.
// Reconnection validates installed contents without reopening mutable sources.
func (b *Binding) Prepare(ctx context.Context, r proto.PromptRequestPayload) (proto.PromptRequestPayload, error) {
	if b == nil && r.LocalEnvironment == nil || r.WorkspaceReadOnly {
		return r, nil
	}
	if b == nil || r.LocalEnvironment == nil || r.LocalEnvironment.CapabilitySources == nil ||
		r.LocalEnvironment.ID != b.environment || r.AgentStateKey != b.stateKey || r.WorkDir != b.workspace {
		return r, agentcapabilities.ErrInvalid
	}
	b.capabilityMu.Lock()
	defer b.capabilityMu.Unlock()
	marker, err := b.openSnapshotMarker()
	if err != nil {
		return r, err
	}
	defer marker.close()
	root, unlock, err := b.openCapabilities(ctx, marker.completed)
	if err != nil {
		return r, err
	}
	defer unlock()
	input := *r.LocalEnvironment.CapabilitySources
	identity := b.capabilityIdentity()
	if err := prepareToolEnvironment(r.LocalEnvironment.ToolEnvironment, marker.completed); err != nil {
		return r, err
	}
	manifest, err := b.loadCapabilitySnapshot(root, input, identity, marker.completed)
	if err != nil || marker.complete() != nil {
		return r, agentcapabilities.ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return r, err
	}
	local := *r.LocalEnvironment
	local.Skills, local.MCP, local.CapabilityRoot = manifest.Skills, nil, b.capabilityRoot
	for i := range local.Skills {
		local.Skills[i].InstallationRoot = b.capabilityRoot
	}
	if local.ToolEnvironment {
		if _, err = ReadToolEnvironment(); err != nil {
			return r, err
		}
	}
	if len(manifest.MCP) != 0 {
		if b.NetworkPolicy().Access != "enabled" {
			return r, agentcapabilities.ErrInvalid
		}
		values, readErr := b.ReadToolEnvironment()
		if readErr != nil {
			return r, readErr
		}
		local.MCP, err = resolveEnvironmentMCP(manifest.MCP, values)
		for i := range local.MCP {
			local.MCP[i].InstallationRoot = b.capabilityRoot
			local.MCP[i].WorkspaceRoot = b.workspace
		}
		if err != nil {
			return r, err
		}
	}
	r.LocalEnvironment = &local
	return r, nil
}

// ApplyRuntimePreparation settles every filesystem operation before returning. A
// cancelled caller never leaves an unowned installation goroutine behind.
func (b *Binding) ApplyRuntimePreparation(ctx context.Context, input proto.RuntimePreparePayload, data []byte) error {
	if b == nil || !b.Matches(input.EnvironmentID, input.SessionID) || !proto.ValidRuntimePrepareRequest(input) || input.Step != "begin" {
		return agentcapabilities.ErrInvalid
	}
	b.capabilityMu.Lock()
	defer b.capabilityMu.Unlock()
	marker, err := b.openSnapshotMarker()
	if err != nil {
		return err
	}
	defer marker.close()
	if marker.completed {
		return agentcapabilities.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch input.Action {
	case "file":
		if len(data) != input.SizeBytes {
			return agentcapabilities.ErrInvalid
		}
		return b.installInitialFile(ctx, *input.File, data)
	case "initialize":
		if len(data) != 0 {
			return agentcapabilities.ErrInvalid
		}
		return b.initializeRuntime(ctx, *input.Initialization)
	}
	root, unlock, err := b.openCapabilities(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	switch input.Action {
	case "skill":
		err = agentcapabilities.InstallSkill(root, data, *input.Skill)
	case "plugin":
		err = agentcapabilities.InstallPlugin(root, input.Slot, data, *input.Plugin)
	case "finalize":
		if err := prepareToolEnvironment(false, false); err != nil {
			return err
		}
		_, err = b.loadCapabilitySnapshot(root, *input.Sources, b.capabilityIdentity(), false)
		if err == nil {
			err = marker.complete()
		}
	default:
		err = agentcapabilities.ErrInvalid
	}
	if err != nil {
		return agentcapabilities.ErrInvalid
	}
	return ctx.Err()
}

func (b *Binding) loadCapabilitySnapshot(root *os.Root, input agentcapabilities.Input, identity agentcapabilities.Identity, completed bool) (agentcapabilities.Manifest, error) {
	if _, err := root.Lstat(agentcapabilities.ManifestName); errors.Is(err, os.ErrNotExist) {
		if completed || agentcapabilities.Finalize(root, input, identity, b.resolveCapabilityDirectory) != nil {
			return agentcapabilities.Manifest{}, agentcapabilities.ErrInvalid
		}
	} else if err != nil {
		return agentcapabilities.Manifest{}, agentcapabilities.ErrInvalid
	}
	manifest, err := agentcapabilities.Load(root)
	if err != nil || agentcapabilities.ValidateSelection(manifest, input, identity) != nil {
		return agentcapabilities.Manifest{}, agentcapabilities.ErrInvalid
	}
	return manifest, nil
}

func (b *Binding) capabilityIdentity() agentcapabilities.Identity {
	return agentcapabilities.Identity{EnvironmentID: b.environment, SessionID: strings.TrimPrefix(b.stateKey, "agents-api-")}
}

// The current Linux Runtime layout is shared by user-owned and managed hosts.
// Deployment paths never come from a capability transport request.
func (b *Binding) openCapabilities(ctx context.Context, completed bool) (*os.Root, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if b.capabilityRoot == "" || runtimefs.ValidateLocalPath(b.capabilityRoot) != nil {
		return nil, nil, agentcapabilities.ErrInvalid
	}
	if !completed {
		if err := os.MkdirAll(b.capabilityRoot, 0700); err != nil {
			return nil, nil, agentcapabilities.ErrInvalid
		}
	}
	root, err := os.OpenRoot(b.capabilityRoot)
	if err != nil {
		return nil, nil, agentcapabilities.ErrInvalid
	}
	unlock, err := runtimefs.LockDirectory(root)
	if err != nil {
		root.Close()
		return nil, nil, agentcapabilities.ErrInvalid
	}
	return root, func() { unlock(); root.Close() }, nil
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func (b *Binding) resolveCapabilityDirectory(source string) (*os.Root, error) {
	if agentcapabilities.ValidateLocalDirectories([]string{source}) != nil {
		return nil, agentcapabilities.ErrInvalid
	}
	if source == "/workspace" || strings.HasPrefix(source, "/workspace/") {
		source = filepath.Join(b.workspace, strings.TrimPrefix(source, "/workspace"))
	}
	// Refuse recursive installation into the selected source itself.
	if containsPath(source, b.capabilityRoot) || containsPath(b.capabilityRoot, source) {
		return nil, agentcapabilities.ErrInvalid
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, agentcapabilities.ErrInvalid
	}
	return root, nil
}
