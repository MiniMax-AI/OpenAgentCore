package execution

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// observationFailure retains the receipt held by the operation that failed.
// A later phase must never be queried and relabelled with an earlier failure.
type observationFailure struct {
	owner deployment.Allocation
	cause error
}

func (e *observationFailure) Error() string { return e.cause.Error() }
func (e *observationFailure) Unwrap() error { return e.cause }

func withObservationOwner(owner deployment.Allocation, err error) error {
	if err == nil {
		return nil
	}
	var observed *observationFailure
	if errors.As(err, &observed) {
		return err
	}
	return &observationFailure{owner: owner, cause: err}
}

func (r *runtimeLifecycle) recordObservation(ctx context.Context, owner deployment.Allocation, observed error) {
	var failure *observationFailure
	if errors.As(observed, &failure) {
		owner = failure.owner
	}
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
