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
	diagnostic := ""
	if observed != nil {
		switch {
		case errors.Is(observed, sandbox.ErrOwnership):
			diagnostic = "ownership_mismatch"
		case errors.Is(observed, sandbox.ErrNotFound):
			diagnostic = "compute_unconfirmed"
			if owner.CreateSettled {
				diagnostic = "resource_missing"
			}
		case errors.Is(observed, sandbox.ErrComputeUnconfirmed), errors.Is(observed, sandbox.ErrCommandUnconfirmed):
			diagnostic = "compute_unconfirmed"
		default:
			diagnostic = "provider_unavailable"
			if online, err := r.reader.NodeOnline(ctx, owner.NodeID); err == nil && !online {
				diagnostic = "node_unavailable"
			}
		}
	}
	// Reconciliation remains authoritative; diagnostics must not interrupt cleanup.
	_ = r.deployment.RecordObservation(ctx, owner, diagnostic)
}
