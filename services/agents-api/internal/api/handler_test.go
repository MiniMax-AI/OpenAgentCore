package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type recordingStore struct {
	ResourceStore
	tenant            string
	input             store.CreateSessionInput
	sessions          []store.Session
	nextSessionCursor string
	listTenant        string
	listAfter         string
	listLimit         int
	listAscending     bool
}

func (s *recordingStore) ListSessions(_ context.Context, tenant, after string, limit int, ascending bool, _ *string) (store.SessionPage, error) {
	s.listTenant, s.listAfter, s.listLimit, s.listAscending = tenant, after, limit, ascending
	return store.SessionPage{Sessions: append([]store.Session(nil), s.sessions...), NextCursor: s.nextSessionCursor}, nil
}

func (s *recordingStore) GetSession(ctx context.Context, tenant, id string) (store.Session, error) {
	if s.ResourceStore != nil {
		return s.ResourceStore.GetSession(ctx, tenant, id)
	}
	return store.Session{ID: id, TenantID: tenant, Configuration: json.RawMessage(`{"environment":{"type":"none"}}`)}, nil
}

func (s *recordingStore) FindSessionCreation(context.Context, string, string, json.RawMessage, identity.Subject) (store.SessionCreation, error) {
	return store.SessionCreation{}, store.ErrNotFound
}

func (s *recordingStore) CreateSession(_ context.Context, tenant string, input store.CreateSessionInput) (store.Session, error) {
	s.tenant, s.input = tenant, input
	return store.Session{ID: uuid.NewString(), TenantID: tenant, Metadata: input.Metadata, Configuration: input.Configuration, CreatedAt: time.Unix(1700000000, 0)}, nil
}

func testHandler(t *testing.T, options ...Option) (http.Handler, *recordingStore, string) {
	t.Helper()
	tenant := uuid.NewString()
	hash := sha256.Sum256([]byte("test-api-key"))
	auth, err := NewAuthenticator([]APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: hex.EncodeToString(hash[:]), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	s := &recordingStore{}
	h, err := NewHandler(s, auth, "codex", options...)
	if err != nil {
		t.Fatal(err)
	}
	return h, s, tenant
}

func TestHTTPConfigurationAndTenantIdentity(t *testing.T) {
	s := &recordingStore{}
	h, _, tenant := testHandler(t, WithExecution(&inputRecorder{ResourceStore: s}))
	body := `{"agent":{"model":"requested-model","instructions":"Keep this."},"environment":{"type":"none"},"metadata":{"tenant_id":"untrusted-tenant"},"input":"Follow the configured instructions."}`
	// Unknown query keys are ignored and never select the tenant.
	request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions?tenant_id=untrusted-tenant", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set("OpenAI-Beta", "agents=v1")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "retry-key")
	request.Header.Set("X-Tenant-ID", "untrusted-tenant")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != http.StatusCreated || s.tenant != tenant || s.input.Engine != "codex" || s.input.IdempotencyKey != "retry-key" {
		t.Fatalf("request = %d %s; tenant=%s, engine=%s", w.Code, w.Body, s.tenant, s.input.Engine)
	}
	var response v1.Session
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Agent.Model != "requested-model" || response.Object != "agent.session" || response.Status != "idle" || response.CreatedAt != 1700000000 {
		t.Fatalf("invalid response: %s, %v", w.Body, err)
	}
	if response.RequiredActions == nil || response.VaultIDs == nil || response.Agent.Tools == nil {
		t.Fatal("upstream list fields must be empty arrays, not null")
	}
}

// Session responses carry both reasoning keys (SES-23); the stored
// configuration and creation retry identity keep their original encoding.
func TestSessionResponseReasoningKeysAreExplicit(t *testing.T) {
	s := &recordingStore{}
	h, _, _ := testHandler(t, WithExecution(&inputRecorder{ResourceStore: s}))
	request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"requested-model"},"environment":{"type":"none"},"input":"hello"}`))
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set("OpenAI-Beta", "agents=v1")
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	var body struct {
		Agent struct {
			Reasoning json.RawMessage `json:"reasoning"`
		} `json:"agent"`
	}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &body) != nil || string(body.Agent.Reasoning) != `{"effort":null,"summary":null}` {
		t.Fatalf("response = %d %s", w.Code, w.Body)
	}
	var stored struct {
		Agent struct {
			Reasoning json.RawMessage `json:"reasoning"`
		} `json:"agent"`
	}
	if json.Unmarshal(s.input.Configuration, &stored) != nil || string(stored.Agent.Reasoning) != `{}` {
		t.Fatalf("stored configuration changed: %s", s.input.Configuration)
	}
}

func TestHTTPRejectsUntrustedOrUnsupportedRequests(t *testing.T) {
	valid := `{"agent":{"model":"example"},"environment":{"type":"none"},"input":"Run the configured request."}`
	for _, test := range []struct {
		name, auth, beta, path, body string
		status                       int
	}{
		{"missing auth", "", "agents=v1", "/v1/agents/sessions", valid, 401},
		{"invalid auth", "Bearer wrong", "agents=v1", "/v1/agents/sessions", valid, 401},
		{"missing beta", "Bearer test-api-key", "", "/v1/agents/sessions", valid, 400},
		{"tenant body", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"agent":`, `"tenant_id":"other","agent":`, 1), 400},
		{"hosted environment without managed deployment", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"none"`, `"openai_hosted"`, 1), 503},
		{"self-hosted environment", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"none"`, `"self_hosted"`, 1), 400},
		{"initial input", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", valid, 503},
		{"stream unavailable", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"agent":`, `"stream":true,"agent":`, 1), 503},
		{"unknown saved agent", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"agent":`, `"agent_id":"saved","agent":`, 1), 404},
		{"unknown agent option", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"model":`, `"tools":[{}],"model":`, 1), 400},
		{"multiple objects", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", valid + `{}`, 400},
		{"null body as empty object", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", `null`, 400},
		{"large body", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", `{"agent":{"model":"` + strings.Repeat("x", 16*1024*1024) + `"}}`, 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, s, _ := testHandler(t)
			r := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			r.Header.Set("Authorization", test.auth)
			r.Header.Set("OpenAI-Beta", test.beta)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var response v1.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("response = %d %s: %v", w.Code, w.Body, err)
			}
			// Beta 401s have a null code (HP-07); every other rejection names one.
			coded := response.Error.Code != nil && *response.Error.Code != ""
			if w.Code != test.status || coded == (test.status == http.StatusUnauthorized) || s.tenant != "" {
				t.Fatalf("response = %d %s, stored tenant = %s", w.Code, w.Body, s.tenant)
			}
		})
	}
}
