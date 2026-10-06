package store

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func changeNodeTarget(t *testing.T, w *Store, view deployment.View, input sandbox.Selection) (deployment.View, sandbox.Selection) {
	t.Helper()
	input.Resources.CPUs++
	request := input
	request.ExpectedGeneration = view.Generation
	next, err := deploymentExecution(t, w).Update(SandboxResetTestContext(t.Context()), view.InstallationID, request)
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation != view.Generation+1 || next.OwnerEpoch != view.OwnerEpoch {
		t.Fatal("target change replaced execution ownership", next)
	}
	return next, input
}

func generationHeartbeat(t *testing.T, s *Store, node deployment.Enrollment, connection string, view deployment.View, state string) {
	t.Helper()
	err := deploymentService(t, s).HeartbeatGenerations(t.Context(), node.NodeID, connection, view.OwnerEpoch, deployment.NodeHealth{}, []sandbox.GenerationStatus{{Generation: view.Generation, SpecificationDigest: view.SpecificationDigest, State: state}})
	if err != nil {
		t.Fatal(err)
	}
}

func placedGeneration(t *testing.T, s *Store, session sessions.Session) (string, int64) {
	t.Helper()
	var node string
	var generation int64
	if err := s.pool.QueryRow(t.Context(), "SELECT node_id::text,deployment_generation FROM runtime_placements WHERE environment_id=$1", session.Environment.ID).Scan(&node, &generation); err != nil {
		t.Fatal(err)
	}
	return node, generation
}

func TestNodeGenerationsCapacityFallbackAndImmutablePending(t *testing.T) {
	for _, provider := range []string{"microsandbox", "docker"} {
		t.Run(provider, func(t *testing.T) {
			s, w, first, input := webSpecificationFixture(t, provider)
			nodes := deploymentService(t, s)
			a := specificationNode(t, s, first)
			b := specificationNode(t, s, first)
			ca := onlineManagerNode(t, s, a.NodeID)
			cb := onlineManagerNode(t, s, b.NodeID)
			tenant := uuid.NewString()
			pending, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
			if err != nil {
				t.Fatal(err)
			}
			pendingNode, pendingGeneration := placedGeneration(t, s, pending)
			token, err := EnrollmentTestToken(nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 2}))
			if err != nil {
				t.Fatal(err)
			}
			second, input := changeNodeTarget(t, w, first, input)
			if _, err = nodes.NodeConfiguration(t.Context(), "", token, 0); err != nil {
				t.Fatal("update retired enrollment", err)
			}
			if _, err = nodes.AuthenticateNode(t.Context(), a.NodeID, a.Credential); err != nil {
				t.Fatal("update retired node", err)
			}
			generationHeartbeat(t, s, a, ca, second, "preparing")
			fallback, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
			if err != nil {
				t.Fatal("target preparation suppressed fallback", err)
			}
			if _, g := placedGeneration(t, s, fallback); g != 1 {
				t.Fatal(g)
			}
			generationHeartbeat(t, s, a, ca, second, "ready")
			newest, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
			if err != nil {
				t.Fatal(err)
			}
			if n, g := placedGeneration(t, s, newest); n != a.NodeID || g != 2 {
				t.Fatal(n, g)
			}
			// Capacity remains shared across generations. A full newest pin cannot hide B.
			if _, err = s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET max_active=1 WHERE id=$1", a.NodeID); err != nil {
				t.Fatal(err)
			}
			old, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
			if err != nil {
				t.Fatal(err)
			}
			if n, g := placedGeneration(t, s, old); n != b.NodeID || g != 1 {
				t.Fatal("newest full hid older free node", n, g)
			}
			third, _ := changeNodeTarget(t, w, second, input)
			// B finishes a superseded target late: it may describe ownership but cannot adopt it.
			generationHeartbeat(t, s, b, cb, second, "ready")
			list, err := nodes.ListNodes(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range list {
				want := uint64(1)
				if n.ID == a.NodeID {
					want = 2
				}
				if n.Rollout.ReadyGeneration == nil || *n.Rollout.ReadyGeneration != want || !n.ProviderReady {
					t.Fatal("late readiness moved pin or erased serving readiness", n)
				}
			}
			owner, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: pending.Environment.ID}, first.InstallationID, runtimedevice.HashCredential("runtime"))
			if err != nil {
				t.Fatal(err)
			}
			if owner.NodeID != pendingNode || owner.DeploymentGeneration != uint64(pendingGeneration) {
				t.Fatal("pending placement moved", owner)
			}
			n, g, err := deploymentService(t, s).AllocationGeneration(t.Context(), sandbox.Reference{TenantID: tenant, EnvironmentID: owner.EnvironmentID, AllocationID: owner.ID})
			if err != nil || n != pendingNode || g != 1 {
				t.Fatal(n, g, err)
			}
			for _, v := range []deployment.View{first, second, third} {
				target := a
				if v.Generation == 1 {
					target = b
				}
				got, err := nodes.NodeConfiguration(t.Context(), target.NodeID, target.Credential, v.Generation)
				if err != nil || got.SpecificationDigest != v.SpecificationDigest {
					t.Fatal("kept generation unrecoverable", v.Generation, err)
				}
			}
		})
	}
}

func TestNodeGenerationsReconnectAndV1Fallback(t *testing.T) {
	s, w, first, input := webSpecificationFixture(t, "docker")
	service := deploymentService(t, s)
	node := specificationNode(t, s, first)
	old := onlineManagerNode(t, s, node.NodeID)
	second, _ := changeNodeTarget(t, w, first, input)
	nodes, err := service.ListNodes(t.Context())
	if err != nil || !nodes[0].ProviderReady || nodes[0].Rollout.State != "update_required" {
		t.Fatal(nodes, err)
	}
	if _, err = s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal("v1 lost fallback", err)
	}
	connection := uuid.NewString()
	if err = service.ConnectNode(t.Context(), node.NodeID, connection, first.OwnerEpoch); err != nil {
		t.Fatal(err)
	}
	if err = service.HeartbeatGenerations(t.Context(), node.NodeID, old, first.OwnerEpoch, deployment.NodeHealth{}, []sandbox.GenerationStatus{{Generation: second.Generation, SpecificationDigest: second.SpecificationDigest, State: "ready"}}); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("old connection qualified", err)
	}
	nodes, err = service.ListNodes(t.Context())
	if err != nil || nodes[0].ProviderReady || *nodes[0].Rollout.ReadyGeneration != 1 {
		t.Fatal("reconnect inherited readiness or lost pin", nodes, err)
	}
	if _, err = s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("unconfirmed connection admitted", err)
	}
	if err = service.Heartbeat(t.Context(), node.NodeID, connection, first.OwnerEpoch, deployment.NodeHealth{ProviderReady: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal("fresh v1 readiness did not restore fallback", err)
	}
}

func TestNodeGenerationPreparationRefusalCreatesNoProvisionalOwnership(t *testing.T) {
	s, _, first, _ := webSpecificationFixture(t, "docker")
	node := specificationNode(t, s, first)
	connection := uuid.NewString()
	if err := deploymentService(t, s).ConnectNode(t.Context(), node.NodeID, connection, first.OwnerEpoch); err != nil {
		t.Fatal(err)
	}
	generationHeartbeat(t, s, node, connection, first, "preparing")
	tenant := uuid.NewString()
	if _, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString())); !errors.Is(err, placement.ErrNodesPreparing) {
		t.Fatal("actual preparation was not identified", err)
	}
	var sessions, placements int
	if err := s.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM sessions WHERE tenant_id=$1),(SELECT count(*) FROM runtime_placements)", tenant).Scan(&sessions, &placements); err != nil || sessions != 0 || placements != 0 {
		t.Fatal("refusal left provisional ownership", sessions, placements, err)
	}
	generationHeartbeat(t, s, node, connection, first, "failed")
	if _, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString())); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("failed preparation advertised active work", err)
	}
}
