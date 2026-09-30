package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
)

func TestEnvironmentTemplatesErrors(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		body   string
	}{
		{environmenttemplates.ErrNotFound, 404, "not_found_error"},
		{fmt.Errorf("create: %w", environmenttemplates.ErrInvalidInput), 400, invalidInputMessage},
		{textvalue.ErrUnstorable, 400, unstorableTextMessage},
		{credentialcrypto.ErrUnavailable, 503, "credential_storage_unavailable"},
		{errors.New("template-canary"), 500, "internal_error"},
	} {
		response := httptest.NewRecorder()
		writeEnvironmentTemplatesError(response, httptest.NewRequest(http.MethodPost, "/v1/agents/environments/templates", nil), test.err)
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.body) || strings.Contains(response.Body.String(), "canary") {
			t.Errorf("%v: %d %s", test.err, response.Code, response.Body)
		}
	}
}

func TestTemplateConfigurationRejectsUnqualifiedInputs(t *testing.T) {
	for _, raw := range []string{`{"network":{"access":"restricted","allowed_domains":["Example.com","example.com"]}}`, `{}`, `{"packages":{}}`, `{"packages":{"npm":null}}`, `{"name":null,"network":null}`, `{"name":"保存","network":{"access":"disabled"},"env":{},"files":[],"setup_commands":[],"packages":{"npm":null}}`} {
		if _, err := decodeTemplateInput([]byte(raw)); err != nil {
			t.Fatalf("supported input: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"name":""}`, `{"name":42}`, `{"type":"openai_hosted"}`, `{"env":{"PATH":"confidential-canary"}}`, `{"setup_commands":[{"command":"confidential-canary","cwd":"relative"}]}`, `{"packages":{"system":["-o"]}}`, `{"packages":{"system":[""]}}`, `{"packages":{"system":[null]}}`, `{"plugins":[{}]}`, `{"skills":[{}]}`, `{"capability_directories":["/private"]}`} {
		if _, err := decodeTemplateInput([]byte(raw)); err == nil {
			t.Fatalf("unsupported input accepted: %s", raw)
		}
	}
	h, _, _ := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/environments/templates", strings.NewReader(`{"env":{"PATH":"confidential-canary"}}`))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("OpenAI-Beta", "agents=v1")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "confidential-canary") {
		t.Fatal("confidential input not safely rejected", response.Code, response.Body.String())
	}
}

// templateLookup is a resolved Template with the given network policy and
// installations.
type templateLookup struct {
	network     string
	domains     []string
	skills      []environmentconfig.Skill
	plugins     []environmentconfig.Plugin
	directories []string
}

func (l *templateLookup) resolved() environmenttemplates.Resolved {
	return environmenttemplates.Resolved{Template: environmenttemplates.Template{ID: "saved", NetworkAccess: l.network, AllowedDomains: l.domains}, Setup: environmentconfig.Setup{Skills: l.skills, Plugins: l.plugins, CapabilityDirectories: l.directories}}
}

// Session creation resolves the Template for the caller's tenant, and a
// Template it cannot resolve answers with the Template's error.
func TestSessionCreationResolvesTemplateForTenant(t *testing.T) {
	var resolved []string
	h, _, tenant := testHandler(t, func(_ *Dependencies, f *testFakes) {
		f.environmentTemplatesReader.resolve = func(_ context.Context, tenant, id string) (environmenttemplates.Resolved, error) {
			resolved = append(resolved, tenant, id)
			return environmenttemplates.Resolved{}, environmenttemplates.ErrNotFound
		}
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","environment_template_id":"saved"},"input":"Start."}`))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("OpenAI-Beta", "agents=v1")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"not_found_error"`) || !reflect.DeepEqual(resolved, []string{tenant, "saved"}) {
		t.Fatal("missing Template", response.Code, response.Body.String(), resolved)
	}
}

func TestTemplateResolutionAndCreationIntent(t *testing.T) {
	lookup := &templateLookup{network: "disabled"}
	request := func(raw string) sessionRequest {
		t.Helper()
		var decoded decodedSessionRequest
		if err := json.Unmarshal([]byte(`{"agent":{"model":"test"},"environment":`+raw+`}`), &decoded); err != nil {
			t.Fatal(err)
		}
		input, err := decoded.validated()
		if err != nil {
			t.Fatal(err)
		}
		return input
	}
	inherited := request(`{"type":"openai_hosted","environment_template_id":"saved"}`)
	intent, err := sessionCreationRequest(inherited, nil)
	if err != nil || !strings.Contains(string(intent), `"environment_template_id":"saved"`) {
		t.Fatal("missing caller intent", string(intent), err)
	}
	if err := applyTemplateEnvironment(&inherited, lookup.resolved()); err != nil || inherited.Environment.Network.Access != "disabled" {
		t.Fatal("inheritance failed", err)
	}
	raw, _ := json.Marshal(inherited.Environment)
	if strings.Contains(string(raw), "template") {
		t.Fatal("template leaked to execution", string(raw))
	}
	broader := request(`{"type":"openai_hosted","environment_template_id":"saved","network":{"access":"enabled"}}`)
	broaderIntent, _ := sessionCreationRequest(broader, nil)
	if string(broaderIntent) == string(intent) {
		t.Fatal("default erased caller override")
	}
	if err := applyTemplateEnvironment(&broader, lookup.resolved()); err == nil {
		t.Fatal("network broadened")
	}
	narrower := request(`{"type":"openai_hosted","environment_template_id":"saved","network":{"access":"disabled"}}`)
	lookup.network = "enabled"
	if err := applyTemplateEnvironment(&narrower, lookup.resolved()); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"type":"openai_hosted","environment_template_id":null}`, `{"type":"none","environment_template_id":"saved"}`} {
		if _, _, _, err := decodeTemplateEnvironment(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid reference accepted", raw)
		}
	}
}
