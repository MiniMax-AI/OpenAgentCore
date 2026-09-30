package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func lifecycleTestNode(t *testing.T, s *Store) string {
	t.Helper()
	nodes := deploymentService(t, s)
	token, err := EnrollmentTestToken(nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 100, MaxRetained: 100}))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	_, err = nodes.Enroll(t.Context(), token, deployment.Enrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: id, Credential: strings.Repeat("n", 64), Name: "second", Provider: "docker", BackendFingerprint: strings.Repeat("b", 64), CoreURL: s.placement.PublicURL()})
	if err != nil {
		t.Fatal(err)
	}
	onlineManagerNode(t, s, id)
	return id
}
func lifecycleTestSession(t *testing.T, s *Store, node string) (string, sessions.Session) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := createSessionOnNode(t, s, tenant, managerSessionInput(uuid.NewString()), node)
	if err != nil {
		t.Fatal(err)
	}
	return tenant, session
}
func lifecycleTestAllocation(t *testing.T, s, w *Store, d deployment.ProcessDeployment, node string) deployment.Allocation {
	t.Helper()
	tenant, session := lifecycleTestSession(t, s, node)
	allocation, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID}, d.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
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
			rows, err := deploymentStore(w).LifecycleAllocations(t.Context(), node, cursor)
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
			rows, err := deploymentStore(w).UnallocatedEnvironments(t.Context(), node, cursor)
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
	if rows, err := deploymentStore(w).LifecycleAllocations(t.Context(), "", ""); err != nil || len(rows) != 0 {
		t.Fatal("managed allocation in the nodeless lane", rows, err)
	}
	if rows, err := deploymentStore(w).UnallocatedEnvironments(t.Context(), "", ""); err != nil || len(rows) != 0 {
		t.Fatal("managed pending Environment in the nodeless lane", rows, err)
	}
}

func TestRuntimeLifecycleNodeInventoryAndRouting(t *testing.T) {
	s, w, d := managerFixture(t, 100, 100)
	other := lifecycleTestNode(t, s)
	tenant, session := lifecycleTestSession(t, s, other)
	environment := session.Environment.ID
	checkRoute := func(want string, wantErr error) {
		t.Helper()
		got, err := deploymentService(t, w).LifecycleNode(t.Context(), tenant, environment)
		if got != want || !errors.Is(err, wantErr) {
			t.Fatal("route", got, err, want, wantErr)
		}
	}
	checkRoute(other, nil) // Pending has no allocation yet.
	if _, err := deploymentService(t, w).LifecycleNode(t.Context(), uuid.NewString(), environment); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("tenant boundary", err)
	}
	owner, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment}, d.InstallationID, runtimedevice.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", other); err != nil {
		t.Fatal(err)
	}
	checkRoute(other, nil)
	nodes, err := deploymentStore(w).LifecycleNodes(t.Context())
	if err != nil || len(nodes) != 2 || !slices.Contains(nodes, other) {
		t.Fatal("offline node omitted", nodes, err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET node_id=NULL WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	checkRoute("", placement.ErrNodeUnavailable)
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET node_id=$1 WHERE id=$2", other, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	checkRoute(other, nil) // Deletion does not discard cleanup routing.
	owner, err = deploymentExecution(t, w).SettleCreation(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = deploymentExecution(t, w).RequestCleanup(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = deploymentExecution(t, w).ReleaseAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	checkRoute(other, nil)
	if rows, err := deploymentStore(w).LifecycleAllocations(t.Context(), other, ""); err != nil || len(rows) != 0 {
		t.Fatal("released allocation scanned", rows, err)
	}
	if err := deploymentService(t, s).RemoveNode(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	nodes, err = deploymentStore(w).LifecycleNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0] != d.LocalNodeID {
		t.Fatal("removed node discovered", nodes, err)
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
			if _, err := deploymentService(t, w).LifecycleNode(t.Context(), tenant, session.Environment.ID); !errors.Is(err, placement.ErrNodeUnavailable) {
				t.Fatal("invalid placement routed", err)
			}
			rows, err := deploymentStore(w).UnallocatedEnvironments(t.Context(), d.LocalNodeID, "")
			if err != nil || len(rows) != 0 {
				t.Fatal("invalid placement provisioned", rows, err)
			}
		})
	}
}

func TestRuntimeLifecycleNodelessLane(t *testing.T) {
	s, w, _, a := nodelessAllocationFixture(t)
	nodes, err := deploymentStore(w).LifecycleNodes(t.Context())
	if err != nil || !slices.Equal(nodes, []string{""}) {
		t.Fatal(nodes, err)
	}
	node, err := deploymentService(t, w).LifecycleNode(t.Context(), a.TenantID, a.EnvironmentID)
	if err != nil || node != "" {
		t.Fatal(node, err)
	}
	rows, err := deploymentStore(w).LifecycleAllocations(t.Context(), "", "")
	if err != nil || len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatal(rows, err)
	}
	tenant := uuid.NewString()
	_, e := localEnvironment(t, s, tenant)
	pending, err := deploymentStore(w).UnallocatedEnvironments(t.Context(), "", "")
	if err != nil || len(pending) != 1 || pending[0].ID != e.ID {
		t.Fatal(pending, err)
	}
	if _, err := deploymentStore(w).LifecycleAllocations(t.Context(), "bad", ""); err == nil {
		t.Fatal("invalid node accepted")
	}
	if _, err := deploymentStore(w).UnallocatedEnvironments(t.Context(), "", "bad"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}
