package v1

import (
	"encoding/json"
	"testing"
)

func TestSessionEventUsageOnlyOnTerminalTurnEvents(t *testing.T) {
	usage := &TokenUsage{InputTokens: 7, InputTokensDetails: InputTokenDetails{CachedTokens: 2}, OutputTokens: 3, OutputTokensDetails: OutputTokenDetails{ReasoningTokens: 1}, TotalTokens: 10}
	measured := `{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":10}`
	for _, test := range []struct {
		event string
		usage *TokenUsage
		want  string
	}{
		{"agent.session.turn.completed", usage, measured},
		{"agent.session.turn.completed", nil, "null"},
		{"agent.session.turn.failed", nil, "null"},
		{"agent.session.turn.cancelled", usage, measured},
		{"agent.session.turn.in_progress", usage, ""},
		{"agent.session.turn.created", nil, ""},
		{"agent.session.idle", usage, ""},
	} {
		raw, err := json.Marshal(SessionEvent{Type: test.event, EventID: "event", TurnID: "turn", Turn: &Turn{ID: "turn", Usage: test.usage}, Usage: test.usage})
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		value, present := fields["usage"]
		if present != (test.want != "") || (present && string(value) != test.want) {
			t.Fatal("unexpected top-level usage", test.event, string(raw))
		}
		if fields["type"] == nil || fields["event_id"] == nil || fields["turn"] == nil || fields["turn_id"] == nil {
			t.Fatal("terminal usage changed the other event fields", string(raw))
		}
		var decoded SessionEvent
		if err := json.Unmarshal(raw, &decoded); err != nil || (present && string(value) != "null" && decoded.Usage == nil) {
			t.Fatal("event did not round-trip", string(raw), err)
		}
	}
}

// Error events carry the pinned SessionError, whose param is present and null
// when unset; Environment state errors keep their observed three fields (HI-01/02).
func TestSessionErrorEventCarriesNullableParam(t *testing.T) {
	failure := &StreamError{Type: "environment_error", Code: "sandbox_error", Message: "Failed to provision environment"}
	param := "input"
	for _, test := range []struct {
		event SessionEvent
		want  string
	}{
		{SessionEvent{Type: "error", EventID: "event", SessionID: "session", Error: failure},
			`{"type":"error","event_id":"event","session_id":"session","error":{"code":"sandbox_error","type":"environment_error","message":"Failed to provision environment","param":null}}`},
		{SessionEvent{Type: "error", EventID: "event", SessionID: "session", Error: &StreamError{Type: "invalid_request_error", Code: "invalid", Message: "m", Param: &param}},
			`{"type":"error","event_id":"event","session_id":"session","error":{"code":"invalid","type":"invalid_request_error","message":"m","param":"input"}}`},
		{SessionEvent{Type: "agent.session.environment.failed", EventID: "event", Environment: &SessionEnvironmentState{ID: "environment", Type: "openai_hosted", Status: "failed",
			Error: &StreamError{Type: "environment_error", Code: "environment_connection_failed", Message: "The environment failed to connect."}}},
			`{"type":"agent.session.environment.failed","event_id":"event","environment":{"id":"environment","type":"openai_hosted","status":"failed","error":{"code":"environment_connection_failed","type":"environment_error","message":"The environment failed to connect."}}}`},
	} {
		raw, err := json.Marshal(test.event)
		if err != nil || string(raw) != test.want {
			t.Fatalf("%s %v", raw, err)
		}
	}
}
