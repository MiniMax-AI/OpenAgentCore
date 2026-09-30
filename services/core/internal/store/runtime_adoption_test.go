package store

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func legacyAdoptionFixture(t *testing.T) (*Store, *Store, deployment.ProcessDeployment, RuntimeAllocation) {
	t.Helper()
	s, _ := newManagedTestStore(t)
	w := executionWriter(t, s)
	d := deploymentSelection()
	deploymentConfigure(t, w, &d)
	tenant := uuid.NewString()
	_, e := localEnvironment(t, s, tenant)
	a, err := w.ReserveRuntimeAllocation(t.Context(), tenant, e.ID, d.InstallationID, runtimedevice.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	d.ProviderKind = "docker"
	d.LocalNodeID = uuid.NewString()
	d.LocalCredentialSHA256 = runtimedevice.HashCredential("node")
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
func TestHistoricalRuntimeResourcesCannotBeAdopted(t *testing.T) {
	for _, state := range []string{"creating", "running", "cleanup_pending", "released"} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pending=%t", state, pending), func(t *testing.T) {
				s, w, d, a := legacyAdoptionFixture(t)
				runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_allocations SET state=$2,create_settled=($2='released'),released_at=CASE WHEN $2='released' THEN clock_timestamp() ELSE NULL END WHERE id=$1`, a.ID, state)
				if pending {
					_, _ = localEnvironment(t, s, a.TenantID)
				}
				before, err := s.GetRuntimeAllocation(t.Context(), a.TenantID, a.EnvironmentID)
				if err != nil {
					t.Fatal(err)
				}
				err = deploymentExecution(t, w).ConfigureProcess(t.Context(), &d)
				if state == "released" && !pending {
					if err != nil {
						t.Fatal("released history blocked fresh configuration", err)
					}
				} else {
					if err == nil {
						t.Fatal("historical resources adopted")
					}
					requireNoLegacyBinding(t, s)
				}
				after, err := s.GetRuntimeAllocation(t.Context(), a.TenantID, a.EnvironmentID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("retained receipt changed", err)
				}
			})
		}
	}
}
