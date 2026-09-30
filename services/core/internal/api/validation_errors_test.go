package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// validationStore counts every persistence attempt so rejected requests can
// prove that validation ran before any write.
type validationStore struct {
	writes int
}

func (s *validationStore) CreateAgent(_ context.Context, command agents.CreateCommand) (agents.Agent, error) {
	s.writes++
	return agents.Agent{ID: uuid.NewString(), TenantID: command.TenantID, Configuration: command.Configuration, Metadata: command.Metadata, CreatedAt: time.Unix(1700000000, 0), UpdatedAt: time.Unix(1700000000, 0)}, nil
}

func (s *validationStore) UpdateAgent(_ context.Context, command agents.UpdateCommand) (agents.Agent, error) {
	s.writes++
	return agents.Agent{ID: command.AgentID, TenantID: command.TenantID, Configuration: json.RawMessage(`{"model":"validation-model"}`), Metadata: map[string]string{}}, nil
}

func (s *validationStore) CreateVault(_ context.Context, input vaults.CreateVault) (vaults.Vault, error) {
	s.writes++
	return vaults.Vault{ID: uuid.NewString(), TenantID: input.TenantID, Name: input.Name, Metadata: input.Metadata}, nil
}

func (s *validationStore) UpdateSessionMetadata(_ context.Context, tenant, id string, metadata map[string]string) (store.Session, error) {
	s.writes++
	return store.Session{ID: id, TenantID: tenant, Metadata: metadata, Configuration: json.RawMessage(`{"agent":{"id":"agent_validation","model":"validation-model"},"environment":{"type":"none"}}`)}, nil
}

func (s *validationStore) CreateSession(_ context.Context, tenant string, input store.CreateSessionInput) (store.Session, error) {
	s.writes++
	return store.Session{ID: uuid.NewString(), TenantID: tenant, Metadata: input.Metadata, Configuration: input.Configuration}, nil
}

func (s *validationStore) CreateEnvironmentTemplate(context.Context, string, store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error) {
	s.writes++
	return store.EnvironmentTemplate{ID: uuid.NewString(), NetworkAccess: "enabled"}, nil
}

func (s *validationStore) UpdateEnvironmentTemplate(_ context.Context, _, id string, input store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error) {
	s.writes++
	return store.EnvironmentTemplate{ID: id, NetworkAccess: input.NetworkAccess, AllowedDomains: input.AllowedDomains}, nil
}

// serve takes every write from the handler, and the Worker admits Sessions
// with initial input into s. Session reads are unexpected.
func (s *validationStore) serve(d *Dependencies, f *testFakes) {
	d.Execution = f.execution()
	f.admission.createSession = s.CreateSession
	f.agents.create, f.agents.update = s.CreateAgent, s.UpdateAgent
	f.vaults.createVault = s.CreateVault
	f.sessions.getSession, f.sessions.updateSessionMetadata = nil, s.UpdateSessionMetadata
	f.environmentTemplates.createEnvironmentTemplate, f.environmentTemplates.updateEnvironmentTemplate = s.CreateEnvironmentTemplate, s.UpdateEnvironmentTemplate
}

func validationHandler(t *testing.T) (http.Handler, *validationStore) {
	t.Helper()
	s := &validationStore{}
	h, _, _ := testHandler(t, s.serve)
	return h, s
}

func errorFields(t *testing.T, w *httptest.ResponseRecorder) (string, *string, string) {
	t.Helper()
	var response struct {
		Error struct {
			Type, Message string
			Code          string
			Param         *string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid error body %d %s", w.Code, w.Body)
	}
	if response.Error.Type != "invalid_request_error" {
		t.Fatalf("error type = %s", w.Body)
	}
	return response.Error.Code, response.Error.Param, response.Error.Message
}

func TestMetadataValidationUsesOfficialFields(t *testing.T) {
	seventeen := map[string]string{}
	sixteen := map[string]string{}
	for i := range 17 {
		seventeen[fmt.Sprintf("k%02d", i)] = "v"
		if i < 16 {
			sixteen[fmt.Sprintf("k%02d", i)] = "v"
		}
	}
	encode := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	key65, key64 := strings.Repeat("K", 65), strings.Repeat("雪", 64)
	operations := []struct {
		name, method, path, body string
		limited                  bool
	}{
		{"agent create", http.MethodPost, "/v1/agents", `{"model":"validation-model","metadata":%s}`, true},
		{"agent update", http.MethodPost, "/v1/agents/" + uuid.NewString(), `{"metadata":%s}`, true},
		{"session create", http.MethodPost, "/v1/agents/sessions", `{"agent":{"model":"validation-model"},"environment":{"type":"none"},"input":"Validate metadata.","metadata":%s}`, true},
		{"session update", http.MethodPost, "/v1/agents/sessions/" + uuid.NewString(), `{"metadata":%s}`, true},
		{"vault create", http.MethodPost, "/v1/vaults", `{"metadata":%s}`, false},
	}
	// limit marks pinned count/length limits, which Vaults do not apply (M5).
	rejected := []struct {
		name, metadata, param, message string
		limit                          bool
	}{
		{"too many pairs", encode(seventeen), "metadata", "Invalid 'metadata': too many properties. Expected an object with at most 16 properties, but got an object with 17 properties instead.", true},
		{"long key", `{"` + key65 + `":"v"}`, "metadata." + key65, "Invalid property name in 'metadata': '" + key65 + "' is too long. Expected a string with maximum length 64, but got a string with length 65 instead.", true},
		{"long value", `{"k":"` + strings.Repeat("雪", 513) + `"}`, "metadata.k", "Invalid 'metadata.k': string too long. Expected a string with maximum length 512, but got a string with length 513 instead.", true},
		{"integer", `{"k":1}`, "metadata.k", "Invalid type for 'metadata.k': expected a string, but got an integer instead.", false},
		{"number", `{"k":1.5}`, "metadata.k", "Invalid type for 'metadata.k': expected a string, but got a number instead.", false},
		{"boolean", `{"k":false}`, "metadata.k", "Invalid type for 'metadata.k': expected a string, but got a boolean instead.", false},
		{"object", `{"k":{"nested":"v"}}`, "metadata.k", "Invalid type for 'metadata.k': expected a string, but got an object instead.", false},
		{"array", `{"k":["v"]}`, "metadata.k", "Invalid type for 'metadata.k': expected a string, but got an array instead.", false},
		{"null", `{"a":null}`, "metadata.a", "Invalid type for 'metadata.a': expected a string, but got null instead.", false},
		{"document order", `{"z":"v","b":null,"a":1}`, "metadata.b", "Invalid type for 'metadata.b': expected a string, but got null instead.", false},
		{"type before count", `{"k00":"v","k01":"v","k02":"v","k03":"v","k04":"v","k05":"v","k06":"v","k07":"v","k08":"v","k09":"v","k10":"v","k11":"v","k12":"v","k13":"v","k14":"v","k15":"v","k16":2}`, "metadata.k16", "Invalid type for 'metadata.k16': expected a string, but got an integer instead.", false},
		{"null character value", `{"k":"a\u0000b"}`, "metadata.k", "Invalid 'metadata.k': string contains U+0000, which this service cannot store.", false},
		{"null character key", `{"a\u0000b":"v"}`, "metadata.a\x00b", "Invalid property name in 'metadata': 'a\x00b' contains U+0000, which this service cannot store.", false},
	}
	accepted := []struct{ name, metadata string }{
		{"boundary pairs", encode(sixteen)},
		{"boundary key", `{"` + key64 + `":"v"}`},
		{"boundary value", `{"k":"` + strings.Repeat("雪", 512) + `"}`},
		{"empty", `{}`},
		{"null", `null`},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			h, s := validationHandler(t)
			for _, tc := range rejected {
				w := credentialRequest(h, op.method, op.path, fmt.Sprintf(op.body, tc.metadata))
				if tc.limit && !op.limited {
					// Vault metadata keeps only its storage bound (M5).
					if w.Code != http.StatusCreated {
						t.Fatalf("%s: Vault metadata limit changed: %d %s", tc.name, w.Code, w.Body)
					}
					continue
				}
				code, param, message := errorFields(t, w)
				if w.Code != http.StatusBadRequest || code != "invalid_request_error" || param == nil || *param != tc.param || message != tc.message {
					t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
				}
			}
			if want := map[bool]int{true: 0, false: 3}[op.limited]; s.writes != want {
				t.Fatalf("rejected metadata reached storage: %d writes", s.writes)
			}
			// Type errors precede the generic whole-body error.
			body := strings.Replace(fmt.Sprintf(op.body, `{"k":1}`), "{", `{"unsupported_field":true,`, 1)
			w := credentialRequest(h, op.method, op.path, body)
			if code, param, _ := errorFields(t, w); w.Code != http.StatusBadRequest || code != "invalid_request_error" || param == nil || *param != "metadata.k" {
				t.Fatalf("metadata type did not precede generic error: %d %s", w.Code, w.Body)
			}
			for _, tc := range accepted {
				before := s.writes
				w := credentialRequest(h, op.method, op.path, fmt.Sprintf(op.body, tc.metadata))
				if w.Code >= 300 || s.writes != before+1 {
					t.Fatalf("%s rejected: %d %s", tc.name, w.Code, w.Body)
				}
			}
		})
	}
}

func TestAgentNameLengthUsesOfficialFields(t *testing.T) {
	h, s := validationHandler(t)
	for _, path := range []string{"/v1/agents", "/v1/agents/" + uuid.NewString()} {
		for _, name := range []string{strings.Repeat("n", 129), strings.Repeat("雪", 129)} {
			w := credentialRequest(h, http.MethodPost, path, `{"model":"validation-model","name":"`+name+`"}`)
			code, param, message := errorFields(t, w)
			if w.Code != http.StatusBadRequest || code != "invalid_request_error" || param == nil || *param != "name" || message != "Invalid 'name': string too long. Expected a string with maximum length 128, but got a string with length 129 instead." {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
		}
		if s.writes != 0 {
			t.Fatal("rejected name reached storage")
		}
		for _, name := range []string{strings.Repeat("雪", 128), "", "  untrimmed  "} {
			before := s.writes
			w := credentialRequest(h, http.MethodPost, path, `{"model":"validation-model","name":"`+name+`"}`)
			if w.Code >= 300 || s.writes != before+1 {
				t.Fatalf("name %q rejected: %d %s", name, w.Code, w.Body)
			}
		}
		s.writes = 0
	}
}

func TestTemplateNetworkRejectionsUseOfficialCode(t *testing.T) {
	h, s := validationHandler(t)
	domains := make([]string, 101)
	for i := range domains {
		domains[i] = fmt.Sprintf("d%d.example.com", i)
	}
	many, err := json.Marshal(domains)
	if err != nil {
		t.Fatal(err)
	}
	rejected := []string{
		`{"access":"restricted","allowed_domains":["*.example.com"]}`,
		`{"access":"restricted","allowed_domains":["example.com:443"]}`,
		`{"access":"restricted","allowed_domains":["https://example.com"]}`,
		`{"access":"restricted","allowed_domains":["2001:db8::1"]}`,
		`{"access":"restricted","allowed_domains":[""]}`,
		`{"access":"restricted","allowed_domains":[]}`,
		`{"access":"restricted","allowed_domains":null}`,
		`{"access":"restricted","allowed_domains":` + string(many) + `}`,
		`{"access":"enabled","allowed_domains":["example.com"]}`,
	}
	template := uuid.NewString()
	for _, network := range rejected {
		for _, request := range []struct{ path, body string }{
			{"/v1/agents/environments/templates", `{"name":"network","network":` + network + `}`},
			{"/v1/agents/environments/templates/" + template, `{"network":` + network + `}`},
			{"/v1/agents/sessions", `{"agent":{"model":"validation-model"},"environment":{"type":"openai_hosted","network":` + network + `}}`},
		} {
			w := credentialRequest(h, http.MethodPost, request.path, request.body)
			code, param, message := errorFields(t, w)
			if w.Code != http.StatusBadRequest || code != "invalid_request_error" || param != nil || message != errNetworkPolicy.message {
				t.Fatalf("%s %s: %d %s", request.path, network, w.Code, w.Body)
			}
		}
	}
	if s.writes != 0 {
		t.Fatal("rejected network reached storage")
	}
	// Other unsupported template installation fields keep their local code.
	for _, body := range []string{`{"unsupported":true,"network":{"access":"restricted","allowed_domains":["*.example.com"]}}`, `{"network":{"access":"restricted","allowed_domains":"example.com"}}`, `{"packages":{"cargo":["x"]}}`} {
		w := credentialRequest(h, http.MethodPost, "/v1/agents/environments/templates", body)
		if code, _, _ := errorFields(t, w); w.Code != http.StatusBadRequest || code != "unsupported_or_invalid_configuration" {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	// The hostname grammar is unchanged.
	for _, network := range []string{`{"access":"restricted","allowed_domains":["Example.COM","localhost","example.com","example.com"]}`, `{"access":"disabled"}`, `{"access":"enabled"}`, `null`} {
		before := s.writes
		w := credentialRequest(h, http.MethodPost, "/v1/agents/environments/templates/"+template, `{"network":`+network+`}`)
		if w.Code != http.StatusOK || s.writes != before+1 {
			t.Fatalf("%s rejected: %d %s", network, w.Code, w.Body)
		}
	}
}

func TestUnstorableTextMapsToInvalidRequest(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{fmt.Errorf("create agent: %w", &pgconn.PgError{Code: "22P05"}), http.StatusBadRequest},
		{fmt.Errorf("create vault: %w", &pgconn.PgError{Code: "22021"}), http.StatusBadRequest},
		{&pgconn.PgError{Code: "23505"}, http.StatusInternalServerError},
	} {
		w := httptest.NewRecorder()
		writeStoreError(w, httptest.NewRequest(http.MethodPost, "/v1/agents", nil), tc.err)
		var response v1.ErrorResponse
		if w.Code != tc.status || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Error.Param != nil {
			t.Fatalf("%v: %d %s", tc.err, w.Code, w.Body)
		}
		if tc.status == http.StatusBadRequest && (response.Error.Code == nil || *response.Error.Code != "invalid_request_error" || response.Error.Type != "invalid_request_error" || response.Error.Message != unstorableTextMessage) {
			t.Fatalf("%v: %s", tc.err, w.Body)
		}
	}
}
