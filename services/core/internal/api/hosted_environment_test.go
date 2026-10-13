package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestHostedEnvironmentDefaultsAndExplicitGaps(t *testing.T) {
	for _, raw := range []string{
		`{"type":"openai_hosted"}`,
		`{"type":"openai_hosted","network":null,"env":null,"files":null,"packages":null,"plugins":null,"skills":null,"setup_commands":null,"capability_directories":null}`,
		`{"type":"openai_hosted","network":{"access":"enabled","allowed_domains":null},"env":{},"files":[],"packages":{"npm":[],"python":null},"plugins":[],"skills":[],"setup_commands":[],"capability_directories":[]}`,
	} {
		got, err := decodeSessionEnvironment(json.RawMessage(raw))
		if err != nil || got.Network == nil || got.Network.Access != "enabled" {
			t.Fatal("hosted defaults changed", got, err)
		}
	}
	got, err := decodeSessionEnvironment(json.RawMessage(`{"type":"openai_hosted","network":{"access":"disabled"}}`))
	if err != nil || got.Network.Access != "disabled" {
		t.Fatal("explicit policy changed", got, err)
	}
	for _, field := range []string{
		`"network":{}`, `"network":{"access":null}`, `"network":{"access":"restricted"}`,
		`"network":{"access":"disabled","allowed_domains":["example.com"]}`, `"network":{"access":"enabled","unknown":true}`,
		`"env":{"SECRET":null}`, `"files":[{}]`, `"packages":{"system":[null]}`,
		`"packages":{"unknown":[]}`, `"plugins":[{}]`, `"skills":[{}]`, `"setup_commands":["echo test"]`,
		`"capability_directories":["/private"]`, `"template_id":"template"`, `"workspace_directory":"/workspace"`,
		`"files":{}`, `"env":[]`, `"packages":[]`, `"network":[]`, `"unknown":null`,
	} {
		if _, err := decodeSessionEnvironment(json.RawMessage(`{"type":"openai_hosted",` + field + `}`)); err == nil {
			t.Fatal("unsupported installation or invalid input accepted", field)
		}
	}
}

// The pinned openai_hosted input has no environment_id, so a request naming an
// existing Environment gets the route's existing error instead of a new
// Environment, with or without a Template.
func TestSessionCreationRejectsExistingEnvironmentReference(t *testing.T) {
	h, s := validationHandler(t)
	rejected := `{"error":{"message":"Request fields have invalid types or null values.","type":"invalid_request_error","code":"invalid_request","param":null}}` + "\n"
	for _, environment := range []string{
		`{"type":"openai_hosted","environment_id":"` + uuid.NewString() + `"}`,
		`{"type":"openai_hosted","environment_id":"` + uuid.NewString() + `","environment_template_id":"` + uuid.NewString() + `"}`,
	} {
		body := `{"agent":{"model":"m"},"environment":` + environment + `,"input":"hi"}`
		if w := bodyGateRequest(h, "/v1/agents/sessions", "application/json", []byte(body)); w.Code != http.StatusBadRequest || w.Body.String() != rejected {
			t.Errorf("%s: %d %s", environment, w.Code, w.Body)
		}
	}
	if s.writes != 0 {
		t.Fatalf("an Environment reference reached storage: %d writes", s.writes)
	}
}

func TestHostedEnvironmentResponseHasPinnedShapeAndNoConnectionAction(t *testing.T) {
	s := environmentSession()
	s.Configuration = json.RawMessage(`{"agent":{"id":"agent","model":"model"},"environment":{"type":"openai_hosted"}}`)
	s.Environment.Configuration = json.RawMessage(`{"type":"openai_hosted"}`)
	s.EnvironmentInputActivity = nil
	result, err := sessionResponse(s, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "idle" || len(result.RequiredActions) != 0 {
		t.Fatal("hosted preparation requires a caller connection", result)
	}
	raw, err := json.Marshal(result.Environment)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(raw, &got) != nil {
		t.Fatal("invalid environment JSON")
	}
	expected := map[string]any{"id": "environment", "type": "openai_hosted", "capability_directories": []any{}, "files": []any{}, "plugins": []any{}, "skills": []any{}, "packages": map[string]any{"npm": []any{}, "python": []any{}, "system": []any{}}, "network": map[string]any{"access": "enabled", "allowed_domains": []any{}}}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("hosted response shape changed", string(raw))
	}
	for _, mutate := range []func(*sessions.Environment){
		func(e *sessions.Environment) { e.TenantID = "foreign" },
		func(e *sessions.Environment) { e.SessionID = "other" },
		func(e *sessions.Environment) {
			e.Configuration = json.RawMessage(`{"type":"self_hosted","workspace_directory":"/workspace"}`)
		},
	} {
		invalid := s
		environment := *s.Environment
		invalid.Environment = &environment
		mutate(invalid.Environment)
		if _, err := sessionResponse(invalid, ""); err == nil {
			t.Fatal("hosted association mismatch accepted")
		}
	}
}

// The execution owner must see idle creation as well as initial-input creation.
// Resource persistence alone cannot validate the configured managed deployment.
func TestHostedCreationUsesExecutionAdmission(t *testing.T) {
	for _, stream := range []bool{false, true} {
		recorder := &hostedCreationRecorder{}
		handler, fixture := environmentCreationHandler(t, "codex", func(d *Dependencies, f *testFakes) {
			f.sessionAdmission.createSession = recorder.CreateSession
		})
		input := ""
		if stream {
			input = `,"input":"Initialize the streamed hosted execution."`
		}
		body := fmt.Sprintf(`{"agent":{"model":"model"},"environment":{"type":"openai_hosted"},"stream":%t%s}`, stream, input)
		request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer key")
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		// The recorder deliberately rejects creation. Its rejection proves
		// admission was used; the resource fixture must remain untouched.
		if recorder.calls != 1 || fixture.input.Engine != "" || fixture.session.ID != "" || response.Code != http.StatusBadRequest {
			t.Fatal("hosted creation bypassed execution admission", stream, response.Code, response.Body.String())
		}
	}
}

// hostedCreationRecorder is the Worker's hosted admission; it counts
// creations.
type hostedCreationRecorder struct {
	calls int
}

func (r *hostedCreationRecorder) CreateSession(context.Context, string, sessions.CreateSession) (sessions.Creation, error) {
	r.calls++
	return sessions.Creation{}, sessions.ErrInvalidInput
}
