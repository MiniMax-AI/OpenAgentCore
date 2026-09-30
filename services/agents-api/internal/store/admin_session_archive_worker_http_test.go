package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// Use the original Store for administrator reads, as server startup does, and
// route archive writes through the real Worker's execution-owned Store clone.
func TestAdminSessionArchiveWorkerHTTPPostgres(t *testing.T) {
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{83}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	installation := uuid.NewString()
	provider := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	providerConfig := func(setup store.SandboxSetup) *execution.RuntimeProvider {
		return &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, AdmissionPaused: setup.AdmissionPaused, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider}
	}
	configuration := execution.NewDeferredRuntimeProvider(installation, func(ctx context.Context) (*execution.RuntimeProvider, error) {
		setup, err := s.GetSandboxSetup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return providerConfig(setup), nil
	}, func(_ context.Context, setup store.SandboxSetup) (execution.PreparedRuntimeDeployment, error) {
		return execution.PreparedRuntimeDeployment{Config: providerConfig(setup)}, nil
	})
	worker, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: gateway.NewRegistry(), ManagedRuntimes: configuration})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = worker.Run(ctx)
		})
	}
	t.Cleanup(stop)
	selection := store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("e2b"), Provider: "e2b", E2B: &sandbox.E2BConfiguration{APIKey: "fixture-api-key", Template: "runtime:" + uuid.NewString()}}
	if _, err := worker.InitializeSandboxDeployment(t.Context(), selection); err != nil {
		t.Fatal(err)
	}
	projectID := uuid.NewString()
	ctx := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ProjectID: projectID, RequestID: uuid.NewString(), TraceID: uuid.NewString()})
	project, err := s.CreateProject(ctx, projectID, "Archive HTTP fixture")
	if err != nil {
		t.Fatal(err)
	}
	create := func() store.Session {
		t.Helper()
		session, err := s.CreateSession(t.Context(), project.TenantID, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`)})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	active, untouched := create(), create()
	owner, err := worker.ProvisionEnvironment(t.Context(), project.TenantID, active.Environment.ID, installation)
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(t.Context(), project.TenantID, active.ID, "pending-turn", json.RawMessage(`{"text":"pending"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveManagedSession(ctx, project.TenantID, active.ID, 1); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("fixture admission Store unexpectedly holds execution ownership", err)
	}
	admin, err := api.NewDeploymentAuthenticator([]string{device.HashCredential("archive-administrator")})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewDatabaseAuthenticator(s)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(s, auth, "codex", api.WithProjectAPIKeys(s, admin), api.WithAdminManagement(s), api.WithExecution(worker), api.WithSessionArchive(worker.ArchiveManagedSession))
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, session string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/core/v1/projects/"+projectID+"/sessions/"+session+"/archive", strings.NewReader(`{"expected_generation":1}`))
		r.Header.Set("Authorization", "Bearer archive-administrator")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request(http.MethodPost, active.ID)
	var archived store.ManagedSessionArchive
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &archived) != nil || archived.State != "cleanup_pending" || archived.SessionID != active.ID {
		t.Fatalf("archive did not use Worker's leased Store: %d %s", w.Code, w.Body)
	}
	allocation, err := s.GetRuntimeAllocation(t.Context(), project.TenantID, active.Environment.ID)
	if err != nil || allocation.ID != owner.ID || allocation.State != "cleanup_pending" {
		t.Fatal("archive did not retain cleanup ownership", allocation, err)
	}
	turn, err := s.GetTurn(t.Context(), project.TenantID, active.ID, input.TurnID)
	if err != nil || turn.Status != store.TurnCancelled {
		t.Fatal("archive did not cancel queued work", turn, err)
	}
	var audits int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM admin_audit_log WHERE project_id=$1 AND action='archive' AND resource_id=$2", projectID, active.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("archive did not commit administrator audit", audits, err)
	}
	stop()
	// Snapshot after shutdown: cancellation and lifecycle draining are complete.
	// A stale handler must not fall back to the still-open admission Store.
	snapshot := func() string {
		t.Helper()
		var value string
		if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'sessions',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM sessions s),
 'environments',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM environments e),
 'turns',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM turns t),
 'allocations',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM runtime_allocations a),
 'devices',(SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM devices d),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM admin_audit_log a))::text`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	w = request(http.MethodPost, untouched.ID)
	if w.Code < http.StatusBadRequest || before != snapshot() {
		t.Fatalf("closed Worker lease allowed archive mutation: %d %s", w.Code, w.Body)
	}
	w = request(http.MethodGet, active.ID)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &archived) != nil || archived.State != "cleanup_pending" {
		t.Fatalf("public Store could not read cleanup after Worker shutdown: %d %s", w.Code, w.Body)
	}
}
