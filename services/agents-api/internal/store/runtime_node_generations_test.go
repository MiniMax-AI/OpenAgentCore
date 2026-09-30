package store

import (
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

func changeNodeTarget(t *testing.T, w *Store, view RuntimeDeploymentView, input SandboxDeploymentSetupRequest) (RuntimeDeploymentView, SandboxDeploymentSetupRequest) {
	t.Helper()
	input.Resources.CPUs++
	next, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: view.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation != view.Generation+1 || next.OwnerEpoch != view.OwnerEpoch {
		t.Fatal("target change replaced execution ownership", next)
	}
	return next, input
}

func generationHeartbeat(t *testing.T, s *Store, node RuntimeNodeEnrollment, connection string, view RuntimeDeploymentView, state string) {
	t.Helper()
	err := s.HeartbeatRuntimeNodeGenerations(t.Context(), node.NodeID, connection, view.OwnerEpoch, RuntimeNodeHealth{}, []sandbox.GenerationStatus{{Generation: view.Generation, SpecificationDigest: view.SpecificationDigest, State: state}})
	if err != nil {
		t.Fatal(err)
	}
}

func placedGeneration(t *testing.T, s *Store, session Session) (string, int64) {
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
			token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 1, MaxRetained: 2}))
			if err != nil {
				t.Fatal(err)
			}
			second, input := changeNodeTarget(t, w, first, input)
			if _, err = s.RuntimeNodeConfiguration(t.Context(), "", token); err != nil {
				t.Fatal("update retired enrollment", err)
			}
			if _, err = s.AuthenticateRuntimeNode(t.Context(), a.NodeID, a.Credential); err != nil {
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
			nodes, err := s.ListRuntimeNodes(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				want := uint64(1)
				if n.ID == a.NodeID {
					want = 2
				}
				if n.Rollout.ReadyGeneration == nil || *n.Rollout.ReadyGeneration != want || !n.ProviderReady {
					t.Fatal("late readiness moved pin or erased serving readiness", n)
				}
			}
			owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, pending.Environment.ID, first.InstallationID, device.HashCredential("runtime"))
			if err != nil {
				t.Fatal(err)
			}
			if owner.NodeID != pendingNode || owner.DeploymentGeneration != uint64(pendingGeneration) {
				t.Fatal("pending placement moved", owner)
			}
			n, g, err := s.ResolveRuntimeGeneration(t.Context(), sandbox.Reference{TenantID: tenant, EnvironmentID: owner.EnvironmentID, AllocationID: owner.ID})
			if err != nil || n != pendingNode || g != 1 {
				t.Fatal(n, g, err)
			}
			for _, v := range []RuntimeDeploymentView{first, second, third} {
				target := a
				if v.Generation == 1 {
					target = b
				}
				got, err := s.RuntimeNodeGenerationConfiguration(t.Context(), target.NodeID, target.Credential, v.Generation)
				if err != nil || got.SpecificationDigest != v.SpecificationDigest {
					t.Fatal("kept generation unrecoverable", v.Generation, err)
				}
			}
		})
	}
}

func TestNodeGenerationsReconnectAndV1Fallback(t *testing.T) {
	s, w, first, input := webSpecificationFixture(t, "docker")
	node := specificationNode(t, s, first)
	old := onlineManagerNode(t, s, node.NodeID)
	second, _ := changeNodeTarget(t, w, first, input)
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || !nodes[0].ProviderReady || nodes[0].Rollout.State != "update_required" {
		t.Fatal(nodes, err)
	}
	if _, err = s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal("v1 lost fallback", err)
	}
	connection := uuid.NewString()
	if err = s.ConnectRuntimeNode(t.Context(), node.NodeID, connection, first.OwnerEpoch); err != nil {
		t.Fatal(err)
	}
	if err = s.HeartbeatRuntimeNodeGenerations(t.Context(), node.NodeID, old, first.OwnerEpoch, RuntimeNodeHealth{}, []sandbox.GenerationStatus{{Generation: second.Generation, SpecificationDigest: second.SpecificationDigest, State: "ready"}}); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("old connection qualified", err)
	}
	nodes, err = s.ListRuntimeNodes(t.Context())
	if err != nil || nodes[0].ProviderReady || *nodes[0].Rollout.ReadyGeneration != 1 {
		t.Fatal("reconnect inherited readiness or lost pin", nodes, err)
	}
	if _, err = s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, ErrRuntimeNodeUnavailable) {
		t.Fatal("unconfirmed connection admitted", err)
	}
	if err = s.HeartbeatRuntimeNode(t.Context(), node.NodeID, connection, first.OwnerEpoch, RuntimeNodeHealth{ProviderReady: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal("fresh v1 readiness did not restore fallback", err)
	}
}

func TestNodeGenerationDowngradePreservesServingProtocol(t *testing.T) {
	for _, mode := range []string{"v2", "old_v1", "current_v1"} {
		t.Run(mode, func(t *testing.T) {
			s, w, first, input := webSpecificationFixture(t, "docker")
			node := specificationNode(t, s, first)
			switch mode {
			case "v2":
				generationHeartbeat(t, s, node, onlineManagerNode(t, s, node.NodeID), first, "ready")
			case "old_v1":
				changeNodeTarget(t, w, first, input)
			}
			db := sql.OpenDB(stdlib.GetConnector(*s.pool.Config().ConnConfig))
			defer db.Close()
			migration, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = migration.DownTo(t.Context(), 81)
			if mode == "current_v1" {
				if err != nil {
					t.Fatal("safe v1 downgrade refused", err)
				}
			} else {
				if err == nil {
					t.Fatal("downgrade discarded required node protocol")
				}
				// DownTo may have removed later, reversible migrations before the
				// node protocol migration refused the downgrade. Restore the current
				// schema before using this version of the Store to verify recovery.
				if _, err = migration.Up(t.Context()); err != nil {
					t.Fatal("refused downgrade could not restore current schema", err)
				}
				if _, err = s.RuntimeNodeGenerationConfiguration(t.Context(), node.NodeID, node.Credential, 1); err != nil {
					t.Fatal("refused downgrade damaged retained recovery", err)
				}
				if err = s.RemoveRuntimeNode(t.Context(), node.NodeID); err != nil {
					t.Fatal(err)
				}
				if _, err = migration.DownTo(t.Context(), 81); err != nil {
					t.Fatal("removed node blocked downgrade", err)
				}
			}
			if _, err = migration.Up(t.Context()); err != nil {
				t.Fatal("node schema could not upgrade again", err)
			}
		})
	}
}

func TestNodeGenerationPreparationRefusalCreatesNoProvisionalOwnership(t *testing.T) {
	s, _, first, _ := webSpecificationFixture(t, "docker")
	node := specificationNode(t, s, first)
	connection := uuid.NewString()
	if err := s.ConnectRuntimeNode(t.Context(), node.NodeID, connection, first.OwnerEpoch); err != nil {
		t.Fatal(err)
	}
	generationHeartbeat(t, s, node, connection, first, "preparing")
	tenant := uuid.NewString()
	if _, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString())); !errors.Is(err, ErrSandboxNodesPreparing) {
		t.Fatal("actual preparation was not identified", err)
	}
	var sessions, placements int
	if err := s.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM sessions WHERE tenant_id=$1),(SELECT count(*) FROM runtime_placements)", tenant).Scan(&sessions, &placements); err != nil || sessions != 0 || placements != 0 {
		t.Fatal("refusal left provisional ownership", sessions, placements, err)
	}
	generationHeartbeat(t, s, node, connection, first, "failed")
	if _, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString())); !errors.Is(err, ErrRuntimeNodeUnavailable) {
		t.Fatal("failed preparation advertised active work", err)
	}
}
