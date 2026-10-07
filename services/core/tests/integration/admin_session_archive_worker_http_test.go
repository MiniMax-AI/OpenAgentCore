package integration

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

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// Serve administrator reads from the pooled Session adapter and archive through
// the deployment execution operations on the Worker's lease, as server startup
// does.
func TestAdminSessionArchiveWorkerHTTPPostgres(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{83}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	installation := uuid.NewString()
	provider := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	deployments := deploymentService(t, s)
	providerConfig := func(setup deployment.Setup) *execution.RuntimeProvider {
		return &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider}
	}
	configuration := execution.NewDeferredRuntimeProvider(installation, func(ctx context.Context) (*execution.RuntimeProvider, error) {
		setup, err := deployments.Setup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return providerConfig(setup), nil
	}, func(_ context.Context, setup deployment.Setup) (execution.PreparedRuntimeDeployment, error) {
		return execution.PreparedRuntimeDeployment{Config: providerConfig(setup)}, nil
	})
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := fixtureOwner(s, lease)
	if err != nil {
		t.Fatal(errors.Join(err, lease.Close(t.Context())))
	}
	worker := startOwnedWorker(t, t.Context(), s, &execution.Dispatcher{Registry: runtimegateway.NewRegistry(), ManagedRuntimes: configuration}, owner)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = worker.Run(ctx)
		})
	}
	t.Cleanup(stop)
	selection := sandbox.Selection{DeploymentSpec: SandboxDeploymentTestSpec("e2b"), Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "fixture-api-key", Template: "runtime:" + uuid.NewString()}}
	if _, err := worker.InitializeSandboxDeployment(t.Context(), selection); err != nil {
		t.Fatal(err)
	}
	projectID := uuid.NewString()
	ctx := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ProjectID: projectID, RequestID: uuid.NewString(), TraceID: uuid.NewString()})
	_, management := fixtureProjects(t, s)
	project, err := management.CreateProject(ctx, projects.CreateProject{ID: projectID, Name: "Archive HTTP fixture"})
	if err != nil {
		t.Fatal(err)
	}
	create := func() sessions.Session {
		t.Helper()
		session, err := s.CreateSession(t.Context(), project.TenantID, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`)})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	active, untouched := create(), create()
	allocated, err := worker.ProvisionEnvironment(t.Context(), project.TenantID, active.Environment.ID, installation)
	if err != nil {
		t.Fatal(err)
	}
	input, err := sendMessage(t.Context(), s, project.TenantID, active.ID, "pending-turn", messageText("pending"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential("archive-administrator")})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := publicHandler(t, s, nil, "codex", storeKeys(s), workerExecution(t, worker), func(d *api.Dependencies) { d.Execution.SessionArchive = owner.Deployment }, withCoreKeys(admin))
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
	var archived sessions.ManagedArchive
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &archived) != nil || archived.State != "cleanup_pending" || archived.SessionID != active.ID {
		t.Fatalf("archive failed: %d %s", w.Code, w.Body)
	}
	allocation, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: project.TenantID, EnvironmentID: active.Environment.ID})
	if err != nil || allocation.ID != allocated.ID || allocation.State != "cleanup_pending" {
		t.Fatal("archive did not retain cleanup ownership", allocation, err)
	}
	turn, err := sessionAdapter(s).GetTurn(t.Context(), project.TenantID, active.ID, input.TurnID)
	if err != nil || turn.Status != sessions.TurnCancelled {
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
