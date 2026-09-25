package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
)

func lifecycleTestNode(t *testing.T, s *Store) string {
	t.Helper()
	token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 100, MaxRetained: 100}))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	_, err = s.EnrollRuntimeNode(t.Context(), token, RuntimeNodeEnrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: id, Credential: strings.Repeat("n", 64), Name: "second", Provider: "docker", BackendFingerprint: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	onlineManagerNode(t, s, id)
	return id
}
func lifecycleTestSession(t *testing.T, s *Store, node string) (string, Session) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := createSessionOnNode(t, s, tenant, managerSessionInput(uuid.NewString()), node)
	if err != nil {
		t.Fatal(err)
	}
	return tenant, session
}
func lifecycleTestAllocation(t *testing.T, s, w *Store, d RuntimeDeployment, node string) RuntimeAllocation {
	t.Helper()
	tenant, session := lifecycleTestSession(t, s, node)
	allocation, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, d.InstallationID, device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	return allocation
}

func TestRuntimeLifecycleNodePagesAreIndependent(t *testing.T) {
	s, w, d := managerFixture(t, 100, 100)
	other := lifecycleTestNode(t, s)
	var allocated, pending []string
	for range 34 {
		allocated = append(allocated, lifecycleTestAllocation(t, s, w, d, d.LocalNodeID).ID)
		_, session := lifecycleTestSession(t, s, d.LocalNodeID)
		pending = append(pending, session.Environment.ID)
	}
	second := lifecycleTestAllocation(t, s, w, d, other)
	_, secondPending := lifecycleTestSession(t, s, other)
	// Offline and unresolved cleanup remain discoverable without changing placement.
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET state='cleanup_pending' WHERE node_id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{d.LocalNodeID, other} {
		var gotAlloc, gotPending []string
		cursor := ""
		for range 4 {
			rows, err := w.ListRuntimeAllocationsForNode(t.Context(), node, cursor)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				break
			}
			if len(rows) > 32 {
				t.Fatal("unbounded allocation page")
			}
			for _, a := range rows {
				if a.NodeID != node {
					t.Fatal("cross-node allocation", a)
				}
				gotAlloc = append(gotAlloc, a.ID)
			}
			cursor = rows[len(rows)-1].ID
		}
		cursor = ""
		for range 4 {
			rows, err := w.ListUnallocatedHostedEnvironmentsForNode(t.Context(), node, cursor)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				break
			}
			if len(rows) > 32 {
				t.Fatal("unbounded pending page")
			}
			for _, e := range rows {
				gotPending = append(gotPending, e.ID)
			}
			cursor = rows[len(rows)-1].ID
		}
		wantAlloc, wantPending := allocated, pending
		if node == other {
			wantAlloc = []string{second.ID}
			wantPending = []string{secondPending.Environment.ID}
		}
		slices.Sort(wantAlloc)
		slices.Sort(wantPending)
		if !slices.Equal(gotAlloc, wantAlloc) || !slices.Equal(gotPending, wantPending) {
			t.Fatal("node pagination lost or mixed rows", node, gotAlloc, gotPending)
		}
	}
	if rows, err := w.ListRuntimeAllocationsForNode(t.Context(), "", ""); err != nil || len(rows) != 0 {
		t.Fatal("managed allocation in legacy lane", rows, err)
	}
	if rows, err := w.ListUnallocatedHostedEnvironmentsForNode(t.Context(), "", ""); err != nil || len(rows) != 0 {
		t.Fatal("managed pending in legacy lane", rows, err)
	}
}

func TestRuntimeLifecycleNodeInventoryAndRouting(t *testing.T) {
	s, w, d := managerFixture(t, 100, 100)
	other := lifecycleTestNode(t, s)
	tenant, session := lifecycleTestSession(t, s, other)
	environment := session.Environment.ID
	checkRoute := func(want string, wantErr error) {
		t.Helper()
		got, err := w.ResolveRuntimeLifecycleNode(t.Context(), tenant, environment)
		if got != want || !errors.Is(err, wantErr) {
			t.Fatal("route", got, err, want, wantErr)
		}
	}
	checkRoute(other, nil) // Pending has no allocation yet.
	if _, err := w.ResolveRuntimeLifecycleNode(t.Context(), uuid.NewString(), environment); !errors.Is(err, ErrNotFound) {
		t.Fatal("tenant boundary", err)
	}
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment, d.InstallationID, device.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", other); err != nil {
		t.Fatal(err)
	}
	checkRoute(other, nil)
	nodes, err := w.ListRuntimeLifecycleNodes(t.Context())
	if err != nil || len(nodes) != 2 || !slices.Contains(nodes, other) {
		t.Fatal("offline node omitted", nodes, err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET node_id=NULL WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	checkRoute("", ErrRuntimeNodeUnavailable)
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET node_id=$1 WHERE id=$2", other, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	checkRoute(other, nil) // Deletion does not discard cleanup routing.
	owner, err = w.SettleRuntimeCreation(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.RequestRuntimeCleanup(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.ReleaseRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	checkRoute(other, nil)
	if rows, err := w.ListRuntimeAllocationsForNode(t.Context(), other, ""); err != nil || len(rows) != 0 {
		t.Fatal("released allocation scanned", rows, err)
	}
	if err := s.RemoveRuntimeNode(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	nodes, err = w.ListRuntimeLifecycleNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0] != d.LocalNodeID {
		t.Fatal("removed node discovered", nodes, err)
	}
	if _, err := s.ListRuntimeLifecycleNodes(t.Context()); err == nil {
		t.Fatal("unleased inventory accepted")
	}
}

func TestRuntimeLifecycleNodeRejectsMissingOrReleasedPlacement(t *testing.T) {
	for _, mutation := range []string{"DELETE FROM runtime_placements WHERE environment_id=$1", "UPDATE runtime_placements SET released_at=clock_timestamp() WHERE environment_id=$1"} {
		t.Run(mutation[:6], func(t *testing.T) {
			s, w, d := managerFixture(t, 4, 4)
			tenant, session := lifecycleTestSession(t, s, d.LocalNodeID)
			if _, err := s.pool.Exec(t.Context(), mutation, session.Environment.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := w.ResolveRuntimeLifecycleNode(t.Context(), tenant, session.Environment.ID); !errors.Is(err, ErrRuntimeNodeUnavailable) {
				t.Fatal("invalid placement routed", err)
			}
			rows, err := w.ListUnallocatedHostedEnvironmentsForNode(t.Context(), d.LocalNodeID, "")
			if err != nil || len(rows) != 0 {
				t.Fatal("invalid placement provisioned", rows, err)
			}
		})
	}
}

func TestRuntimeLifecycleLegacyLaneAndOwnerLoss(t *testing.T) {
	s, w, _, a := legacyAdoptionFixture(t)
	nodes, err := w.ListRuntimeLifecycleNodes(t.Context())
	if err != nil || !slices.Equal(nodes, []string{""}) {
		t.Fatal(nodes, err)
	}
	node, err := w.ResolveRuntimeLifecycleNode(t.Context(), a.TenantID, a.EnvironmentID)
	if err != nil || node != "" {
		t.Fatal(node, err)
	}
	rows, err := w.ListRuntimeAllocationsForNode(t.Context(), "", "")
	if err != nil || len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatal(rows, err)
	}
	tenant := uuid.NewString()
	_, e := localEnvironment(t, s, tenant)
	pending, err := w.ListUnallocatedHostedEnvironmentsForNode(t.Context(), "", "")
	if err != nil || len(pending) != 1 || pending[0].ID != e.ID {
		t.Fatal(pending, err)
	}
	if _, err := w.ListRuntimeAllocationsForNode(t.Context(), "bad", ""); err == nil {
		t.Fatal("invalid node accepted")
	}
	if _, err := w.ListUnallocatedHostedEnvironmentsForNode(t.Context(), "", "bad"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	if err := w.executionLease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ListRuntimeLifecycleNodes(t.Context()); err == nil {
		t.Fatal("closed owner enumerated")
	}
	if _, err := w.ListRuntimeAllocationsForNode(t.Context(), "", ""); err == nil {
		t.Fatal("closed owner scanned")
	}
	if _, err := w.ListUnallocatedHostedEnvironmentsForNode(t.Context(), "", ""); err == nil {
		t.Fatal("closed owner provisioned")
	}
	if _, err := w.ResolveRuntimeLifecycleNode(t.Context(), a.TenantID, a.EnvironmentID); err == nil {
		t.Fatal("closed owner routed")
	}
}
