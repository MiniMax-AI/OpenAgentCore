package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

// provisionPending shares the existing lifecycle owner and serial gate. This
// also recovers idle Session creation interrupted after its database commit.
func (r *runtimeLifecycle) provisionPending(ctx context.Context) error {
	rows, err := r.reader.UnallocatedEnvironments(ctx, r.nodeID, r.pendingCursor)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		r.pendingCursor = ""
		return nil
	}
	for _, environment := range rows {
		r.pendingCursor = environment.ID
		provider := r.config.InstallationID
		operation, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := r.provision(operation, environment.TenantID, environment.ID, provider)
		cancel()
		if err != nil {
			if ownership := r.lease.CheckOwnership(ctx); ownership != nil {
				return ownership
			}
			log.Ctx(ctx).Warn("managed Runtime bootstrap incomplete", "environment_id", environment.ID)
		}
	}
	return nil
}

// reserveReplacementPlacements runs on the manager's existing inventory clock,
// before node scans. The committed input supplies demand; placement is the
// durable retry intent after the first caller or hint is gone. No native
// operation runs here and no retained idle filesystem consumes a reservation.
func (m *runtimeManager) reserveReplacementPlacements(ctx context.Context) error {
	m.mu.Lock()
	configuration := m.config
	m.mu.Unlock()
	if m.workspaces == nil || configuration.Workspace == nil {
		return nil
	}
	operation, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := m.deploymentReader.ReplacementEnvironments(operation, m.replacementCursor)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		m.replacementCursor = ""
		return nil
	}
	for _, environment := range rows {
		m.replacementCursor = environment.ID
		_, err = m.deployment.EnsurePlacement(operation, deployment.AllocationKey{TenantID: environment.TenantID, EnvironmentID: environment.ID}, configuration.InstallationID)
		if err != nil {
			if ownership := m.lease.CheckOwnership(ctx); ownership != nil {
				return ownership
			}
			log.Ctx(ctx).Warn("managed Runtime placement incomplete", "environment_id", environment.ID)
		}
	}
	return nil
}
