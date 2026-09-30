package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func e2bSelection() SandboxDeploymentSetupRequest {
	return SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("e2b"), Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "fixture-private-api-key", Template: "runtime:" + uuid.NewString()}}
}

func TestSandboxE2BEndpointPersistenceAndOnlineSwitch(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := e2bSelection()
	input.Configuration.(*e2b.DeploymentConfiguration).APIURL, input.Configuration.(*e2b.DeploymentConfiguration).Domain = "https://sandbox-test.sandbase.ai", "sandbox-test.sandbase.ai"
	view, err := w.InitializeSandboxDeployment(t.Context(), id, input)
	if err != nil || view.Configuration == nil || e2bPublicConfiguration(t, view).APIURL != input.Configuration.(*e2b.DeploymentConfiguration).APIURL || e2bPublicConfiguration(t, view).Domain != input.Configuration.(*e2b.DeploymentConfiguration).Domain {
		t.Fatal("custom endpoint was not returned", view, err)
	}
	setup, err := s.GetSandboxSetup(t.Context())
	if err != nil || setup.Configuration == nil || setup.Configuration.(*e2b.DeploymentConfiguration).APIURL != input.Configuration.(*e2b.DeploymentConfiguration).APIURL || setup.Configuration.(*e2b.DeploymentConfiguration).Domain != input.Configuration.(*e2b.DeploymentConfiguration).Domain {
		t.Fatal("custom endpoint was not persisted", setup, err)
	}
	change := e2bSelection()
	change.Configuration.(*e2b.DeploymentConfiguration).Template = input.Configuration.(*e2b.DeploymentConfiguration).Template
	update := SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: change, ExpectedGeneration: view.Generation}
	changed, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), id, update)
	if err != nil || changed.Generation != view.Generation+1 || changed.Configuration == nil || e2bPublicConfiguration(t, changed).APIURL != "https://api.e2b.app" || e2bPublicConfiguration(t, changed).Domain != "e2b.app" {
		t.Fatal("online endpoint switch failed", changed, err)
	}
}

func TestSandboxResetClearsCustomE2BEndpoint(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	installation := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	input := e2bSelection()
	input.Configuration.(*e2b.DeploymentConfiguration).APIURL, input.Configuration.(*e2b.DeploymentConfiguration).Domain = "https://sandbox-test.sandbase.ai", "sandbox-test.sandbase.ai"
	configured, err := w.InitializeSandboxDeployment(t.Context(), installation, input)
	if err != nil {
		t.Fatal(err)
	}
	ctx := SandboxResetTestContext(t.Context())
	reset, err := w.StartSandboxReset(ctx, installation, SandboxResetRequest{ExpectedGeneration: configured.Generation, Clear: "force"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := w.CompleteSandboxReset(ctx, installation, configured.Generation, reset.Reset.RequestedAt)
	if err != nil || empty.Provider != "" || empty.Reset != nil || empty.Generation != configured.Generation+1 {
		t.Fatal("custom endpoint blocked reset completion", empty, err)
	}
	var apiURL, domain string
	if err := pool.QueryRow(t.Context(), "SELECT COALESCE(provider_config->>'api_url',''), COALESCE(provider_config->>'domain','') FROM runtime_deployment").Scan(&apiURL, &domain); err != nil || apiURL != "" || domain != "" {
		t.Fatal("reset retained custom endpoint", apiURL, domain, err)
	}
}

func TestSandboxDirectDeploymentOwnershipAndCleanSwitch(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := e2bSelection()
	view, err := w.InitializeSandboxDeployment(t.Context(), id, input)
	if err != nil || view.Generation != 1 || view.Mode != "direct" || view.Configuration == nil || !view.CredentialConfigured {
		t.Fatal("direct setup", view, err)
	}
	raw, _ := json.Marshal(view)
	if bytes.Contains(raw, []byte(input.Configuration.(*e2b.DeploymentConfiguration).APIKey)) {
		t.Fatal("credential in public view")
	}
	var ciphertext []byte
	if err := pool.QueryRow(t.Context(), "SELECT provider_credential FROM runtime_deployment").Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte(input.Configuration.(*e2b.DeploymentConfiguration).APIKey)) {
		t.Fatal("credential not encrypted", err)
	}
	setup, err := s.GetSandboxSetup(t.Context())
	if err != nil || setup.Configuration.(*e2b.DeploymentConfiguration).APIKey != input.Configuration.(*e2b.DeploymentConfiguration).APIKey {
		t.Fatal("internal credential unavailable", err)
	}
	if _, err := s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 8}); !errors.Is(err, ErrSandboxDeploymentConflict) {
		t.Fatal("cloud enrolled a machine", err)
	}
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	environment := session.Environment.ID
	nodes, err := w.ListRuntimeLifecycleNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0] != "" {
		t.Fatal("cloud lifecycle requires node", nodes, err)
	}
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment, id, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil || owner.NodeID != "" {
		t.Fatal(owner, err)
	}
	credential, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID)
	if err != nil || !ok || credential.RuntimeAllocationID != owner.ID || credential.RuntimeNodeID != "" {
		t.Fatal("direct bootstrap lost managed identity", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE runtime_allocations SET kept_at=clock_timestamp()-interval '2 hours' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	observed, err := w.GetRuntimeAllocation(t.Context(), tenant, environment)
	if err != nil || observed.Expired {
		t.Fatal("cloud inherited legacy node-less expiry", err)
	}
	if _, err := w.StartSandboxReset(SandboxResetTestContext(t.Context()), id, SandboxResetRequest{Clear: "auto", ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	update := SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}, ExpectedGeneration: 1}
	if _, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), id, update); !errors.Is(err, ErrSandboxResetInProgress) {
		t.Fatal("reset allowed switch", err)
	}
	if _, err := w.RequestRuntimeCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReleaseRuntimeAllocation(t.Context(), owner); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("unsettled create released", err)
	}
	if _, err := w.SettleRuntimeCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReleaseRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	changed, err := resetAndSelect(t, w, id, update.ExpectedGeneration, update.SandboxDeploymentSetupRequest)
	if err != nil || changed.Generation != 3 || changed.Mode != "nodes" || changed.Reset != nil || string(changed.Configuration) != "{}" || changed.Resources != (SandboxDeploymentResources{}) {
		t.Fatal("clean switch", changed, err)
	}
	if _, err := w.CancelSandboxReset(SandboxResetTestContext(t.Context()), id, 1); !errors.Is(err, ErrSandboxDeploymentConflict) {
		t.Fatal("stale resume accepted", err)
	}
	if _, err := s.GetSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal("historical Session lost", err)
	}
}

func TestSandboxSwitchRetiresNodesAndEnrollment(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{5}, 32))
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.InitializeSandboxDeployment(t.Context(), id, SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err != nil {
		t.Fatal(err)
	}
	token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 4}))
	if err != nil {
		t.Fatal(err)
	}
	node := RuntimeNodeEnrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: uuid.NewString(), Name: "Machine", Provider: "docker", Credential: strings.Repeat("c", 64), BackendFingerprint: strings.Repeat("b", 64)}
	if _, err := s.EnrollRuntimeNode(t.Context(), token, node); err != nil {
		t.Fatal(err)
	}
	unused, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 8}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartSandboxReset(SandboxResetTestContext(t.Context()), id, SandboxResetRequest{Clear: "auto", ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	// AdmissionPaused rejects a valid enrollment without consuming it. Authentication
	// still precedes deployment details for invalid or retired credentials.
	spareNode := node
	spareNode.NodeID = uuid.NewString()
	if _, err := s.EnrollRuntimeNode(t.Context(), unused, spareNode); !errors.Is(err, ErrSandboxResetInProgress) {
		t.Fatal("reset accepted enrollment", err)
	}
	var consumed bool
	if err := pool.QueryRow(t.Context(), "SELECT consumed_at IS NOT NULL FROM runtime_node_enrollments WHERE token_sha256=$1", runtimeTokenDigest(unused)).Scan(&consumed); err != nil || consumed {
		t.Fatal("maintenance consumed enrollment", err)
	}
	spareNode.Provider = "microsandbox"
	if _, err := s.EnrollRuntimeNode(t.Context(), strings.Repeat("invalid", 8), spareNode); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("invalid token disclosed deployment validation", err)
	}
	spareNode.Provider = "docker"
	input := e2bSelection()
	if _, err := resetAndSelect(t, w, id, 1, input); err != nil {
		t.Fatal(err)
	}

	if _, err := s.EnrollRuntimeNode(t.Context(), unused, spareNode); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("retired token did not reject before cloud deployment validation", err)
	}
	if _, err := s.AuthenticateRuntimeNode(t.Context(), node.NodeID, node.Credential); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("old node credential survived", err)
	}
	if _, err := w.StartSandboxReset(SandboxResetTestContext(t.Context()), id, SandboxResetRequest{Clear: "auto", ExpectedGeneration: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := resetAndSelect(t, w, id, 3, SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.CancelSandboxReset(SandboxResetTestContext(t.Context()), id, 5); err != nil {
		t.Fatal(err)
	}
	node.NodeID = uuid.NewString()
	if _, err := s.EnrollRuntimeNode(t.Context(), unused, node); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("old enrollment survived roundtrip", err)
	}
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || len(nodes) != 0 {
		t.Fatal("retired nodes reappeared", err)
	}
}

func TestSandboxResetSerializesFreshDirectSessions(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{6}, 32))
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := e2bSelection()
	if _, err := w.InitializeSandboxDeployment(t.Context(), id, input); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString()))
			if err != nil && !errors.Is(err, ErrSandboxResetAdmission) && !errors.Is(err, ErrRuntimeNodeUnavailable) {
				t.Error(err)
			}
		}()
	}
	if _, err := w.StartSandboxReset(SandboxResetTestContext(t.Context()), id, SandboxResetRequest{Clear: "auto", ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for range 3 {
		if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, ErrSandboxResetAdmission) {
			t.Fatal("fresh creation bypassed reset", err)
		}
	}
	view, err := s.GetRuntimeDeployment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), id, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}, ExpectedGeneration: 1})
	if view.Resources.Pending > 0 && !errors.Is(err, ErrSandboxResetInProgress) {
		t.Fatal("committed pending Session bypassed switch guard", err)
	}
}

func TestSandboxSwitchPreservesReleasedAllocationAndItemHistory(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{8}, 32))
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	installation := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	selection := e2bSelection()
	if _, err := w.InitializeSandboxDeployment(t.Context(), installation, selection); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, installation, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReleaseAbsentRuntimeCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	// A separate completed Session supplies public Items without calling a model.
	history, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(t.Context(), tenant, history.ID, "history", json.RawMessage(`{"text":"retained request"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.TransitionTurn(t.Context(), tenant, history.ID, input.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendTurnEvents(t.Context(), tenant, history.ID, input.TurnID, 1, []ExecutionEvent{{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"completed","text":"retained answer"}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.TransitionTurn(t.Context(), tenant, history.ID, input.TurnID, TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnCompleted}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListItems(t.Context(), tenant, history.ID, "", 100, true)
	if err != nil || len(items.Items) != 2 {
		t.Fatal("history fixture", err)
	}
	before, _ := json.Marshal(items)
	var allocationBefore []byte
	if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(a) FROM runtime_allocations a WHERE id=$1", owner.ID).Scan(&allocationBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartSandboxReset(SandboxResetTestContext(t.Context()), installation, SandboxResetRequest{Clear: "auto", ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := resetAndSelect(t, w, installation, 1, SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListItems(t.Context(), tenant, history.ID, "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(items)
	if !bytes.Equal(before, after) {
		t.Fatal("switch rewrote public Item history")
	}
	var allocationAfter []byte
	if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(a) FROM runtime_allocations a WHERE id=$1", owner.ID).Scan(&allocationAfter); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(allocationBefore, allocationAfter) {
		t.Fatal("switch rewrote released allocation ownership")
	}
	for _, id := range []string{session.ID, history.ID} {
		if _, err := s.GetSession(t.Context(), tenant, id); err != nil {
			t.Fatal("switch lost undeleted Session", err)
		}
	}
}

// Migration 000069 leaves a node-backed selection saved before specifications
// with an empty specification and its nodes with empty digests. The retained
// node reconnects to drain resources; fresh admission and node configuration
// stay closed until an administrator replaces the selection.
func TestUnspecifiedNodeDeploymentRejectedWithoutMutation(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	spec := SandboxDeploymentTestSpec("docker")
	selection := SandboxDeploymentSetupRequest{DeploymentSpec: spec, Provider: "docker"}
	if _, err := w.InitializeSandboxDeployment(t.Context(), id, selection); err != nil {
		t.Fatal(err)
	}
	token, err := EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 4}))
	if err != nil {
		t.Fatal(err)
	}
	node := RuntimeNodeEnrollment{DeploymentGeneration: 1, SpecificationDigest: spec.Digest("docker"), NodeID: uuid.NewString(), Name: "Legacy", Provider: "docker", Credential: strings.Repeat("l", 64), BackendFingerprint: strings.Repeat("b", 64)}
	if _, err := s.EnrollRuntimeNode(t.Context(), token, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE runtime_deployment SET specification='{}'"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE runtime_nodes SET specification_digest='', deployment_generation=0"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetSandboxSetup(t.Context()); err == nil {
		t.Fatal("missing deployment specification accepted")
	}
	if _, err := s.AuthenticateRuntimeNode(t.Context(), node.NodeID, node.Credential); !errors.Is(err, ErrRuntimeSpecificationMismatch) {
		t.Fatal("unspecified node authenticated", err)
	}
	if _, err := s.RuntimeNodeConfiguration(t.Context(), node.NodeID, node.Credential); !errors.Is(err, ErrRuntimeSpecificationMismatch) {
		t.Fatal("node configuration served without a specification", err)
	}
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatal("unspecified deployment admitted a fresh sandbox", err)
	}
	if _, err := s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 4}); !errors.Is(err, ErrSandboxDeploymentConflict) {
		t.Fatal("unspecified deployment issued an enrollment token", err)
	}

	epoch := managerEpoch(t, s)
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err == nil {
		t.Fatal("unsupported installation claimed")
	}
	if managerEpoch(t, s) != epoch {
		t.Fatal("refused startup changed owner epoch")
	}
	var specification string
	if err := pool.QueryRow(t.Context(), "SELECT specification::text FROM runtime_deployment").Scan(&specification); err != nil || specification != "{}" {
		t.Fatal("deployment silently repaired", specification, err)
	}
}
