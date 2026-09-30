package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

type inputRecorder struct {
	ResourceStore
	tenant, session, key string
	inputs               []store.Input
	err                  error
}

func (s *inputRecorder) SubmitInputs(_ context.Context, tenant, session, key string, inputs []store.Input) ([]store.InputReceipt, error) {
	s.tenant, s.session, s.key, s.inputs = tenant, session, key, inputs
	return nil, s.err
}

func TestPublicInputAdmission(t *testing.T) {
	recorder := &inputRecorder{}
	h, _, tenant := testHandler(t, WithExecution(recorder))
	body := `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"First"}]},{"role":"user","content":[{"type":"input_text","text":"Second"}]}]},{"type":"agent.session.input.cancel"}]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/session-id/events", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-api-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "batch-key")
	r.Header.Set("X-Tenant-ID", "forged")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 || w.Body.Len() != 0 || recorder.tenant != tenant || recorder.session != "session-id" || recorder.key != "batch-key" {
		t.Fatalf("response=%d %s recorder=%+v", w.Code, w.Body, recorder)
	}
	if len(recorder.inputs) != 2 || recorder.inputs[0].Kind != "message" || recorder.inputs[1].Kind != "cancel" {
		t.Fatal(recorder.inputs)
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(recorder.inputs[0].Payload, &stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored["input"]), "Second") {
		t.Fatal("individual user messages lost")
	}
}

func TestPublicInputRejectsUnsupportedOrMalformedBatch(t *testing.T) {
	for _, body := range []string{
		`{"events":[{"type":"agent.session.input.message","input":[{"type":null,"role":"user","content":[{"type":"input_text","text":"x"}]}]}]}`,
		`null`, `{}`, `{"events":null}`, `{"events":[{"type":"agent.session.input.tool_result","call_id":"x"}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"assistant","content":[{"type":"input_text","text":"x"}]}]}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/a.png"}]}]}]}`,
		`{"events":[{"type":"agent.session.input.cancel","input":[]}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":""}]}]}]}`,
		`{"events":[{"type":"agent.session.input.cancel"}]} {}`,
	} {
		recorder := &inputRecorder{}
		h, _, _ := testHandler(t, WithExecution(recorder))
		r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/id/events", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || recorder.inputs != nil {
			t.Fatalf("accepted %s: %d %s", body, w.Code, w.Body)
		}
	}
}

// Whitespace-only text is content (SES-03/04); it is stored without trimming.
// Empty text, content and input keep today's rejection and error fields.
func TestPublicInputWhitespaceTextAdmission(t *testing.T) {
	for _, input := range []string{
		`[{"role":"user","content":[{"type":"input_text","text":"   "}]}]`,
		`[{"role":"user","content":[{"type":"input_text","text":"\n\t"}]}]`,
		`[{"role":"user","content":[{"type":"input_text","text":"   "}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"\n\t"}]}]`,
		`[{"role":"user","content":[{"type":"input_text","text":"   "}]},{"role":"user","content":[{"type":"input_text","text":"Reply only OK."}]}]`,
		// SES-08 is unknown officially; an empty part beside text keeps today's acceptance.
		`[{"role":"user","content":[{"type":"input_text","text":""},{"type":"input_text","text":"Reply only OK."}]}]`,
	} {
		recorder := &inputRecorder{}
		h, _, _ := testHandler(t, WithExecution(recorder))
		body := `{"events":[{"type":"agent.session.input.message","input":` + input + `}]}`
		r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/session-id/events", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 202 || w.Body.Len() != 0 || len(recorder.inputs) != 1 || recorder.inputs[0].Kind != "message" {
			t.Fatalf("%s: %d %s %+v", input, w.Code, w.Body, recorder.inputs)
		}
		var stored struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(recorder.inputs[0].Payload, &stored); err != nil || !jsonEqual(t, stored.Input, input) {
			t.Fatalf("stored %s, want %s", stored.Input, input)
		}
	}
	for _, body := range []string{
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":""}]}]}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":""},{"type":"input_text","text":""}]}]}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":" "}]},{"role":"user","content":[{"type":"input_text","text":""}]}]}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[]}]}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[]}]}`,
	} {
		recorder := &inputRecorder{}
		h, _, _ := testHandler(t, WithExecution(recorder))
		r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/session-id/events", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || recorder.inputs != nil || w.Body.String() != emptyInputError {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
}

// emptyInputError is the unchanged response for empty text, content and input.
const emptyInputError = `{"error":{"message":"Invalid resource identifier or request limits.","type":"invalid_request_error","code":"invalid_request","param":null}}` + "\n"

func jsonEqual(t *testing.T, actual json.RawMessage, expected string) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal(actual, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &right); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(left, right)
}
