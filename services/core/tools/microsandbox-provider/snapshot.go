//go:build linux

package main

import (
	"context"
	"fmt"
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
	identity := wire.SnapshotIdentity{Reference: selector, ID: artifact.ID(), Digest: artifact.Digest(), CheckpointID: state.Checkpoint.CheckpointID, CheckpointRoot: report.Checkpoint.Root, OperationID: operation, SourceName: source.Name, SourceID: source.ID, SourceGeneration: source.Generation}
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
		_, err = sdk.Snapshot.Create(ctx, sdk.SnapshotCreateOptions{
			Name: "s-" + q.OperationID, Group: wire.Name(b.q.Config, b.q.Reference, 0), FromSandbox: q.Source.Name,
			Full: true, RecordIntegrity: true, GuestFlush: sdk.GuestFlushSkip, Labels: b.snapshotLabels(q.OperationID, q.Source)})
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
	if q.ObserveOnly {
		// A completed operation receipt remains observable for cleanup even if
		// its source or resource proof is no longer qualified for execution.
		return observeCapturedSnapshot(q.Source, snap, func(source wire.Compute) (wire.State, error) {
			_, state, err := b.inspectOwned(ctx, source)
			return state, err
		})
	}
	if e = qualifySnapshotResources(b.q.Config, artifact.Labels()); e != nil {
		return wire.State{}, e
	}
	h, _, e := b.inspect(ctx, q.Source)
	if e != nil && !sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return wire.State{}, e
	}
	if e == nil {
		// Graceful stop could run the captured source after the checkpoint.
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
	}
	return wire.State{Compute: q.Source, Status: "suspended", BootstrapComplete: true, Snapshot: &snap, SourceStopped: true}, nil
}
func (b backend) resume(ctx context.Context, q wire.ResumeRequest) (wire.State, error) {
	// Verify the exact full artifact before either adopting or creating a target.
	artifact, e := b.verifiedSnapshot(ctx, q.Snapshot)
	if e != nil {
		return wire.State{}, e
	}
	if e = qualifySnapshotResources(b.q.Config, artifact.Labels()); e != nil {
		return wire.State{}, e
	}
	_, _, e = b.inspectOwned(ctx, q.Target)
	if e == nil {
		return b.finishRestore(ctx, q.Target)
	}
	if !sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return wire.State{}, e
	}
	if q.ObserveOnly || q.Target.ID != "" {
		return wire.State{}, wire.ErrUnconfirmed
	}
	source, err := sdk.GetSandbox(ctx, q.Snapshot.SourceName)
	if err == nil {
		if source.ID() != q.Snapshot.SourceID {
			return wire.State{}, sandbox.ErrOwnership
		}
		if !terminal(source.Status()) {
			return wire.State{}, wire.ErrUnconfirmed
		}
	} else if !sdk.IsKind(err, sdk.ErrSandboxNotFound) {
		return wire.State{}, err
	}
	live, e := sdk.RestoreSandbox(ctx, artifact, q.Target.Name, sdk.WithRestoreNetworkPolicy(b.network()))
	if e != nil {
		return wire.State{}, e
	}
	target := q.Target
	target.ID = live.ID()
	// The same completion path handles a fresh target and a previous Restore
	// whose response or derived proof write was interrupted.
	state, err := b.finishRestore(ctx, target)
	detachErr := live.Detach(context.Background())
	if err != nil {
		return wire.State{}, err
	}
	if detachErr != nil {
		return wire.State{}, detachErr
	}
	return state, nil
}

// Observation consumes an already verified artifact identity and never changes
// source state. Execution qualification belongs to capture and subsequent use.
func observeCapturedSnapshot(source wire.Compute, snapshot wire.SnapshotIdentity, readOwned func(wire.Compute) (wire.State, error)) (wire.State, error) {
	state, err := readOwned(source)
	if err != nil && !sdk.IsKind(err, sdk.ErrSandboxNotFound) {
		return wire.State{}, err
	}
	stopped := sdk.IsKind(err, sdk.ErrSandboxNotFound) || terminal(sdk.SandboxStatus(state.Status))
	status := "suspended"
	if !stopped {
		status = state.Status
	}
	return wire.State{Compute: source, Status: status, BootstrapComplete: true, Snapshot: &snapshot, SourceStopped: stopped}, nil
}

func terminal(s sdk.SandboxStatus) bool {
	return s == sdk.SandboxStatusStopped || s == sdk.SandboxStatusCrashed
}
