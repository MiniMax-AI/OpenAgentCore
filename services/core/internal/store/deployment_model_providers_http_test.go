package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// Deployment defaults live in Core, are managed with the Core key and are
// frozen only into hosted Sessions; self-hosted Sessions bring their own
// provider, and a Session with no provider is rejected before any write.
func TestDeploymentModelProvidersHTTP(t *testing.T) {
	_, pool := store.NewTestStore(t)
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployment_model_providers"); err != nil {
		t.Fatal(err)
	}
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{53}, 32))
	st, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	tenant, projectKey, coreKey := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "defaults-http", TokenSHA256: runtimedevice.HashCredential(projectKey), TenantID: tenant}})
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential(coreKey)})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := publicHandler(t, st, db, auth, "codex", storeExecution(t, st), managedSandboxes(t, st), withCoreKeys(admin), withHarnesses([]string{"codex", "mcode"}))
	if err != nil {
		t.Fatal(err)
	}
	defaults := deploymentDefaults(t, db)
	call := func(method, path, key, body string, status int) map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		for _, secret := range []string{"deployment-canary", "session-canary", "agent-canary", "invalid-canary", `"api_key":`} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("%s %s returned a key", method, path)
			}
		}
		if w.Code != status {
			t.Fatalf("%s %s: status %d, expected %d: %s", method, path, w.Code, status, w.Body)
		}
		var result map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	text := func(raw json.RawMessage) string {
		var value string
		_ = json.Unmarshal(raw, &value)
		return value
	}
	providerView := func(configuration map[string]json.RawMessage) map[string]json.RawMessage {
		t.Helper()
		var provider map[string]json.RawMessage
		if err := json.Unmarshal(configuration["model_provider"], &provider); err != nil {
			t.Fatal("invalid safe model provider", err)
		}
		return provider
	}
	providerOf := func(sessionID string) string {
		t.Helper()
		provider, err := st.SessionModelExecution(t.Context(), tenant, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		return provider.APIKey
	}
	const path = "/core/v1/harnesses/codex/model-configuration"
	codexDefault := `{"model":"fixture","model_provider":{"protocol":"responses","base_url":"https://deployment.example/v1","api_key":"deployment-canary"}}`

	// Only the Core key manages defaults.
	call("GET", "/core/v1/harnesses", projectKey, "", 401)
	call("PUT", path, projectKey, codexDefault, 401)
	list := call("GET", "/core/v1/harnesses", coreKey, "", 200)
	var harnesses []struct {
		Object        string          `json:"object"`
		ID            string          `json:"id"`
		Enabled       bool            `json:"enabled"`
		Default       bool            `json:"default"`
		Configuration json.RawMessage `json:"model_configuration"`
	}
	if string(list["object"]) != `"list"` || json.Unmarshal(list["data"], &harnesses) != nil || len(harnesses) != 3 {
		t.Fatalf("unexpected harness list: %s", list["data"])
	}
	for _, harness := range harnesses {
		if harness.Object != "core.harness" || harness.Enabled != (harness.ID != "claude_sdk") ||
			harness.Default != (harness.ID == "codex") || string(harness.Configuration) != "null" {
			t.Fatalf("unexpected harness: %#v", harness)
		}
	}
	call("GET", path, coreKey, "", 404)
	call("PUT", "/core/v1/harnesses/other/model-configuration", coreKey, codexDefault, 404)
	for _, invalid := range []string{
		`{"protocol":"responses","base_url":"http://deployment.example/v1","api_key":"invalid-canary"}`,
		`{"protocol":"unknown","base_url":"https://deployment.example/v1","api_key":"invalid-canary"}`,
		`{"protocol":"responses","base_url":"https://deployment.example/v1"}`,
		`{"protocol":"responses","base_url":"https://deployment.example/v1","api_key":"invalid-canary","api_key_configured":true}`,
		`{"protocol":"responses","base_url":"https://deployment.example/v1","api_key":"invalid-canary","context_window":-1}`,
	} {
		call("PUT", path, coreKey, `{"model":"fixture","model_provider":`+invalid+`}`, 400)
	}
	call("PUT", "/core/v1/harnesses/mcode/model-configuration", coreKey, `{"model":"fixture","model_provider":{"protocol":"anthropic","base_url":"https://deployment.example/anthropic","api_key":"invalid-canary"}}`, 400)

	// Without any provider, hosted and self-hosted creation fail before any write.
	hosted := `{"agent":{"model":"hosted-model"},"environment":{"type":"openai_hosted"}}`
	selfHosted := `{"agent":{"model":"self-hosted-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`
	for _, body := range []string{hosted, selfHosted} {
		failure := call("POST", "/v1/agents/sessions", projectKey, body, 400)
		if !strings.Contains(string(failure["error"]), `"code":"model_provider_required","param":"x_agents_core.model_provider"`) {
			t.Fatalf("unclear failure: %s", failure["error"])
		}
	}

	saved := call("PUT", path, coreKey, codexDefault, 200)
	for _, field := range []string{"last_used_at", "last_error_code", "last_error_at"} {
		if string(saved[field]) != "null" {
			t.Fatal("new Core observation field is not null", field)
		}
	}
	for _, field := range []string{"revision", "recovery_pending"} {
		if _, ok := saved[field]; ok {
			t.Fatal("private observation field exposed", field)
		}
	}
	if text(saved["object"]) != "core.model_configuration" || text(saved["harness"]) != "codex" || text(providerView(saved)["base_url"]) != "https://deployment.example/v1" || string(providerView(saved)["api_key_configured"]) != "true" || text(saved["updated_at"]) == "" {
		t.Fatalf("unexpected provider view: %v", saved)
	}
	if retrieved := call("GET", path, coreKey, "", 200); text(providerView(retrieved)["base_url"]) != "https://deployment.example/v1" {
		t.Fatal("retrieved view differs")
	}

	for _, protocol := range []string{"anthropic", "chat_completions"} {
		bundle := strings.Replace(codexDefault, `"protocol":"responses"`, `"protocol":"`+protocol+`"`, 1)
		failure := call("PUT", path, coreKey, bundle, 400)
		if !strings.Contains(string(failure["error"]), "model_provider_protocol_unsupported") {
			t.Fatal("unsupported native protocol not explained")
		}
		provider, err := defaults.Resolve(t.Context(), "codex")
		if err != nil || provider == nil || provider.Provider.Protocol != "responses" || provider.Provider.APIKey != "deployment-canary" {
			t.Fatal("rejected protocol changed the stored default", err)
		}
	}
	call("PUT", path, coreKey, codexDefault, 200)

	// Simulate a default persisted when cross-protocol execution was supported.
	// It must remain readable, but cannot create new Sessions or be rewritten.
	historical := strings.Replace(codexDefault, `"protocol":"responses"`, `"protocol":"anthropic"`, 1)
	encrypted, err := cipher.SealDeploymentModelProvider([]byte(historical), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE deployment_model_providers SET protocol='anthropic', encrypted_config=$1 WHERE harness='codex'", encrypted); err != nil {
		t.Fatal(err)
	}
	incompatible := call("POST", "/v1/agents/sessions", projectKey, hosted, 400)
	if !strings.Contains(string(incompatible["error"]), "does not support this model provider protocol") || strings.Contains(string(incompatible["error"]), "credential_storage_unavailable") {
		t.Fatal("unsupported stored protocol was reported as a credential failure")
	}
	if text(providerView(call("GET", path, coreKey, "", 200))["protocol"]) != "anthropic" {
		t.Fatal("unsupported default was rewritten")
	}
	var retained []byte
	if err := pool.QueryRow(t.Context(), "SELECT encrypted_config FROM deployment_model_providers WHERE harness='codex'").Scan(&retained); err != nil || !bytes.Equal(retained, encrypted) {
		t.Fatal("rejected default changed its encrypted snapshot", err)
	}
	call("PUT", path, coreKey, codexDefault, 200)

	// Hosted Sessions freeze the default; later edits never reach them.
	hostedID := text(call("POST", "/v1/agents/sessions", projectKey, hosted, 201)["id"])
	if providerOf(hostedID) != "deployment-canary" {
		t.Fatal("hosted Session did not freeze the deployment default")
	}
	projection, err := st.GetSessionExecutionConfiguration(t.Context(), tenant, hostedID)
	if err != nil || projection.ModelProvider.Source != "deployment" || projection.ModelProvider.Status != "available" || projection.ModelProvider.Configuration == nil || projection.ModelProvider.Configuration.BaseURL != "https://deployment.example/v1" {
		t.Fatal("deployment selection not recorded", projection, err)
	}
	call("PUT", path, coreKey, strings.Replace(codexDefault, "deployment.example", "changed.example", 1), 200)
	if providerOf(hostedID) != "deployment-canary" {
		t.Fatal("a changed default reached an existing Session")
	}

	// Self-hosted Sessions take the request or saved Agent bundle, never the default.
	failure := call("POST", "/v1/agents/sessions", projectKey, selfHosted, 400)
	if !strings.Contains(string(failure["error"]), "never to self_hosted") {
		t.Fatalf("self-hosted Session used or misreported the deployment default: %s", failure["error"])
	}
	requestProvider := strings.TrimSuffix(selfHosted, "}") + `,"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://session.example/v1","api_key":"session-canary"}}}`
	if id := text(call("POST", "/v1/agents/sessions", projectKey, requestProvider, 201)["id"]); providerOf(id) != "session-canary" {
		t.Fatal("self-hosted Session lost its request provider")
	}
	agentID := text(call("POST", "/v1/agents", projectKey, `{"model":"agent-model","x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://agent.example/v1","api_key":"agent-canary"}}}`, 201)["id"])
	fromAgent := `{"agent_id":"` + agentID + `","environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`
	if id := text(call("POST", "/v1/agents/sessions", projectKey, fromAgent, 201)["id"]); providerOf(id) != "agent-canary" {
		t.Fatal("self-hosted Session lost its saved Agent provider")
	}

	// Removal is idempotent and audited; hosted creation then fails fast again.
	call("DELETE", path, coreKey, "", 204)
	call("DELETE", path, coreKey, "", 204)
	call("GET", path, coreKey, "", 404)
	call("POST", "/v1/agents/sessions", projectKey, hosted, 400)
	if providerOf(hostedID) != "deployment-canary" {
		t.Fatal("removing the default changed an existing Session")
	}
	page, err := auditpg.New(pgunit.NewPool(pool)).ListAdminAudit(t.Context(), adminaudit.Filter{ResourceType: "deployment_model_provider", ResourceID: "codex"})
	if err != nil || len(page.Data) < 4 || page.Data[0].Action != "delete" || page.Data[0].ProjectID != nil {
		t.Fatal("deployment writes not audited", page, err)
	}
}

// A hosted or self-hosted Session created before providers were required has
// no frozen provider: new work is rejected before anything is queued, and input
// reserved before the upgrade fails with that reason instead of waiting.
func TestLegacySessionWithoutProviderCannotStartWork(t *testing.T) {
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`), true)
	legacy, err := h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: []byte(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	executor := connectFixtureRuntime(t, h, legacy)
	// Reserved directly, as a pre-upgrade Core did.
	pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, legacy.ID, "before-upgrade", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"old"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	worker, stop := startEnvironmentExpiryWorker(t, h.db, h.d)
	defer stop()
	_, pool := store.NewTestStore(t)
	reservations := func() int {
		t.Helper()
		var count int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM environment_input_reservations WHERE session_id=$1", legacy.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := reservations()
	message := []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"start"}`)}}
	if _, err := worker.SubmitInputs(t.Context(), h.tenant, legacy.ID, uuid.NewString(), message); !errors.Is(err, execution.ErrModelProviderRequired) {
		t.Fatal("provider-free Session accepted work", err)
	}
	if after := reservations(); after != before {
		t.Fatal("rejected work was queued", before, after)
	}
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "legacy reservation settled", func() bool {
		got, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, legacy.ID, pending.ID)
		return err == nil && got.State == store.EnvironmentInputFailed
	})
	session, err := h.s.GetSession(t.Context(), h.tenant, legacy.ID)
	if err != nil || session.EnvironmentInputActivity == nil || session.EnvironmentInputActivity.Failure != "model_provider_required" {
		t.Fatal("legacy reservation did not fail with its reason", session.EnvironmentInputActivity, err)
	}
	_ = executor.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		var frame proto.Envelope
		if executor.conn.ReadJSON(&frame) != nil {
			break
		}
		if frame.Type == proto.TypeExecutionPrepare || frame.Type == proto.TypePromptRequest {
			t.Fatal("provider-free work reached the executor", frame.Type)
		}
	}
}

// A none Session may freeze the deployment default, so its caller intent is
// recorded first: a same-key retry returns the committed Session after the
// default was replaced or removed.
func TestNoneSessionRetryAfterDeploymentDefaultChanges(t *testing.T) {
	st, db := newModelTestStoreDB(t)
	if _, err := db.pool.Exec(t.Context(), "DELETE FROM deployment_model_providers"); err != nil {
		t.Fatal(err)
	}
	tenant, token := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "none-retry", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant}})
	handler, err := publicHandler(t, st, db, auth, "codex", storeExecution(t, st))
	if err != nil {
		t.Fatal(err)
	}
	admin := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "abcd1234", RequestID: "none-retry", TraceID: "none-retry"})
	defaults := deploymentDefaults(t, db)
	setDefault := func(key string) {
		t.Helper()
		if _, err := defaults.Replace(admin, modelconfiguration.Replacement{Harness: "codex", Configuration: v1.ModelConfigurationInput{ModelProvider: v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://deployment.example/v1", APIKey: key}, Model: "fixture"}}); err != nil {
			t.Fatal(err)
		}
	}
	create := func(key string, agent ...string) string {
		t.Helper()
		body := `{"agent":{"model":"m"},"environment":{"type":"none"},"input":"hello"}`
		if len(agent) > 0 {
			body = `{"agent":` + agent[0] + `,"environment":{"type":"none"},"input":"hello"}`
		}
		r := httptest.NewRequest("POST", "/v1/agents/sessions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var session struct{ ID string }
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &session) != nil {
			t.Fatalf("creation %d: %s", w.Code, w.Body)
		}
		return session.ID
	}
	setDefault("first-default-key")
	snapshot, err := defaults.Resolve(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	original := create(key)
	if provider, err := st.SessionModelExecution(t.Context(), tenant, original); err != nil || provider.APIKey != "first-default-key" {
		t.Fatal("none Session did not freeze the deployment default", err)
	}
	setDefault("rotated-default-key")
	if create(key) != original || create(key, `{"model":"m","text":{"verbosity":"medium"}}`) != original {
		t.Fatal("retry after rotation created another Session")
	}
	if err := defaults.Delete(admin, "codex"); err != nil {
		t.Fatal(err)
	}
	if create(key) != original {
		t.Fatal("retry after removal created another Session")
	}
	if provider, err := st.SessionModelExecution(t.Context(), tenant, original); err != nil || provider.APIKey != "first-default-key" {
		t.Fatal("retry changed the frozen provider", err)
	}
	var revision uuid.UUID
	if err := db.pool.QueryRow(t.Context(), "SELECT deployment_provider_revision FROM session_execution_configuration WHERE session_id=$1", original).Scan(&revision); err != nil || revision != snapshot.Revision {
		t.Fatal("API retry changed frozen revision", err)
	}
}

func TestDeploymentProviderResolutionPairsRevisionDuringReplacement(t *testing.T) {
	st, db := newManagedTestStoreDB(t)
	tenant, token := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "tuple-test", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant}})
	admin := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
	provider := v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://original.example/v1", APIKey: "original-fixture-key"}
	defaults := deploymentDefaults(t, db)
	if _, err := defaults.Replace(admin, modelconfiguration.Replacement{Harness: "codex", Configuration: v1.ModelConfigurationInput{ModelProvider: provider, Model: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	original, err := defaults.Resolve(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	// Replacement after the atomic tuple read but before API resolution returns
	// must never pair the old ciphertext with the replacement's revision.
	resolver := func(ctx context.Context, harness string) (*modelconfiguration.Snapshot, error) {
		snapshot, err := defaults.Resolve(ctx, harness)
		if err != nil {
			return nil, err
		}
		replacement := provider
		replacement.APIKey = "replacement-fixture-key"
		_, err = defaults.Replace(admin, modelconfiguration.Replacement{Harness: harness, Configuration: v1.ModelConfigurationInput{ModelProvider: replacement, Model: "fixture"}})
		return snapshot, err
	}
	handler, err := publicHandler(t, st, db, auth, "codex", storeExecution(t, st), modelProviderDefaults(resolver))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"m"},"environment":{"type":"none"},"input":"hello"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var session struct{ ID string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &session) != nil {
		t.Fatalf("creation failed: %d %s", w.Code, w.Body)
	}
	var revision uuid.UUID
	if err = db.pool.QueryRow(t.Context(), "SELECT deployment_provider_revision FROM session_execution_configuration WHERE session_id=$1", session.ID).Scan(&revision); err != nil || revision != original.Revision {
		t.Fatal("tuple revision changed", err)
	}
	frozen, err := st.SessionModelExecution(t.Context(), tenant, session.ID)
	if err != nil || frozen == nil || *frozen != provider {
		t.Fatal("tuple bundle changed", err)
	}
	for _, private := range []string{"original-fixture-key", "replacement-fixture-key", revision.String(), "deployment_provider_revision"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private snapshot entered public response")
		}
	}
}

// The official-client job starts a fresh server with an independent encryption
// key after the Go suite. Deployment-wide fixtures must leave its database intact.
func TestDeploymentProviderResolutionFixtureIsolation(t *testing.T) {
	_, shared := store.NewTestStore(t)
	revisions := func() string {
		t.Helper()
		var value string
		if err := shared.QueryRow(t.Context(), "SELECT COALESCE(jsonb_object_agg(harness, revision), '{}'::jsonb)::text FROM deployment_model_providers").Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := revisions()
	t.Run("resolution", TestDeploymentProviderResolutionPairsRevisionDuringReplacement)
	if revisions() != before {
		t.Fatal("deployment provider fixture changed the shared test database")
	}
}

// deploymentDefaults serves deployment default model configurations from db,
// as the Core routes do.
func deploymentDefaults(t *testing.T, db fixtureDB) *modelconfiguration.Service {
	t.Helper()
	service, err := modelconfiguration.NewService(modelconfigurationpg.New(pgunit.NewPool(db.pool), db.cipher))
	if err != nil {
		t.Fatal(err)
	}
	return service
}
