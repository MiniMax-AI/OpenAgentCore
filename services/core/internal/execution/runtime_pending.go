package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// provisionPending shares the existing lifecycle owner and serial gate. This
// also recovers idle Session creation interrupted after its database commit.
func (r *runtimeLifecycle) provisionPending(ctx context.Context) error {
	if r.config.AdmissionPaused && r.config.Generation == 0 {
		return nil
	}
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
