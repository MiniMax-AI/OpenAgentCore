package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// VerifyLegacyRuntimeOwnership uses only positive, read-only Provider observations.
// It runs before node placement and never becomes an execution fallback route.
func VerifyLegacyRuntimeOwnership(ctx context.Context, p sandbox.Provider, allocation store.RuntimeAllocation) (err error) {
	failure := "resource_unconfirmed"
	defer func() {
		if err == nil {
			return
		}
		reason := failure
		switch {
		case errors.Is(err, sandbox.ErrOwnership), errors.Is(err, sandbox.ErrInvalid):
			reason = "ownership_mismatch"
		case errors.Is(err, sandbox.ErrNotFound):
			reason = "resource_missing"
		case failure == "snapshot_unconfirmed":
		case errors.Is(err, sandbox.ErrComputeUnconfirmed):
		default:
			reason = "provider_unavailable"
		}
		err = store.RuntimeOwnershipFailure(reason)
	}()
	reference := runtimeReference(allocation)
	if allocation.ComputePhase == "disabled" {
		if p == nil {
			return sandbox.ErrComputeUnconfirmed
		}
		info, err := p.GetInfo(ctx, reference)
		if err != nil {
			return err
		}
		if info.Reference != reference || info.ProviderID == "" {
			return sandbox.ErrOwnership
		}
		return nil
	}
	checkpoint, ok := p.(sandbox.CheckpointProvider)
	if !ok {
		return sandbox.ErrComputeUnconfirmed
	}
	var receipt runtimeCompute
	if json.Unmarshal(allocation.ComputeState, &receipt) != nil || receipt.Current.ID == "" {
		return sandbox.ErrOwnership
	}
	// A consumed restore no longer retains the source's complete compute receipt.
	// Finish that transition with the previous Core instead of reconstructing it.
	if snapshot := receipt.Snapshot; snapshot != nil && (snapshot.SourceID != receipt.Current.ID || snapshot.SourceName != receipt.Current.Name || snapshot.SourceGeneration != receipt.Current.Generation) {
		return sandbox.ErrComputeUnconfirmed
	}
	found := false
	inspect := func(compute sandbox.Compute) error {
		observed, err := checkpoint.GetCompute(ctx, reference, compute)
		if errors.Is(err, sandbox.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if observed.Compute.ID == "" || !sameAdoptionCompute(compute, observed.Compute) {
			return sandbox.ErrOwnership
		}
		found = true
		return nil
	}
	if err := inspect(receipt.Current); err != nil {
		return err
	}
	if receipt.Target != nil {
		if err := inspect(*receipt.Target); err != nil {
			return err
		}
	}
	if receipt.Snapshot != nil || receipt.SuspendID != "" {
		failure = "snapshot_unconfirmed"
		// ObserveOnly verifies an already authorized operation and its full artifact.
		// The helper never pauses, captures, kills or restores on this path.
		var observed sandbox.ComputeState
		var err error
		observed, err = checkpoint.Suspend(ctx, sandbox.SuspendRequest{Reference: reference, OperationID: receipt.SuspendID, Source: receipt.Current, Snapshot: receipt.Snapshot, ObserveOnly: true})
		if err != nil {
			return err
		}
		if !sameAdoptionCompute(receipt.Current, observed.Compute) {
			return sandbox.ErrOwnership
		}
		if receipt.Snapshot != nil && (observed.Snapshot == nil || *receipt.Snapshot != *observed.Snapshot) {
			return sandbox.ErrOwnership
		}
		if observed.Snapshot != nil {
			snapshot := observed.Snapshot
			if snapshot.OperationID != receipt.SuspendID || snapshot.SourceID != receipt.Current.ID || snapshot.SourceName != receipt.Current.Name || snapshot.SourceGeneration != receipt.Current.Generation {
				return sandbox.ErrOwnership
			}
			found = true
		}
	}
	if !found {
		return sandbox.ErrNotFound
	}
	return nil
}

func sameAdoptionCompute(want, got sandbox.Compute) bool {
	if want.Name != got.Name || want.Generation != got.Generation || (want.ID != "" && want.ID != got.ID) || (want.RestoredFrom == nil) != (got.RestoredFrom == nil) {
		return false
	}
	return want.RestoredFrom == nil || *want.RestoredFrom == *got.RestoredFrom
}
