package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// demandReader answers the placement demand recheck and may report a released allocation.
type demandReader struct {
	deployment.Reader
	demanded bool
	released bool
	checks   int
}

func (r *demandReader) PlacementDemanded(context.Context, deployment.AllocationKey) (bool, error) {
	r.checks++
	return r.demanded, nil
}

func (r *demandReader) EnvironmentAllocation(ctx context.Context, key deployment.AllocationKey) (deployment.Allocation, error) {
	if r.released {
		return deployment.Allocation{State: "released"}, nil
	}
	return r.Reader.EnvironmentAllocation(ctx, key)
}

// Input that expired after the placement was reserved prepares no workspace or sandbox and gives the reserved node back.
func TestInputExpiringAfterPlacementPreparesNothing(t *testing.T) {
	for name, tc := range map[string]struct{ released bool }{"never allocated": {}, "released allocation": {released: true}} {
		t.Run(name, func(t *testing.T) {
			provider := &retentionProvider{}
			f := newWorkspaceSettlementFixture(t, provider)
			r, s := f.lifecycle, f.session
			r.config.Mode = string(sandbox.DeploymentNodes)
			reader := &demandReader{Reader: r.reader, released: tc.released}
			r.reader = reader
			_, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
			if !errors.Is(err, deployment.ErrAllocationConflict) {
				t.Fatal(err)
			}
			var workspaces, allocations int
			if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM environment_workspaces),(SELECT count(*) FROM runtime_allocations)`).Scan(&workspaces, &allocations); err != nil {
				t.Fatal(err)
			}
			target, err := reader.Reader.LifecyclePlacement(t.Context(), deployment.AllocationKey{TenantID: s.TenantID, EnvironmentID: s.Environment.ID})
			if err != nil || workspaces != 0 || allocations != 0 || provider.creates != 0 || !target.PlacementReleased {
				t.Fatal("prepared for expired input", workspaces, allocations, provider.creates, target, err)
			}
		})
	}
}

// A direct deployment has no placement demand to check.
func TestDirectDeploymentSkipsPlacementDemandCheck(t *testing.T) {
	f := newWorkspaceSettlementFixture(t, &retentionProvider{})
	r, s := f.lifecycle, f.session
	r.config.Mode = string(sandbox.DeploymentDirect)
	reader := &demandReader{Reader: r.reader}
	r.reader = reader
	_, _ = r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
	if reader.checks != 0 {
		t.Fatal("direct deployment checked placement demand", reader.checks)
	}
}
