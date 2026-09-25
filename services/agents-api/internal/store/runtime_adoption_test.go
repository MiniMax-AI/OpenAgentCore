package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
)

func legacyAdoptionFixture(t *testing.T) (*Store, *Store, RuntimeDeployment, RuntimeAllocation) {
	t.Helper()
	s, _ := newManagedTestStore(t)
	w := executionLease(t, s).Store()
	d := deploymentSelection()
	deploymentConfigure(t, w, &d)
	tenant := uuid.NewString()
	_, e := localEnvironment(t, s, tenant)
	a, err := w.ReserveRuntimeAllocation(t.Context(), tenant, e.ID, d.InstallationID, device.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	d.ProviderKind = "docker"
	d.LocalNodeID = uuid.NewString()
	d.LocalCredentialSHA256 = device.HashCredential("node")
	d.LocalMaxActive, d.LocalMaxRetained = 4, 16
	return s, w, d, a
}
func requireNoLegacyBinding(t *testing.T, s *Store) {
	t.Helper()
	var nodes, placements, bound int
	var kind string
	if err := s.pool.QueryRow(t.Context(), `SELECT provider_kind,(SELECT count(*) FROM runtime_nodes),(SELECT count(*) FROM runtime_placements),(SELECT count(*) FROM runtime_allocations WHERE node_id IS NOT NULL) FROM runtime_deployment`).Scan(&kind, &nodes, &placements, &bound); err != nil {
		t.Fatal(err)
	}
	if kind != "" || nodes != 0 || placements != 0 || bound != 0 {
		t.Fatal("partial adoption", kind, nodes, placements, bound)
	}
}
func TestRuntimeLegacyAdoptionRequiresEveryOwnershipReceipt(t *testing.T) {
	for _, failure := range []string{"no_verifier", "resource_missing", "provider_unavailable", "ownership_mismatch", "snapshot_unconfirmed", "secret diagnostic"} {
		t.Run(failure, func(t *testing.T) {
			s, w, d, _ := legacyAdoptionFixture(t)
			var verify RuntimeOwnershipVerifier
			if failure != "no_verifier" {
				verify = func(context.Context, RuntimeAllocation) error { return RuntimeOwnershipFailure(failure) }
			}
			err := w.ConfigureRuntimeDeployment(t.Context(), &d, verify)
			if !errors.Is(err, ErrRuntimeLegacyOwnership) || strings.Contains(err.Error(), "secret diagnostic") {
				t.Fatal(err)
			}
			if failure != "no_verifier" && failure != "secret diagnostic" && !strings.Contains(err.Error(), failure) {
				t.Fatal("missing safe reason", err)
			}
			requireNoLegacyBinding(t, s)
		})
	}
}
func TestRuntimeLegacyAdoptionRechecksCompletePlan(t *testing.T) {
	for _, change := range []string{"compute_state", "state", "create_settled", "new_allocation", "lease_lost"} {
		t.Run(change, func(t *testing.T) {
			s, w, d, a := legacyAdoptionFixture(t)
			verify := func(ctx context.Context, _ RuntimeAllocation) error {
				switch change {
				case "compute_state":
					runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_allocations SET compute_state='{"changed":true}' WHERE id=$1`, a.ID)
				case "state":
					runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_allocations SET state='cleanup_pending' WHERE id=$1`, a.ID)
				case "create_settled":
					runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_allocations SET create_settled=true WHERE id=$1`, a.ID)
				case "new_allocation":
					_, e := localEnvironment(t, s, a.TenantID)
					added, err := w.ReserveRuntimeAllocation(ctx, a.TenantID, e.ID, d.InstallationID, device.HashCredential("new"))
					if err != nil {
						t.Fatal(err)
					}
					runtimeSuspensionSQL(t, s.pool, "UPDATE runtime_allocations SET id='00000000-0000-0000-0000-000000000001' WHERE id=$1", added.ID)
				case "lease_lost":
					if err := w.executionLease.Close(ctx); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			if err := w.ConfigureRuntimeDeployment(t.Context(), &d, verify); err == nil {
				t.Fatal("changed adoption plan accepted")
			}
			requireNoLegacyBinding(t, s)
		})
	}
}
func TestRuntimeLegacyAdoptionChecksAllPagesAndRollsBack(t *testing.T) {
	s, w, d, a := legacyAdoptionFixture(t)
	for range 33 {
		_, e := localEnvironment(t, s, a.TenantID)
		if _, err := w.ReserveRuntimeAllocation(t.Context(), a.TenantID, e.ID, d.InstallationID, device.HashCredential(uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	verify := func(context.Context, RuntimeAllocation) error {
		calls++
		if calls == 34 {
			return RuntimeOwnershipFailure("ownership_mismatch")
		}
		return nil
	}
	if err := w.ConfigureRuntimeDeployment(t.Context(), &d, verify); err == nil || calls != 34 {
		t.Fatal(calls, err)
	}
	requireNoLegacyBinding(t, s)
}
func TestRuntimeLegacyAdoptionRetainsStatesAndIgnoresReleasedHistory(t *testing.T) {
	for _, state := range []string{"creating", "running", "cleanup_pending", "released"} {
		t.Run(state, func(t *testing.T) {
			s, w, d, a := legacyAdoptionFixture(t)
			runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_allocations SET state=$2,create_settled=($2='released'), released_at=CASE WHEN $2='released' THEN clock_timestamp() ELSE NULL END WHERE id=$1`, a.ID, state)
			pending, _ := localEnvironment(t, s, a.TenantID)
			calls := 0
			verify := func(context.Context, RuntimeAllocation) error { calls++; return nil }
			if err := w.ConfigureRuntimeDeployment(t.Context(), &d, verify); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetRuntimeAllocation(t.Context(), a.TenantID, a.EnvironmentID)
			if err != nil || got.State != state || got.CreateSettled != (state == "released") {
				t.Fatal(got, err)
			}
			if state == "released" {
				if calls != 0 || got.NodeID != "" {
					t.Fatal("released history was assigned", calls, got)
				}
				if _, err := sessionRuntimePlacement(t.Context(), s, a.TenantID, a.SessionID); !errors.Is(err, ErrNotFound) {
					t.Fatal("released history placement fabricated", err)
				}
			} else if calls != 1 || got.NodeID != d.LocalNodeID {
				t.Fatal(calls, got)
			}
			if placement, err := sessionRuntimePlacement(t.Context(), s, a.TenantID, pending.ID); err != nil || placement.NodeID != d.LocalNodeID {
				t.Fatal(placement, err)
			}
			if err := w.ConfigureRuntimeDeployment(t.Context(), &d, func(context.Context, RuntimeAllocation) error { t.Fatal("reverified fixed placement"); return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRuntimeLegacyAdoptionFinalRowLockIsBounded(t *testing.T) {
	s, w, d, a := legacyAdoptionFixture(t)
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	verify := func(context.Context, RuntimeAllocation) error {
		if _, err := tx.Exec(t.Context(), "SELECT id FROM runtime_allocations WHERE id=$1 FOR UPDATE", a.ID); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	started := time.Now()
	if err := w.ConfigureRuntimeDeployment(t.Context(), &d, verify); err == nil {
		t.Fatal("locked final receipt admitted")
	}
	if time.Since(started) > executionTransactionTimeout+2*time.Second {
		t.Fatal("unbounded final lock wait")
	}
	requireNoLegacyBinding(t, s)
}
