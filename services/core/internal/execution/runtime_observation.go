package execution

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func (r *runtimeLifecycle) recordObservation(ctx context.Context, owner deployment.Allocation, observed error) {
	if owner.NodeID == "" {
		return
	}
	diagnostic := deployment.AllocationObserved
	if observed != nil {
		switch {
		case errors.Is(observed, sandbox.ErrOwnership):
			diagnostic = deployment.AllocationOwnershipMismatch
		case errors.Is(observed, sandbox.ErrNotFound):
			diagnostic = deployment.AllocationComputeUnconfirmed
			if owner.CreateSettled {
				diagnostic = deployment.AllocationResourceMissing
			}
		case errors.Is(observed, sandbox.ErrComputeUnconfirmed):
			diagnostic = deployment.AllocationComputeUnconfirmed
		default:
			diagnostic = deployment.AllocationProviderUnavailable
			if online, err := r.reader.NodeOnline(ctx, owner.NodeID); err == nil && !online {
				diagnostic = deployment.AllocationNodeUnavailable
			}
		}
	}
	// Reconciliation remains authoritative; diagnostics must not interrupt cleanup.
	_ = r.deployment.RecordObservation(ctx, owner, diagnostic)
}
