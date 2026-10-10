//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

func (b backend) snapshotLabels(operation string, source wire.Compute) map[string]string {
	labels := wire.Labels(b.q.Config, b.q.Reference)
	labels["io.oac.operation"] = operation
	labels["io.oac.source_id"] = source.ID
	labels["io.oac.source_generation"] = strconv.FormatUint(source.Generation, 10)
	labels[resourceProofLabel] = resourceProof(b.q.Config)
	return labels
}

// inspectSnapshot opens only an allocation/operation-derived selector.
func (b backend) inspectSnapshot(ctx context.Context, operation string, source wire.Compute) (*sdk.SnapshotArtifact, wire.SnapshotIdentity, error) {
	selector := wire.SnapshotReference(b.q.Config, b.q.Reference, operation)
	artifact, e := sdk.Snapshot.Open(ctx, selector)
	if e != nil {
		return nil, wire.SnapshotIdentity{}, e
	}
	if artifact.Scope() != sdk.SnapshotScopeFull || artifact.SourceSandbox() == nil || *artifact.SourceSandbox() != source.Name {
		return nil, wire.SnapshotIdentity{}, sandbox.ErrOwnership
	}
	for key, value := range b.snapshotLabels(operation, source) {
		// Drift must never block ownership-based artifact deletion.
		if key == resourceProofLabel {
			continue
		}
		if artifact.Labels()[key] != value {
			return nil, wire.SnapshotIdentity{}, sandbox.ErrOwnership
		}
	}
	report, e := artifact.Verify(ctx)
	if e != nil {
		return nil, wire.SnapshotIdentity{}, e
	}
	state := artifact.State()
	if report.Digest != artifact.Digest() || report.Checkpoint == nil || report.Checkpoint.Root == "" || report.Checkpoint.Kind == "" || state.Checkpoint == nil || state.Checkpoint.CheckpointID == "" {
		return nil, wire.SnapshotIdentity{}, sandbox.ErrOwnership
	}
	compatibility := sandbox.CheckpointCompatibility{ArtifactDomain: artifact.Labels()["io.oac.artifact_domain"], ExecutionClass: artifact.Labels()["io.oac.execution_class"]}
	identity := wire.SnapshotIdentity{Compatibility: compatibility, Reference: selector, ID: artifact.ID(), Digest: artifact.Digest(), CheckpointID: state.Checkpoint.CheckpointID, CheckpointRoot: report.Checkpoint.Root, OperationID: operation, SourceName: source.Name, SourceID: source.ID, SourceGeneration: source.Generation}
	if wire.ValidateSnapshot(b.q.Config, b.q.Reference, identity) != nil {
		return nil, wire.SnapshotIdentity{}, sandbox.ErrOwnership
	}
	return artifact, identity, nil
}
func (b backend) verifiedSnapshot(ctx context.Context, want wire.SnapshotIdentity) (*sdk.SnapshotArtifact, error) {
	a, got, e := b.inspectSnapshot(ctx, want.OperationID, wire.Compute{Name: want.SourceName, ID: want.SourceID, Generation: want.SourceGeneration})
	if e != nil {
		return nil, e
	}
	if got != want {
		return nil, sandbox.ErrOwnership
	}
	return a, nil
}
func (b backend) suspend(ctx context.Context, q wire.SuspendRequest) (wire.State, error) {
	receipt, receiptErr := b.receipt(q.OperationID)
	if receiptErr == nil {
		if receipt.Deleted || receipt.Snapshot.SourceName != q.Source.Name || receipt.Snapshot.SourceID != q.Source.ID || receipt.Snapshot.SourceGeneration != q.Source.Generation || (q.Snapshot != nil && *q.Snapshot != receipt.Snapshot) {
			return wire.State{}, sandbox.ErrOwnership
		}
		return b.settleSource(ctx, q, receipt)
	} else if !errors.Is(receiptErr, os.ErrNotExist) {
		return wire.State{}, receiptErr
	}

	// The host allocation flock spans pause, capture, verification and source kill.
	// A surviving completed artifact is observed, never overwritten or recaptured.
	artifact, snap, e := b.inspectSnapshot(ctx, q.OperationID, q.Source)
	if sdk.IsKind(e, sdk.ErrSnapshotNotFound) {
		if q.ObserveOnly {
			if q.Snapshot != nil {
				return wire.State{}, wire.ErrUnconfirmed
			}
			_, state, observeErr := b.inspectOwned(ctx, q.Source)
			if observeErr != nil {
				return wire.State{}, observeErr
			}
			if !state.BootstrapComplete || (state.Status != "running" && state.Status != "paused") {
				return wire.State{}, wire.ErrUnconfirmed
			}
			return state, nil
		}
		if q.Snapshot != nil {
			return wire.State{}, wire.ErrUnconfirmed
		}
		source, state, err := b.inspect(ctx, q.Source)
		if err != nil {
			return wire.State{}, err
		}
		if !state.BootstrapComplete {
			return wire.State{}, wire.ErrUnconfirmed
		}
		if err = source.PauseWithGuestFlush(ctx, sdk.GuestFlushSkip); err != nil {
			return wire.State{}, err
		}
		labels := b.snapshotLabels(q.OperationID, q.Source)
		compatibility, err := wire.CheckpointClass(b.q.Config)
		if err != nil {
			return wire.State{}, err
		}
		labels["io.oac.artifact_domain"] = compatibility.ArtifactDomain
		labels["io.oac.execution_class"] = compatibility.ExecutionClass
		var captured struct {
			Labels map[string]string `json:"labels"`
		}
		if json.Unmarshal([]byte(source.ConfigJSON()), &captured) != nil {
			return wire.State{}, sandbox.ErrOwnership
		}
		for _, key := range []string{workspaceModeLabel, workspaceObjectLabel, workspacePathLabel} {
			labels[key] = captured.Labels[key]
		}
		_, err = sdk.Snapshot.Create(ctx, sdk.SnapshotCreateOptions{
			Name: "s-" + q.OperationID, Group: wire.Name(b.q.Config, b.q.Reference, 0), FromSandbox: q.Source.Name,
			Full: true, RecordIntegrity: true, GuestFlush: sdk.GuestFlushSkip, Labels: labels})
		if err != nil {
			return wire.State{}, err
		}
		artifact, snap, e = b.inspectSnapshot(ctx, q.OperationID, q.Source)
	}
	if e != nil {
		return wire.State{}, e
	}
	if q.Snapshot != nil && snap != *q.Snapshot {
		return wire.State{}, sandbox.ErrOwnership
	}
	if e = qualifySnapshotResources(b.q.Config, artifact.Labels()); e != nil {
		return wire.State{}, e
	}
	receipt, e = b.publishArchive(ctx, artifact, snap)
	if e != nil {
		return wire.State{}, e
	}
	return b.settleSource(ctx, q, receipt)
}

func (b backend) settleSource(ctx context.Context, q wire.SuspendRequest, r archiveReceipt) (wire.State, error) {
	if e := b.verifyArchive(r); e != nil {
		if q.ObserveOnly {
			// Ownership comes from the durable receipt, independently of archive
			// usability. Expose cleanup identity without authorizing source transfer,
			// claiming its current state, or weakening the restore integrity check.
			return wire.State{Compute: q.Source, Status: "unknown", Snapshot: &r.Snapshot}, nil
		}
		return wire.State{}, e
	}
	if r.SourceStopped {
		return wire.State{Compute: q.Source, Status: "suspended", BootstrapComplete: true, Snapshot: &r.Snapshot, SourceStopped: true}, nil
	}
	h, state, e := b.inspectOwned(ctx, q.Source)
	if e != nil && !sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return wire.State{}, e
	}
	if e == nil && !terminal(sdk.SandboxStatus(state.Status)) {
		if state.Status != "paused" {
			return wire.State{}, wire.ErrUnconfirmed
		}
		if e = h.Kill(ctx); e != nil {
			return wire.State{}, e
		}
		observed, err := h.Refresh(ctx)
		if err != nil {
			return wire.State{}, err
		}
		if !terminal(observed.Status()) {
			return wire.State{}, fmt.Errorf("source termination unconfirmed")
		}
	} else if sdk.IsKind(e, sdk.ErrSandboxNotFound) && !r.SourceTerminated {
		return wire.State{}, wire.ErrUnconfirmed
	}
	if !r.SourceTerminated {
		r.SourceTerminated = true
		if e = durableJSON(filepath.Join(b.archiveDirectory(q.OperationID), "receipt.json"), r); e != nil {
			return wire.State{}, e
		}
	}
	if h != nil {
		if e = h.Remove(ctx); e != nil {
			return wire.State{}, e
		}
	}
	if _, e = sdk.GetSandbox(ctx, q.Source.Name); !sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return wire.State{}, wire.ErrUnconfirmed
	}
	if e = sdk.Snapshot.Remove(ctx, r.Snapshot.Reference, false); e != nil && !sdk.IsKind(e, sdk.ErrSnapshotNotFound) {
		return wire.State{}, e
	}
	r.SourceStopped = true
	if e = durableJSON(filepath.Join(b.archiveDirectory(q.OperationID), "receipt.json"), r); e != nil {
		return wire.State{}, e
	}
	return wire.State{Compute: q.Source, Status: "suspended", BootstrapComplete: true, Snapshot: &r.Snapshot, SourceStopped: true}, nil
}

func (b backend) resume(ctx context.Context, q wire.ResumeRequest) (wire.State, error) {
	// No managed target may predate its exact admission journal.
	if _, err := os.Lstat(filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")); errors.Is(err, os.ErrNotExist) {
		_, err = sdk.GetSandbox(ctx, q.Target.Name)
		if err == nil {
			return wire.State{}, sandbox.ErrOwnership
		}
		if !sdk.IsKind(err, sdk.ErrSandboxNotFound) {
			return wire.State{}, err
		}
	} else if err != nil {
		return wire.State{}, err
	}

	closed, e := b.admitRestore(q)
	if e != nil {
		return wire.State{}, e
	}
	if closed {
		if !q.ObserveOnly {
			return wire.State{}, wire.ErrUnconfirmed
		}
		target := q.Target
		target.ID = ""
		return wire.State{Compute: target, Status: "absent", RestoreAttemptClosed: q.OperationID}, nil
	}
	// An existing admitted observation may cheaply remain unknown. Native absence
	// cannot close it: a detached launcher could still make the target visible.
	if q.ObserveOnly {
		_, observed, err := b.inspectOwned(ctx, q.Target)
		if sdk.IsKind(err, sdk.ErrSandboxNotFound) {
			return wire.State{}, wire.ErrUnconfirmed
		}
		if err != nil {
			return wire.State{}, err
		}
		if observed.Status != "running" {
			return wire.State{}, wire.ErrUnconfirmed
		}
	}
	// Only a fresh dispatch may close its attempt before native execution.
	// Lifecycle FFI calls use an uncancelled context; the request deadline is
	// checked between settled operations, never used to cancel a native wait.
	if !q.ObserveOnly {
		if err := b.closeExpiredRestore(q); err != nil {
			return wire.State{}, err
		}
	}
	// Admission is durable before any native import/restore operation. A prior
	// admitted operation with no visible target remains unknown, never replayed.
	artifact, e := b.importArchive(ctx, q.Snapshot)
	if e != nil {
		return wire.State{}, e
	}
	if e = qualifySnapshotResources(b.q.Config, artifact.Labels()); e != nil {
		return wire.State{}, e
	}
	if err := qualifyWorkspace(artifact.Labels(), b.q.Workspace); err != nil {
		return wire.State{}, err
	}
	_, _, e = b.inspectOwned(ctx, q.Target)
	if e == nil {
		return b.completeRestore(ctx, q, q.Target)
	}
	if !sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return wire.State{}, e
	}
	if q.ObserveOnly || q.Target.ID != "" {
		return wire.State{}, wire.ErrUnconfirmed
	}
	restore := sdk.RestoreConfig{NetworkPolicy: b.network(), ExternalMountPolicy: sdk.ExternalMountStrict}
	if b.q.Workspace != nil {
		restore.Volumes = map[string]sdk.MountConfig{"/environment": sdk.Mount.Bind(b.q.Workspace.Path, sdk.MountOptions{})}
	}
	if err := b.closeExpiredRestore(q); err != nil {
		return wire.State{}, err
	}
	live, e := sdk.RestoreSandbox(ctx, artifact, q.Target.Name, sdk.WithRestoreConfig(restore))
	if e != nil {
		return wire.State{}, e
	}
	target := q.Target
	target.ID = live.ID()
	// The same completion path handles a fresh target and a previous Restore
	// whose response or derived proof write was interrupted.
	state, err := b.completeRestore(ctx, q, target)
	detachErr := live.Detach(context.Background())
	if err != nil {
		return state, err
	}
	if detachErr != nil {
		return state, detachErr
	}
	return state, nil
}

func terminal(s sdk.SandboxStatus) bool {
	return s == sdk.SandboxStatusStopped || s == sdk.SandboxStatusCrashed
}
