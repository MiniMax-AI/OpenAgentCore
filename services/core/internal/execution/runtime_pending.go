package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
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

// reservePlacements runs under the inventory gate before node scans. Committed
// Sessions and inputs supply demand; a placement survives a disconnected caller.
// No native operation runs here or consumes a reservation for retained idle files.
func (m *runtimeManager) reservePlacements(ctx context.Context) error {
	m.mu.Lock()
	configuration := m.config
	m.mu.Unlock()
	if configuration.Mode != string(sandbox.DeploymentNodes) {
		return nil
	}
	operation, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, next, err := m.deploymentReader.PlacementDemand(operation, m.placementCursor)
	if err != nil {
		// A bounded inventory read may time out while the execution owner is
		// still healthy. Keep its cursor for the next hint or maintenance tick.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return m.lease.CheckOwnership(ctx)
		}
		return err
	}
	if len(rows) == 0 {
		m.placementCursor = deployment.PlacementDemandCursor{}
		return nil
	}
	for _, environment := range rows {
		if operation.Err() != nil {
			if ownership := m.lease.CheckOwnership(ctx); ownership != nil {
				return ownership
			}
			return ctx.Err()
		}
		reserved, err := m.deployment.EnsurePlacement(operation, deployment.AllocationKey{TenantID: environment.TenantID, EnvironmentID: environment.ID}, configuration.InstallationID)
		// A timed-out attempt yields its position, but later rows have not
		// been attempted and must receive a fresh budget on the next scan.
		m.placementCursor = deployment.PlacementDemandCursor{At: environment.At, EnvironmentID: environment.ID, Until: next.Until}
		if err != nil {
			if ownership := m.lease.CheckOwnership(ctx); ownership != nil {
				return ownership
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if operation.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			log.Ctx(ctx).Warn("managed Runtime placement incomplete", "environment_id", environment.ID)
			continue
		}
		node, err := m.node(reserved.NodeID)
		if err != nil {
			return err
		}
		select {
		case node.lifecycle.wakeHints <- struct{}{}:
		default:
		}
		if operation.Err() != nil {
			if ownership := m.lease.CheckOwnership(ctx); ownership != nil {
				return ownership
			}
			return ctx.Err()
		}
	}
	m.placementCursor = next
	if next.EnvironmentID == "" {
		m.placementCursor = deployment.PlacementDemandCursor{}
	}
	return nil
}
