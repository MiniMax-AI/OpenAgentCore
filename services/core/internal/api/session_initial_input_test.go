package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// admitInto enables Execution whose Worker admits Session creation into s,
// apart from the store that creates Sessions without execution work. Input
// submission stays unexpected.
func admitInto(s *recordingStore) func(*Dependencies, *testFakes) {
	return func(d *Dependencies, f *testFakes) {
		d.Execution = f.execution()
		f.sessionAdmission.createSession = s.CreateSession
	}
}

func TestInitialInputUsesExecutionAdmissionAndSharedMessageValidation(t *testing.T) {
	for _, value := range []string{`"First"`, `[{"role":"user","content":[{"type":"input_text","text":"First"}]}]`} {
		execution := &recordingStore{}
		h, idle, tenant := testHandler(t, admitInto(execution))
		r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"test-model"},"environment":{"type":"none"},"input":`+value+`}`))
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "create-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 201 || idle.tenant != "" || execution.tenant != tenant || execution.input.IdempotencyKey != "create-key" || len(execution.input.InitialInputs) != 1 {
			t.Fatal(w.Code, w.Body, execution.input)
		}
		expected, err := executionInputs([]json.RawMessage{json.RawMessage(`{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"First"}]}]}`)})
		if err != nil || string(expected[0].Payload) != string(execution.input.InitialInputs[0].Payload) {
			t.Fatal(execution.input, err)
		}
	}
	for _, value := range []string{`[{"type":null,"role":"user","content":[{"type":"input_text","text":"x"}]}]`, `[{"type":"","role":"user","content":[{"type":"input_text","text":"x"}]}]`, `0`, `true`, `{}`, `[]`, `""`, `[{"role":"user","content":[]}]`, `[{"role":"user","content":[{"type":"input_text","text":""}]}]`, `[{"role":"user","unknown":true,"content":[{"type":"input_text","text":"x"}]}]`, `[{"role":"user","content":[{"type":"input_text","text":"x","unknown":true}]}]`, `[{"role":"assistant","content":[{"type":"input_text","text":"x"}]}]`} {
		if _, err := initialSessionInputs(json.RawMessage(value)); err == nil {
			t.Fatal("invalid initial input accepted", value)
		}
	}
	for _, value := range []json.RawMessage{nil, json.RawMessage(` null `)} {
		if got, err := initialSessionInputs(value); err != nil || got != nil {
			t.Fatal(got, err)
		}
	}
}

// W1/W2: whitespace-only initial text creates the Session and keeps its exact
// text; the empty string, empty content and empty input keep today's 400.
func TestInitialInputAdmitsWhitespaceTextVerbatim(t *testing.T) {
	for _, value := range []struct{ input, stored string }{
		{`"   "`, `[{"role":"user","content":[{"type":"input_text","text":"   "}]}]`},
		{`"\n\t"`, `[{"role":"user","content":[{"type":"input_text","text":"\n\t"}]}]`},
		{`[{"role":"user","content":[{"type":"input_text","text":"\n\t"}]}]`, `[{"role":"user","content":[{"type":"input_text","text":"\n\t"}]}]`},
		{`[{"type":"message","role":"user","content":[{"type":"input_text","text":"   "}]},{"role":"user","content":[{"type":"input_text","text":" \t\n "}]}]`, `[{"type":"message","role":"user","content":[{"type":"input_text","text":"   "}]},{"role":"user","content":[{"type":"input_text","text":" \t\n "}]}]`},
	} {
		execution := &recordingStore{}
		h, _, _ := testHandler(t, admitInto(execution))
		w := createWithInput(h, value.input)
		if w.Code != 201 || len(execution.input.InitialInputs) != 1 {
			t.Fatalf("%s: %d %s", value.input, w.Code, w.Body)
		}
		var stored struct {
			Type  string          `json:"type"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(execution.input.InitialInputs[0].Payload, &stored); err != nil || stored.Type != "agent.session.input.message" || !jsonEqual(t, stored.Input, value.stored) {
			t.Fatalf("%s stored %s", value.input, execution.input.InitialInputs[0].Payload)
		}
	}
	for _, value := range []string{`""`, `[]`, `[{"role":"user","content":[]}]`, `[{"role":"user","content":[{"type":"input_text","text":""}]}]`} {
		execution := &recordingStore{}
		h, _, _ := testHandler(t, admitInto(execution))
		w := createWithInput(h, value)
		if w.Code != 400 || w.Body.String() != emptyInputError || execution.tenant != "" {
			t.Fatalf("%s: %d %s", value, w.Code, w.Body)
		}
	}
}

func createWithInput(h http.Handler, input string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"test-model"},"environment":{"type":"none"},"input":`+input+`}`))
	r.Header.Set("Authorization", "Bearer test-api-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
