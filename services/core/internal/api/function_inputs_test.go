package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func submitResultRequest(t *testing.T, body string, failure error) (*httptest.ResponseRecorder, *inputRecorder) {
	t.Helper()
	recorder := &inputRecorder{err: failure}
	h, _, _ := testHandler(t, recorder.admit)
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/session/events", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-api-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, recorder
}

func TestPublicFunctionResultsPreserveOptionalValues(t *testing.T) {
	for _, fields := range []string{
		`"success":false`, `"success":true,"error":null,"output":null`,
		`"success":false,"error":"","output":""`, `"success":true,"output":[]`,
		`"success":false,"error":"failure","output":[{"type":"input_text","text":""},{"type":"input_image","image_url":"data:image/png;base64,AA=="},{"type":"input_text","text":"last"}]`,
	} {
		body := `{"events":[{"type":"agent.session.input.tool_result","turn_id":"turn","call_id":"call",` + fields + `}]}`
		w, recorder := submitResultRequest(t, body, nil)
		if w.Code != 202 || w.Body.Len() != 0 || len(recorder.inputs) != 1 {
			t.Fatal(w.Code, w.Body, recorder.inputs)
		}
		var input sessions.FunctionResultInput
		if json.Unmarshal(recorder.inputs[0].Payload, &input) != nil || input.TurnID != "turn" || input.CallID != "call" {
			t.Fatal(input)
		}
		var actual, expected map[string]any
		_ = json.Unmarshal(input.Result, &actual)
		_ = json.Unmarshal([]byte(`{`+fields+`}`), &expected)
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(expected)
		if string(a) != string(b) {
			t.Fatalf("got %s want %s", a, b)
		}
	}
}

func TestPublicFunctionResultsRejectMalformedVariantsBeforeAdmission(t *testing.T) {
	prefix := `"type":"agent.session.input.tool_result","turn_id":"turn","call_id":"call"`
	for _, event := range []string{
		`{` + prefix + `}`, `{` + prefix + `,"success":null}`, `{` + prefix + `,"success":"true"}`,
		`{` + prefix + `,"success":true,"input":null}`, `{` + prefix + `,"success":true,"error":{}}`,
		`{` + prefix + `,"success":true,"output":{}}`, `{` + prefix + `,"success":true,"output":false}`,
		`{` + prefix + `,"success":true,"output":[null]}`, `{` + prefix + `,"success":true,"output":[{"type":"input_text"}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_text","text":null}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_text","text":"x","image_url":null}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_image"}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_image","image_url":null}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_image","image_url":"url","text":"x"}]}`,
		`{"type":"agent.session.input.tool_result","turn_id":"turn","success":true}`,
		`{"type":"agent.session.input.tool_result","call_id":"call","success":true}`,
		`{"type":"agent.session.input.cancel","success":null}`, `{"type":"agent.session.input.cancel","input":null}`,
		`{"type":"agent.session.input.message","input":[],"output":null}`,
	} {
		w, recorder := submitResultRequest(t, `{"events":[{"type":"agent.session.input.cancel"},`+event+`]}`, nil)
		if w.Code != 400 || recorder.inputs != nil {
			t.Fatal(event, w.Code, w.Body, recorder.inputs)
		}
	}
}

// Session input admission errors on events.create (EVT-11, EVT-12, ERR-22,
// ERR-27): input conflicts use the official conflict_error fields, result
// targets inside an owned Session are request errors, a missing or foreign
// Session stays not found and Idempotency-Key reuse keeps Core's local code.
func TestPublicInputAdmissionErrorFields(t *testing.T) {
	body := `{"events":[{"type":"agent.session.input.tool_result","turn_id":"turn","call_id":"call","success":true}]}`
	for _, test := range []struct {
		name   string
		err    error
		status int
		want   string
	}{
		{"cancelled_turn", fmt.Errorf("submit turn inputs: %w", sessions.ErrTurnConflict), 409,
			`{"error":{"message":"The Turn cannot accept this input in its current state.","type":"conflict_error","code":"conflict_error","param":null}}`},
		{"pending_input", fmt.Errorf("submit turn inputs: %w", sessions.ErrInputPending), 409,
			`{"error":{"message":"Earlier input to this Session is still pending.","type":"conflict_error","code":"conflict_error","param":null}}`},
		{"changed_result", fmt.Errorf("submit turn inputs: %w", sessions.ErrFunctionResultConflict), 409,
			`{"error":{"message":"The tool call already has a different result.","type":"conflict_error","code":"conflict_error","param":null}}`},
		{"unknown_call", fmt.Errorf("submit turn inputs: %w", sessions.ErrUnknownFunctionCall), 400,
			`{"error":{"message":"Unknown pending tool call.","type":"invalid_request_error","code":"invalid_request_error","param":null}}`},
		{"other_turn", fmt.Errorf("submit turn inputs: %w", sessions.ErrFunctionCallTurnMismatch), 400,
			`{"error":{"message":"The tool call belongs to a different Turn.","type":"invalid_request_error","code":"invalid_request_error","param":null}}`},
		{"missing_session", fmt.Errorf("submit turn inputs: %w", sessions.ErrNotFound), 404,
			`{"error":{"message":"Resource not found.","type":"not_found_error","code":"not_found_error","param":null}}`},
		{"key_reuse", fmt.Errorf("submit turn inputs: %w", sessions.ErrIdempotencyConflict), 409,
			`{"error":{"message":"This idempotency key was used with different input.","type":"conflict_error","code":"idempotency_conflict","param":null}}`},
		{"environment_input_expired", execution.ErrEnvironmentInputExpired, 409,
			`{"error":{"message":"The environment input deadline elapsed before admission.","type":"conflict_error","code":"environment_input_expired","param":null}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, _ := submitResultRequest(t, body, test.err)
			if w.Code != test.status || w.Body.String() != test.want+"\n" {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
}
