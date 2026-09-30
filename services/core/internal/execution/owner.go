package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Ownership is the execution lease as the Worker uses it. *pgunit.Lease implements it.
type Ownership interface {
	CheckOwnership(context.Context) error
	CancelOperations(context.Context, context.CancelFunc) error
	Close(context.Context) error
}

// Owner is everything bound to one execution lease. Later cutovers add one explicit
// field per domain's execution operations and delete the matching store calls.
type Owner struct {
	Lease      Ownership
	Store      *store.Store                    // the remaining store execution operations, built by store.NewExecution(s, lease)
	Deployment *deployment.ExecutionOperations // sandbox deployment changes on the lease-bound deploymentpg storage
}

// leaseCloseTimeout bounds releasing the lease once the Worker owns it.
const leaseCloseTimeout = 5 * time.Second

// closeLease releases the lease within its own deadline, independent of the
// cancellation of the request or run that ends ownership.
func closeLease(ctx context.Context, lease Ownership) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaseCloseTimeout)
	defer cancel()
	return lease.Close(ctx)
}
