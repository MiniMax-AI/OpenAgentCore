package deployment

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ResetEnvironmentTx is one reset expiry of a standalone Environment.
type ResetEnvironmentTx interface {
	// LoadDeployment locks the deployment and returns it.
	LoadDeployment() (Record, error)
	LoadEnvironment(ctx context.Context) (sessions.Environment, error)
	// FindAllocation returns the Environment's latest allocation.
	FindAllocation() (Allocation, bool, error)
	ExpireEnvironment(ctx context.Context, environment string) error
	// RequestCleanup revokes the allocation's device and requests its cleanup.
	RequestCleanup(current Allocation) error
	// ReleasePlacement releases a reservation that has no allocation.
	ReleasePlacement() error
}

// ExpireResetEnvironment ends an unattached standalone Environment for the
// running reset, which its generation and request time identify. Its
// allocation is revoked and left to the lifecycle's ordinary cleanup; an
// Environment that a Session attached or that already ended is left alone.
func (e *ExecutionOperations) ExpireResetEnvironment(ctx context.Context, key AllocationKey, generation uint64, requestedAt time.Time) error {
	key, err := key.parse()
	if err != nil {
		return err
	}
	return e.storage.WithResetEnvironment(ctx, key, func(tx ResetEnvironmentTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := checkArchiveDeployment(d, generation); err != nil {
			return err
		}
		if err := checkArchiveReset(d, requestedAt); err != nil {
			return err
		}
		environment, err := tx.LoadEnvironment(ctx)
		if err != nil {
			return err
		}
		if environment.SessionOwned || environment.AttachedSessionID != "" || environment.Status == "failed" || environment.Status == "expired" {
			return nil
		}
		current, allocated, err := tx.FindAllocation()
		if err != nil {
			return err
		}
		live := allocated && current.State != "released"
		if live && current.ProviderKey != d.InstallationID {
			return ErrConflict
		}
		if err := tx.ExpireEnvironment(ctx, environment.ID); err != nil {
			return err
		}
		if live {
			return tx.RequestCleanup(current)
		}
		return tx.ReleasePlacement()
	})
}
