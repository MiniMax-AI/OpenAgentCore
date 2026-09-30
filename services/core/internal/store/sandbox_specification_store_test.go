package store

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func webSpecificationFixture(t *testing.T, provider string) (*Store, *Store, deployment.View, sandbox.Selection) {
	t.Helper()
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{13}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	w := executionWriter(t, s)
	id := uuid.NewString()
	changes := deploymentExecution(t, w)
	if err := changes.Claim(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := sandbox.Selection{Provider: provider, DeploymentSpec: SandboxDeploymentTestSpec(provider)}
	if provider == "e2b" {
		input = e2bSelection()
	}
	view, err := changes.Initialize(t.Context(), id, input)
	if err != nil {
		t.Fatal(err)
	}
	return s, w, view, input
}

func specificationNode(t *testing.T, s *Store, view deployment.View) deployment.Enrollment {
	t.Helper()
	nodes := deploymentService(t, s)
	token, err := EnrollmentTestToken(nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 4, MaxRetained: 16}))
	if err != nil {
		t.Fatal(err)
	}
	input := deployment.Enrollment{NodeID: uuid.NewString(), Name: "specification fixture", Credential: strings.Repeat("n", 64), Provider: view.Provider,
		BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest, CoreURL: s.publicURL}
	if _, err := nodes.Enroll(t.Context(), token, input); err != nil {
		t.Fatal(err)
	}
	onlineManagerNode(t, s, input.NodeID)
	return input
}

func TestSandboxSpecificationBootstrapReadDoesNotConsumeEnrollment(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "docker")
	nodes := deploymentService(t, s)
	token, err := EnrollmentTestToken(nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 2, MaxRetained: 4}))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		config, err := nodes.NodeConfiguration(t.Context(), "", token, 0)
		if err != nil || config.Generation != view.Generation || config.InstallationID != view.InstallationID || !reflect.DeepEqual(config.Specification, input.DeploymentSpec) || config.SpecificationDigest != view.SpecificationDigest {
			t.Fatal("bootstrap did not return the saved configuration", err)
		}
	}
	if _, err := nodes.NodeConfiguration(t.Context(), "", "invalid-token", 0); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("unauthenticated configuration read", err)
	}
	node := deployment.Enrollment{NodeID: uuid.NewString(), Name: "bootstrap", Credential: strings.Repeat("n", 64), Provider: "docker", BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest}
	for _, change := range []func(*deployment.Enrollment){
		func(n *deployment.Enrollment) { n.DeploymentGeneration++ },
		func(n *deployment.Enrollment) { n.SpecificationDigest = strings.Repeat("c", 64) },
	} {
		wrong := node
		change(&wrong)
		if _, err := nodes.Enroll(t.Context(), token, wrong); !errors.Is(err, deployment.ErrSpecificationMismatch) {
			t.Fatal("mismatched node configuration enrolled", err)
		}
	}
	if _, err := nodes.Enroll(t.Context(), token, node); err != nil {
		t.Fatal("read or mismatch consumed the enrollment", err)
	}
	if _, err := nodes.NodeConfiguration(t.Context(), "", token, 0); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("consumed enrollment still authorized bootstrap", err)
	}
	if err := deploymentExecution(t, w).StartReset(SandboxResetTestContext(t.Context()), view.InstallationID, deployment.ResetRequest{Clear: "auto", ExpectedGeneration: view.Generation}); err != nil {
		t.Fatal(err)
	}
	config, err := nodes.NodeConfiguration(t.Context(), node.NodeID, node.Credential, 0)
	if err != nil || config.SpecificationDigest != view.SpecificationDigest {
		t.Fatal("retained node identity could not recover configuration in maintenance", err)
	}
	if _, err := nodes.NodeConfiguration(t.Context(), node.NodeID, "invalid-credential", 0); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("retained identity bypassed credential validation", err)
	}
	for _, field := range []string{"deployment_generation", "specification_digest"} {
		query, value := "UPDATE runtime_nodes SET deployment_generation=deployment_generation+1 WHERE id=$1", any(nil)
		if field == "specification_digest" {
			query, value = "UPDATE runtime_nodes SET specification_digest=$2 WHERE id=$1", strings.Repeat("d", 64)
		}
		args := []any{node.NodeID}
		if value != nil {
			args = append(args, value)
		}
		if _, err := s.pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
		if _, err := nodes.AuthenticateNode(t.Context(), node.NodeID, node.Credential); !errors.Is(err, deployment.ErrSpecificationMismatch) {
			t.Fatal("stale persisted node authenticated", field, err)
		}
		if _, err := nodes.NodeConfiguration(t.Context(), node.NodeID, node.Credential, 0); !errors.Is(err, deployment.ErrSpecificationMismatch) {
			t.Fatal("stale persisted node received configuration", field, err)
		}
		if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET deployment_generation=$2,specification_digest=$3 WHERE id=$1", node.NodeID, view.Generation, view.SpecificationDigest); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSandboxSpecificationChangesPreserveEveryRetainedResource(t *testing.T) {
	for _, state := range []string{"pending", "creating", "running", "stopped", "snapshot", "cleanup_pending"} {
		t.Run(state, func(t *testing.T) {
			s, w, view, input := webSpecificationFixture(t, "microsandbox")
			changes, deployments := deploymentExecution(t, w), deploymentService(t, s)
			node := specificationNode(t, s, view)
			tenant := uuid.NewString()
			session, err := createSessionOnNode(t, s, tenant, managerSessionInput(uuid.NewString()), node.NodeID)
			if err != nil {
				t.Fatal(err)
			}
			var owner RuntimeAllocation
			if state != "pending" {
				owner, err = w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, view.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
				if err != nil {
					t.Fatal(err)
				}
				if state != "creating" {
					owner, err = w.ObserveRuntimeRunning(t.Context(), owner)
					if err != nil {
						t.Fatal(err)
					}
				}
				switch state {
				case "stopped":
					// A stopped native instance retains its allocation; the Store only
					// records that running compute could not be confirmed.
					if err := w.RecordRuntimeObservation(t.Context(), owner, "compute_unconfirmed"); err != nil {
						t.Fatal(err)
					}
				case "snapshot":
					// Seed the provider's completed suspension receipt, as lifecycle
					// fixtures do; allocation ownership was admitted through the Store.
					if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET compute_phase='suspended',compute_state=$2::jsonb,compute_retained_until=clock_timestamp()+interval '1 day' WHERE id=$1", owner.ID, `{"snapshot":{"id":"retained-native-snapshot"}}`); err != nil {
						t.Fatal(err)
					}
				case "cleanup_pending":
					owner, err = w.RequestRuntimeCleanup(t.Context(), owner)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			before, err := deployments.View(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if before.Resources.Allocations+before.Resources.Pending != 1 {
				t.Fatal("resource fixture was not retained", before.Resources)
			}
			var allocationBefore []byte
			if owner.ID != "" {
				if err := s.pool.QueryRow(t.Context(), "SELECT to_jsonb(a) FROM runtime_allocations a WHERE id=$1", owner.ID).Scan(&allocationBefore); err != nil {
					t.Fatal(err)
				}
			}
			for _, field := range []string{"resources", "runtime"} {
				changed := input
				if field == "resources" {
					changed.Resources.MemoryMiB *= 2
				} else {
					runtime := *input.Runtime
					runtime.SourceCommit = strings.Repeat("1", 40)
					changed.Runtime = &runtime
				}
				changed.ExpectedGeneration = view.Generation
				if _, _, err := changes.ClassifyChange(t.Context(), view.InstallationID, changed); err != nil {
					t.Fatal(field, err)
				}
				next, err := changes.Update(SandboxResetTestContext(t.Context()), view.InstallationID, changed)
				if err != nil || next.Generation != view.Generation+1 || next.OwnerEpoch != view.OwnerEpoch {
					t.Fatal(field, next, err)
				}
				view = next
			}
			after, err := deployments.View(t.Context())
			if err != nil || before.Resources != after.Resources {
				t.Fatal("online specification change altered ownership", err)
			}
			if owner.ID != "" {
				var allocationAfter []byte
				if err := s.pool.QueryRow(t.Context(), "SELECT to_jsonb(a) FROM runtime_allocations a WHERE id=$1", owner.ID).Scan(&allocationAfter); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(allocationBefore, allocationAfter) {
					t.Fatal("online change mutated or deleted retained ownership")
				}
				owner, err = w.RequestRuntimeCleanup(t.Context(), owner)
				if err != nil {
					t.Fatal(err)
				}
				owner, err = w.SettleRuntimeCreation(t.Context(), owner)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.ReleaseRuntimeAllocation(t.Context(), owner); err != nil {
					t.Fatal(err)
				}
			} else if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
				t.Fatal(err)
			}
			changed := input
			changed.Resources.MemoryMiB *= 2
			runtime := *input.Runtime
			runtime.SourceCommit = strings.Repeat("2", 40)
			changed.Runtime = &runtime
			changed.ExpectedGeneration = view.Generation
			committed, err := changes.Update(SandboxResetTestContext(t.Context()), view.InstallationID, changed)
			if err != nil || committed.Generation != view.Generation+1 || committed.Reset != nil || committed.Resources != (deployment.Resources{}) || committed.Specification == nil || !reflect.DeepEqual(*committed.Specification, changed.DeploymentSpec) {
				t.Fatal("completed cleanup did not permit the replacement", err)
			}
			if _, err := deployments.AuthenticateNode(t.Context(), node.NodeID, node.Credential); err != nil {
				t.Fatal("online change retired serving identity", err)
			}
		})
	}
}

func TestSandboxSpecificationAllocationRaceWithMaintenance(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "e2b")
	tenant := uuid.NewString()
	var created []sessions.Session
	for range 12 {
		session, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, session)
	}
	type result struct {
		session sessions.Session
		owner   RuntimeAllocation
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, len(created))
	maintenance := make(chan error, 1)
	for _, session := range created {
		go func() {
			<-start
			owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, view.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
			results <- result{session, owner, err}
		}()
	}
	resets := deploymentExecution(t, w)
	go func() {
		<-start
		maintenance <- resets.StartReset(SandboxResetTestContext(t.Context()), view.InstallationID, deployment.ResetRequest{Clear: "auto", ExpectedGeneration: view.Generation})
	}()
	close(start)
	if err := <-maintenance; err != nil {
		t.Fatal(err)
	}
	var allocated int64
	for range created {
		result := <-results
		if result.err == nil {
			allocated++
			retry, err := w.ReserveRuntimeAllocation(t.Context(), tenant, result.session.Environment.ID, view.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
			if err != nil || retry.ID != result.owner.ID || !retry.Replayed {
				t.Fatal("maintenance changed an admitted allocation retry", err)
			}
		} else {
			if !errors.Is(result.err, ErrSandboxResetAdmission) {
				t.Fatal("allocation race failed outside admission", result.err)
			}
			if _, err := w.ReserveRuntimeAllocation(t.Context(), tenant, result.session.Environment.ID, view.InstallationID, runtimedevice.HashCredential(uuid.NewString())); !errors.Is(err, ErrSandboxResetAdmission) {
				t.Fatal("fresh allocation passed committed maintenance", err)
			}
		}
	}
	after, err := deploymentService(t, s).View(t.Context())
	if err != nil || after.Reset == nil || after.Generation != view.Generation || after.Resources.Allocations != allocated || after.Resources.Pending != int64(len(created))-allocated {
		t.Fatal("concurrent maintenance lost resource accounting", after.Resources, err)
	}
	input.Resources.CPUs++
	input.ExpectedGeneration = view.Generation
	if _, err := deploymentExecution(t, w).Update(SandboxResetTestContext(t.Context()), view.InstallationID, input); !errors.Is(err, deployment.ErrResetInProgress) {
		t.Fatal("allocation race bypassed replacement guard", err)
	}
}

// A node keeps the public URL it enrolled with. After the public URL changes it
// receives no new sandboxes until it is re-added.
func TestNodeBoundToAnotherPublicURLGetsNoNewSandboxes(t *testing.T) {
	s, _, view, _ := webSpecificationFixture(t, "docker")
	s.SetPublicURL("https://old.example")
	node := specificationNode(t, s, view)
	nodes, err := deploymentService(t, s).ListNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].ID != node.NodeID || nodes[0].CoreURL != "https://old.example" {
		t.Fatal("enrollment did not record the node's address", nodes, err)
	}
	s.SetPublicURL("https://new.example")
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, deployment.ErrNodeUnavailable) {
		t.Fatal("placed a new sandbox on a node bound to the old address", err)
	}
	bindings, err := deploymentService(t, s).AddressBindings(t.Context())
	if err != nil || bindings.Nodes != 1 || bindings.NodesOnOtherAddress != 1 || bindings.HostedSandboxes != 0 {
		t.Fatal(bindings, err)
	}
	s.SetPublicURL("https://old.example")
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal("node on the current address rejected placement", err)
	}
}

// An E2B selection saved before the public URL became loopback admits no new
// Session, and its configuration stays readable for cleanup.
func TestE2BAdmitsNothingWhileThePublicURLIsLoopback(t *testing.T) {
	s, _, _, _ := webSpecificationFixture(t, "e2b")
	s.SetPublicURL("http://127.0.0.1:8091")
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, deployment.ErrPublicURLUnreachable) {
		t.Fatal("admitted an E2B Session that could not reach Core", err)
	}
	if setup, err := deploymentService(t, s).Setup(t.Context()); err != nil || setup.Provider != "e2b" {
		t.Fatal("the saved E2B selection became unreadable", err)
	}
	s.SetPublicURL("https://core.example")
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal(err)
	}
}
