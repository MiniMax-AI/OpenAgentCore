package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/google/uuid"
)

func webSpecificationFixture(t *testing.T, provider string) (*Store, *Store, RuntimeDeploymentView, SandboxDeploymentSetupRequest) {
	t.Helper()
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{13}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := SandboxDeploymentSetupRequest{Provider: provider, DeploymentSpec: SandboxDeploymentTestSpec(provider)}
	if provider == "e2b" {
		input = e2bSelection()
	}
	view, err := w.InitializeSandboxDeployment(t.Context(), id, input)
	if err != nil {
		t.Fatal(err)
	}
	return s, w, view, input
}

func specificationNode(t *testing.T, s *Store, view RuntimeDeploymentView) RuntimeNodeEnrollment {
	t.Helper()
	token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 4, MaxRetained: 16}))
	if err != nil {
		t.Fatal(err)
	}
	input := RuntimeNodeEnrollment{NodeID: uuid.NewString(), Name: "specification fixture", Credential: strings.Repeat("n", 64), Provider: view.Provider,
		BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest, CoreURL: s.publicURL}
	if _, err := s.EnrollRuntimeNode(t.Context(), token, input); err != nil {
		t.Fatal(err)
	}
	onlineManagerNode(t, s, input.NodeID)
	return input
}

func TestSandboxSpecificationRoundTripAndFileConfigurationCannotOverride(t *testing.T) {
	for _, provider := range []string{"docker", "microsandbox", "e2b"} {
		t.Run(provider, func(t *testing.T) {
			s, w, view, input := webSpecificationFixture(t, provider)
			setup, err := s.GetSandboxSetup(t.Context())
			if err != nil || !reflect.DeepEqual(setup.Specification, input.DeploymentSpec) || view.Specification == nil || !reflect.DeepEqual(*view.Specification, input.DeploymentSpec) || view.SpecificationDigest != input.DeploymentSpec.Digest(provider) {
				t.Fatal("saved deployment lost its resources or Runtime provenance", err)
			}
			preview, err := SandboxSetupForSelection(view.InstallationID, input)
			if err != nil || preview.Mode != setup.Mode || preview.BackendFingerprint != setup.BackendFingerprint || preview.IdleSeconds != setup.IdleSeconds || preview.RetentionSeconds != setup.RetentionSeconds || !reflect.DeepEqual(preview.Configuration, setup.Configuration) {
				t.Fatal("preview and persisted normalized deployment disagree", err)
			}
			input.ExpectedGeneration = view.Generation
			retry, err := w.InitializeSandboxDeployment(t.Context(), view.InstallationID, input)
			if err != nil || !reflect.DeepEqual(retry, view) {
				t.Fatal("identical specification changed the generation", err)
			}
			changed := input
			changed.Resources.CPUs++
			if _, err := w.InitializeSandboxDeployment(t.Context(), view.InstallationID, changed); !errors.Is(err, ErrSandboxDeploymentConflict) {
				t.Fatal("initial setup silently resized a configured deployment", err)
			}
			file := RuntimeDeployment{InstallationID: view.InstallationID, BackendFingerprint: setup.BackendFingerprint, ProviderKind: provider, AdmissionPaused: true}
			for _, candidate := range []*RuntimeDeployment{nil, &file} {
				if err := w.ConfigureRuntimeDeployment(t.Context(), candidate); !errors.Is(err, ErrSandboxDeploymentConflict) {
					t.Fatal("file configuration replaced database ownership", err)
				}
			}
			after, err := s.GetRuntimeDeployment(t.Context())
			if err != nil || !reflect.DeepEqual(after, view) {
				t.Fatal("rejected writes changed the committed specification", err)
			}
		})
	}
}

func TestSandboxSpecificationBootstrapReadDoesNotConsumeEnrollment(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "docker")
	token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 4}))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		config, err := s.RuntimeNodeConfiguration(t.Context(), "", token)
		if err != nil || config.Generation != view.Generation || config.InstallationID != view.InstallationID || !reflect.DeepEqual(config.Specification, input.DeploymentSpec) || config.SpecificationDigest != view.SpecificationDigest {
			t.Fatal("bootstrap did not return the saved configuration", err)
		}
	}
	if _, err := s.RuntimeNodeConfiguration(t.Context(), "", "invalid-token"); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("unauthenticated configuration read", err)
	}
	node := RuntimeNodeEnrollment{NodeID: uuid.NewString(), Name: "bootstrap", Credential: strings.Repeat("n", 64), Provider: "docker", BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest}
	for _, change := range []func(*RuntimeNodeEnrollment){
		func(n *RuntimeNodeEnrollment) { n.DeploymentGeneration++ },
		func(n *RuntimeNodeEnrollment) { n.SpecificationDigest = strings.Repeat("c", 64) },
	} {
		wrong := node
		change(&wrong)
		if _, err := s.EnrollRuntimeNode(t.Context(), token, wrong); !errors.Is(err, ErrRuntimeSpecificationMismatch) {
			t.Fatal("mismatched node configuration enrolled", err)
		}
	}
	if _, err := s.EnrollRuntimeNode(t.Context(), token, node); err != nil {
		t.Fatal("read or mismatch consumed the enrollment", err)
	}
	if _, err := s.RuntimeNodeConfiguration(t.Context(), "", token); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("consumed enrollment still authorized bootstrap", err)
	}
	if _, err := w.StartSandboxReset(SandboxResetTestContext(t.Context()), view.InstallationID, SandboxResetRequest{Clear: "auto", ExpectedGeneration: view.Generation}); err != nil {
		t.Fatal(err)
	}
	config, err := s.RuntimeNodeConfiguration(t.Context(), node.NodeID, node.Credential)
	if err != nil || config.SpecificationDigest != view.SpecificationDigest {
		t.Fatal("retained node identity could not recover configuration in maintenance", err)
	}
	if _, err := s.RuntimeNodeConfiguration(t.Context(), node.NodeID, "invalid-credential"); !errors.Is(err, ErrRuntimeNodeCredential) {
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
		if _, err := s.AuthenticateRuntimeNode(t.Context(), node.NodeID, node.Credential); !errors.Is(err, ErrRuntimeSpecificationMismatch) {
			t.Fatal("stale persisted node authenticated", field, err)
		}
		if _, err := s.RuntimeNodeConfiguration(t.Context(), node.NodeID, node.Credential); !errors.Is(err, ErrRuntimeSpecificationMismatch) {
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
			node := specificationNode(t, s, view)
			tenant := uuid.NewString()
			session, err := createSessionOnNode(t, s, tenant, managerSessionInput(uuid.NewString()), node.NodeID)
			if err != nil {
				t.Fatal(err)
			}
			var owner RuntimeAllocation
			if state != "pending" {
				owner, err = w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, view.InstallationID, device.HashCredential(uuid.NewString()))
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
			before, err := s.GetRuntimeDeployment(t.Context())
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
				update := SandboxDeploymentUpdateRequest{ExpectedGeneration: view.Generation, SandboxDeploymentSetupRequest: changed}
				if err := w.CheckSandboxDeploymentSwitch(t.Context(), view.InstallationID, update); err != nil {
					t.Fatal(field, err)
				}
				next, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), view.InstallationID, update)
				if err != nil || next.Generation != view.Generation+1 || next.OwnerEpoch != view.OwnerEpoch {
					t.Fatal(field, next, err)
				}
				view = next
			}
			after, err := s.GetRuntimeDeployment(t.Context())
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
			committed, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), view.InstallationID, SandboxDeploymentUpdateRequest{ExpectedGeneration: view.Generation, SandboxDeploymentSetupRequest: changed})
			if err != nil || committed.Generation != view.Generation+1 || committed.Reset != nil || committed.Resources != (SandboxDeploymentResources{}) || committed.Specification == nil || !reflect.DeepEqual(*committed.Specification, changed.DeploymentSpec) {
				t.Fatal("completed cleanup did not permit the replacement", err)
			}
			if _, err := s.AuthenticateRuntimeNode(t.Context(), node.NodeID, node.Credential); err != nil {
				t.Fatal("online change retired serving identity", err)
			}
		})
	}
}

func TestSandboxSpecificationInitialCredentialRemainsPrivate(t *testing.T) {
	s, _, view, input := webSpecificationFixture(t, "e2b")
	raw, err := json.Marshal(view)
	if err != nil || bytes.Contains(raw, []byte(input.Configuration.(*e2b.DeploymentConfiguration).APIKey)) || bytes.Contains(raw, []byte("api_key")) {
		t.Fatal("public deployment serialized a private credential", err)
	}
	var stored []byte
	if err := s.pool.QueryRow(t.Context(), "SELECT provider_credential FROM runtime_deployment").Scan(&stored); err != nil || len(stored) == 0 || bytes.Contains(stored, []byte(input.Configuration.(*e2b.DeploymentConfiguration).APIKey)) {
		t.Fatal("private credential was not encrypted", err)
	}
	// The credential is rejected before the cloud deployment mode is reported.
	if _, err := s.RuntimeNodeConfiguration(t.Context(), "", input.Configuration.(*e2b.DeploymentConfiguration).APIKey); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("cloud key authorized node bootstrap", err)
	}
}

func TestSandboxSpecificationAllocationRaceWithMaintenance(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "e2b")
	tenant := uuid.NewString()
	var sessions []Session
	for range 12 {
		session, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	type result struct {
		session Session
		owner   RuntimeAllocation
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, len(sessions))
	maintenance := make(chan error, 1)
	for _, session := range sessions {
		go func() {
			<-start
			owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, view.InstallationID, device.HashCredential(uuid.NewString()))
			results <- result{session, owner, err}
		}()
	}
	go func() {
		<-start
		_, err := w.StartSandboxReset(SandboxResetTestContext(t.Context()), view.InstallationID, SandboxResetRequest{Clear: "auto", ExpectedGeneration: view.Generation})
		maintenance <- err
	}()
	close(start)
	if err := <-maintenance; err != nil {
		t.Fatal(err)
	}
	var allocated int64
	for range sessions {
		result := <-results
		if result.err == nil {
			allocated++
			retry, err := w.ReserveRuntimeAllocation(t.Context(), tenant, result.session.Environment.ID, view.InstallationID, device.HashCredential(uuid.NewString()))
			if err != nil || retry.ID != result.owner.ID || !retry.Replayed {
				t.Fatal("maintenance changed an admitted allocation retry", err)
			}
		} else {
			if !errors.Is(result.err, ErrSandboxResetAdmission) {
				t.Fatal("allocation race failed outside admission", result.err)
			}
			if _, err := w.ReserveRuntimeAllocation(t.Context(), tenant, result.session.Environment.ID, view.InstallationID, device.HashCredential(uuid.NewString())); !errors.Is(err, ErrSandboxResetAdmission) {
				t.Fatal("fresh allocation passed committed maintenance", err)
			}
		}
	}
	after, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || after.Reset == nil || after.Generation != view.Generation || after.Resources.Allocations != allocated || after.Resources.Pending != int64(len(sessions))-allocated {
		t.Fatal("concurrent maintenance lost resource accounting", after.Resources, err)
	}
	input.Resources.CPUs++
	if _, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), view.InstallationID, SandboxDeploymentUpdateRequest{ExpectedGeneration: view.Generation, SandboxDeploymentSetupRequest: input}); !errors.Is(err, ErrSandboxResetInProgress) {
		t.Fatal("allocation race bypassed replacement guard", err)
	}
}

// A node keeps the public URL it enrolled with. After the public URL changes it
// receives no new sandboxes until it is re-added.
func TestNodeBoundToAnotherPublicURLGetsNoNewSandboxes(t *testing.T) {
	s, _, view, _ := webSpecificationFixture(t, "docker")
	s.SetPublicURL("https://old.example")
	node := specificationNode(t, s, view)
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].ID != node.NodeID || nodes[0].CoreURL != "https://old.example" {
		t.Fatal("enrollment did not record the node's address", nodes, err)
	}
	s.SetPublicURL("https://new.example")
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, ErrRuntimeNodeUnavailable) {
		t.Fatal("placed a new sandbox on a node bound to the old address", err)
	}
	bindings, err := s.AddressBindings(t.Context())
	if err != nil || bindings.Nodes != 1 || bindings.NodesOnOtherAddress != 1 || bindings.HostedSandboxes != 0 {
		t.Fatal(bindings, err)
	}
	s.SetPublicURL("https://old.example")
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal("node on the current address rejected placement", err)
	}
}

// Enrollment records the command's public ID and the node's Core address. A node
// using another address is refused without consuming the token, and a node
// enrolled with a token issued before Core recorded IDs reports none.
func TestEnrollmentRecordsItsIDAndRefusesAnotherAddress(t *testing.T) {
	s, _, view, _ := webSpecificationFixture(t, "docker")
	s.SetPublicURL("https://core.example")
	issued, err := s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 2})
	if err != nil || uuid.Validate(issued.ID) != nil || issued.Token == "" {
		t.Fatal(issued.ID, err)
	}
	input := RuntimeNodeEnrollment{NodeID: uuid.NewString(), Name: "addressed", Credential: strings.Repeat("a", 64), Provider: "docker",
		BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest, CoreURL: "https://other.example"}
	if _, err := s.EnrollRuntimeNode(t.Context(), issued.Token, input); !errors.Is(err, ErrRuntimeNodeAddressMismatch) {
		t.Fatal("enrolled a node that uses another Core address", err)
	}
	input.CoreURL = "https://core.example"
	if _, err := s.EnrollRuntimeNode(t.Context(), issued.Token, input); err != nil {
		t.Fatal("the refused enrollment consumed its token", err)
	}
	earlier := strings.Repeat("e", 64)
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO runtime_node_enrollments(token_sha256,installation_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '10 minutes')",
		runtimeTokenDigest(earlier), view.InstallationID); err != nil {
		t.Fatal(err)
	}
	older := input
	older.NodeID, older.Credential = uuid.NewString(), strings.Repeat("o", 64)
	if _, err := s.EnrollRuntimeNode(t.Context(), earlier, older); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || len(nodes) != 2 {
		t.Fatal(nodes, err)
	}
	for _, node := range nodes {
		switch node.ID {
		case input.NodeID:
			if node.EnrollmentID == nil || *node.EnrollmentID != issued.ID || node.CoreURL != "https://core.example" {
				t.Fatal("the node did not record its enrollment", node.EnrollmentID, node.CoreURL)
			}
		case older.NodeID:
			if node.EnrollmentID != nil {
				t.Fatal("a token without an ID reported one", *node.EnrollmentID)
			}
		}
	}
}

// An E2B selection saved before the public URL became loopback admits no new
// Session, and its configuration stays readable for cleanup.
func TestE2BAdmitsNothingWhileThePublicURLIsLoopback(t *testing.T) {
	s, _, _, _ := webSpecificationFixture(t, "e2b")
	s.SetPublicURL("http://127.0.0.1:8091")
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, ErrSandboxPublicURLUnreachable) {
		t.Fatal("admitted an E2B Session that could not reach Core", err)
	}
	if setup, err := s.GetSandboxSetup(t.Context()); err != nil || setup.Provider != "e2b" {
		t.Fatal("the saved E2B selection became unreadable", err)
	}
	s.SetPublicURL("https://core.example")
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseDoesNotEnumerateProviderRegistrations(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "docker")
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), "UPDATE runtime_deployment SET provider_kind='new-adapter' WHERE singleton=true"); err != nil {
		t.Fatal("database enumerated provider implementations", err)
	}
	// Roll back before calling the serialized Store, which still rejects unknown
	// registrations even though persistence can represent a new adapter.
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	input.Provider = "new-adapter"
	input.ExpectedGeneration = view.Generation
	if _, err = w.InitializeSandboxDeployment(t.Context(), view.InstallationID, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unknown adapter reached persistence", err)
	}
}
