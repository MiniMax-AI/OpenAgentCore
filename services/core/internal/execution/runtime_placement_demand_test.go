package execution

import (
	"context"
	"errors"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

func TestPlacementInventoryTimeoutRetainsOwnerAndCursor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		fatal   bool
	}{
		{"bounded read", context.DeadlineExceeded, false},
		{"database failure", errors.New("database unavailable"), true},
		{"cancellation", context.Canceled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testRuntimeManager(t)
			before := deployment.PlacementDemandCursor{EnvironmentID: "cursor"}
			m.placementCursor = before
			m.deploymentReader = &strictDeploymentReader{t: t, placementDemand: func(context.Context, deployment.PlacementDemandCursor) ([]deployment.PlacementDemand, deployment.PlacementDemandCursor, error) {
				return nil, deployment.PlacementDemandCursor{}, tc.failure
			}}
			err := m.reservePlacements(t.Context())
			if (err != nil) != tc.fatal {
				t.Fatal(err)
			}
			if m.placementCursor != before {
				t.Fatal("failed page advanced cursor", m.placementCursor)
			}
		})
	}
}

func TestPlacementInventoryTimeoutNeverMasksParentCancellationOrLostLease(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		m := testRuntimeManager(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		if canceled {
			cancel()
		} else {
			m.lease = lostLease{}
		}
		m.deploymentReader = &strictDeploymentReader{t: t, placementDemand: func(context.Context, deployment.PlacementDemandCursor) ([]deployment.PlacementDemand, deployment.PlacementDemandCursor, error) {
			return nil, deployment.PlacementDemandCursor{}, context.DeadlineExceeded
		}}
		if err := m.reservePlacements(ctx); err == nil {
			t.Fatal("timeout masked cancellation or lost ownership")
		}
	}
}

func TestPlacementReservationTimeoutYieldsOnlyAttemptedDemand(t *testing.T) {
	for _, size := range []int{2, 32} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := testRuntimeManager(t)
			m.config.InstallationID = uuid.NewString()
			horizon := time.Now().UTC()
			rows := make([]deployment.PlacementDemand, size)
			for i := range rows {
				rows[i] = deployment.PlacementDemand{UnallocatedEnvironment: deployment.UnallocatedEnvironment{ID: uuid.NewString(), TenantID: uuid.NewString()}, At: horizon.Add(time.Duration(i-size) * time.Second)}
			}
			reads := 0
			m.deploymentReader = &strictDeploymentReader{t: t, placementDemand: func(ctx context.Context, after deployment.PlacementDemandCursor) ([]deployment.PlacementDemand, deployment.PlacementDemandCursor, error) {
				reads++
				if ctx.Err() != nil {
					t.Fatal("page read inherited expired budget", ctx.Err())
				}
				if reads == 1 {
					next := deployment.PlacementDemandCursor{Until: horizon}
					if size == 32 {
						next.At = rows[size-1].At
						next.EnvironmentID = rows[size-1].ID
					}
					return rows, next, nil
				}
				want := deployment.PlacementDemandCursor{At: rows[0].At, EnvironmentID: rows[0].ID, Until: horizon}
				if after != want {
					t.Fatalf("later unattempted rows were skipped or horizon changed: got %#v, want %#v", after, want)
				}
				return rows[1:], deployment.PlacementDemandCursor{Until: horizon}, nil
			}}
			attempts := []string{}
			_, m.deployment = deploymentOperations(t, &strictDeploymentStorage{t: t}, &strictDeploymentReader{t: t}, &strictExecutionStorage{t: t, withReservation: func(ctx context.Context, key deployment.AllocationKey, _ func(sessions.LockedSession, deployment.ReservationTx) error) error {
				if ctx.Err() != nil {
					t.Fatal("reservation attempted with spent context", ctx.Err())
				}
				attempts = append(attempts, key.EnvironmentID)
				if len(attempts) == 1 {
					return context.DeadlineExceeded
				}
				return placement.ErrNodeUnavailable
			}})
			if err := m.reservePlacements(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 {
				t.Fatal("spent reservation budget advanced later rows", attempts)
			}
			if err := m.reservePlacements(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(attempts) != size {
				t.Fatal("later rows did not get a fresh attempt", attempts)
			}
			for i, id := range attempts {
				if id != rows[i].ID {
					t.Fatal("attempt order changed", attempts)
				}
			}
			if m.placementCursor != (deployment.PlacementDemandCursor{}) {
				t.Fatal("completed short page did not wrap", m.placementCursor)
			}
		})
	}
}

func TestPlacementBudgetEndsBeforeAttemptPreservesCursor(t *testing.T) {
	m := testRuntimeManager(t)
	before := deployment.PlacementDemandCursor{EnvironmentID: uuid.NewString(), At: time.Now().Add(-time.Minute), Until: time.Now()}
	m.placementCursor = before
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.deploymentReader = &strictDeploymentReader{t: t, placementDemand: func(context.Context, deployment.PlacementDemandCursor) ([]deployment.PlacementDemand, deployment.PlacementDemandCursor, error) {
		cancel()
		return []deployment.PlacementDemand{{UnallocatedEnvironment: deployment.UnallocatedEnvironment{ID: uuid.NewString(), TenantID: uuid.NewString()}}}, deployment.PlacementDemandCursor{Until: before.Until}, nil
	}}
	// There is deliberately no deployment executor: no reservation may start
	// after the read exhausts the parent budget.
	if err := m.reservePlacements(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if m.placementCursor != before {
		t.Fatal("unattempted demand advanced cursor", m.placementCursor)
	}
}
