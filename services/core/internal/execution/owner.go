package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
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
	Sessions   *sessions.ExecutionOperations   // Session execution operations on the lease-bound sessionpg storage
}

// Bind returns a copy of d bound to owner's execution operations. StartWorker
// binds through it; a Dispatcher that runs Turns outside a Worker binds the same way.
func (d *Dispatcher) Bind(owner Owner) (*Dispatcher, error) {
	if owner.Store == nil {
		return nil, errors.New("execution requires the execution Store")
	}
	if owner.Sessions == nil {
		return nil, errors.New("execution requires the Session execution operations")
	}
	owned := *d
	owned.Store = owner.Store
	owned.sessionExecution = owner.Sessions
	return &owned, nil
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
