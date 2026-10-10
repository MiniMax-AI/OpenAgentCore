package deployment

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// EndRetentionForDemand brings forward the existing retention deadline only
// when a stopped, idle owner can keep its native history and release a needed
// retained slot. The ordinary expiry cleanup still proves every resource gone.
func (e *ExecutionOperations) EndRetentionForDemand(ctx context.Context, owner Allocation) (Allocation, error) {
	if owner.NodeID == "" || owner.ComputePhase != "suspended" {
		return owner, nil
	}
	return e.change(ctx, owner, true, func(tx AllocationTx, current Allocation) (Allocation, error) {
		if current.State != "running" || current.ComputePhase != "suspended" || current.ComputeRevision != owner.ComputeRevision || current.Expired {
			return Allocation{}, ErrAllocationConflict
		}
		activity, err := tx.LoadActivity(current)
		if err != nil || activity.Busy || activity.WakeRequested {
			return current, err
		}
		retained, err := tx.CanRetainEnvironment(current)
		if err != nil || !retained {
			return current, err
		}
		pressure, err := tx.LoadSuspensionDemand(current)
		if err != nil {
			return current, err
		}
		if pressure.Deployment.Resetting || pressure.Deployment.Mode != string(sandbox.DeploymentNodes) || pressure.Deployment.InstallationID != current.ProviderKey {
			return current, nil
		}
		needed, err := e.placementNeedsCapacity(tx, pressure, current.NodeID, false, true)
		if err != nil {
			return current, err
		}
		if needed {
			return tx.SetCompute(current, ComputeChange{Phase: "suspended", State: current.ComputeState, RetainedUntil: &activity.ObservedAt})
		}
		return current, nil
	})
}
