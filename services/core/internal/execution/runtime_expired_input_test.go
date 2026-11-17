package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// expireInitialInput gives the fixture Session an initial input that expired.
func expireInitialInput(t *testing.T, f workspaceSettlementFixture) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO environment_input_reservations(id,session_id,idempotency_key,batch,settings,is_initial,created_at,deadline,state,settled_at) VALUES($1,$2,$3,'[{}]','{}',true,clock_timestamp()-interval '10 minutes',clock_timestamp()-interval '5 minutes','expired',clock_timestamp()-interval '5 minutes')`, uuid.NewString(), f.session.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

// Input that expired after the placement was reserved prepares no workspace or sandbox and gives the reserved node back.
func TestExpiredInitialInputPreparesNothingAndReleasesTheReservation(t *testing.T) {
	provider := &retentionProvider{}
	f := newWorkspaceSettlementFixture(t, provider)
	r, s := f.lifecycle, f.session
	r.config.Mode = string(sandbox.DeploymentNodes)
	expireInitialInput(t, f)
	_, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
	if !errors.Is(err, deployment.ErrPlacementDemandLapsed) || !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal(err)
	}
	var workspaces, allocations int
	if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM environment_workspaces),(SELECT count(*) FROM runtime_allocations)`).Scan(&workspaces, &allocations); err != nil {
		t.Fatal(err)
	}
	target, err := r.reader.LifecyclePlacement(t.Context(), deployment.AllocationKey{TenantID: s.TenantID, EnvironmentID: s.Environment.ID})
	if err != nil || workspaces != 0 || allocations != 0 || provider.creates != 0 || !target.PlacementReleased {
		t.Fatal("prepared for expired input", workspaces, allocations, provider.creates, target, err)
	}
}

// lapsedReader reports a released allocation whose demand lapsed.
type lapsedReader struct {
	deployment.Reader
	checks int
}

func (r *lapsedReader) PlacementDemandLapsed(context.Context, deployment.AllocationKey) (bool, error) {
	r.checks++
	return true, nil
}

func (r *lapsedReader) EnvironmentAllocation(context.Context, deployment.AllocationKey) (deployment.Allocation, error) {
	return deployment.Allocation{State: "released"}, nil
}

// Released compute whose demand lapsed before preparation prepares nothing and gives the reserved node back.
func TestReleasedAllocationWithLapsedDemandPreparesNothing(t *testing.T) {
	provider := &retentionProvider{}
	f := newWorkspaceSettlementFixture(t, provider)
	r, s := f.lifecycle, f.session
	r.config.Mode = string(sandbox.DeploymentNodes)
	reader := &lapsedReader{Reader: r.reader}
	r.reader = reader
	if _, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID); !errors.Is(err, deployment.ErrPlacementDemandLapsed) {
		t.Fatal(err)
	}
	target, err := reader.Reader.LifecyclePlacement(t.Context(), deployment.AllocationKey{TenantID: s.TenantID, EnvironmentID: s.Environment.ID})
	if err != nil || provider.creates != 0 || !target.PlacementReleased {
		t.Fatal(provider.creates, target, err)
	}
}

// A direct deployment has no placement demand to check, so provisioning never asks for it.
func TestDirectDeploymentSkipsPlacementDemandCheck(t *testing.T) {
	provider := &retentionProvider{}
	f := newWorkspaceSettlementFixture(t, provider)
	r, s := f.lifecycle, f.session
	r.config.Mode = string(sandbox.DeploymentDirect)
	// A reader that would refuse; a direct deployment never asks it.
	checks := 0
	r.reader = &directReader{Reader: r.reader, checks: &checks}
	owner, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
	if checks != 0 || err != nil || owner.ID == "" || provider.creates != 1 {
		t.Fatal("direct deployment", checks, owner, err, provider.creates)
	}
}

type directReader struct {
	deployment.Reader
	checks *int
}

func (r *directReader) PlacementDemandLapsed(context.Context, deployment.AllocationKey) (bool, error) {
	*r.checks++
	return true, nil
}
