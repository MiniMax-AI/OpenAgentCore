package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestDiagnosticFailureWhitelist(t *testing.T) {
	cases := map[string][]string{
		"harness_error": {"engine_failed"}, "model_provider_required": {"model_provider_required"}, "runtime_unavailable": {"execution_device_unavailable", "execution_unavailable"}, "runtime_disconnected": {"device_disconnected", "event_stream_incomplete"}, "runtime_preparation_failed": {"preparation_start_failed", "preparation_interrupted"}, "execution_interrupted": {"execution_interrupted"},
		"delivery_unconfirmed":    {"delivery_unknown", "input_outcome_unknown", "cancel_unconfirmed", "cancel_outcome_unavailable", "function_result_unconfirmed"},
		"input_rejected":          {"invalid_input", "input_not_applied", "message_input_unsupported", "input_invalid_input", "input_run_inactive", "input_input_conflict", "input_input_limit", "input_unsupported", "input_rejected", "input_not_ready", "input_busy"},
		"executor_protocol_error": {"invalid_executor_result", "interaction_not_supported", "execution_state_unavailable", "execution_state_changed", "function_call_invalid", "function_result_invalid"}, "core_storage_failed": {"event_persistence_failed", "artifact_capture_failed"}, "internal_error": {"secret-canary", "input_secret-canary", "execution_state_secret-canary", ""},
	}
	catalog, err := os.ReadFile("../../../../contracts/agents-api/core-errors.md")
	if err != nil {
		t.Fatal(err)
	}
	for want, inputs := range cases {
		if !strings.Contains(string(catalog), "`"+want+"`") {
			t.Fatal("uncatalogued diagnostics code", want)
		}
		for _, input := range inputs {
			raw, _ := json.Marshal(map[string]string{"error_code": input, "error": "raw-secret-canary"})
			got := turnDiagnosticFailure(store.Turn{Status: store.TurnFailed, Outcome: raw})
			encoded, _ := json.Marshal(got)
			if got.Code != want || strings.Contains(string(encoded), "canary") {
				t.Fatal(input, got)
			}
		}
	}
	if got := turnDiagnosticFailure(store.Turn{Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"engine_failed",`)}); got.Code != "internal_error" {
		t.Fatal("malformed outcome accepted", got)
	}
	for _, status := range []string{store.TurnQueued, store.TurnInProgress, store.TurnWaiting, store.TurnCompleted, store.TurnCancelled} {
		if got := turnDiagnosticFailure(store.Turn{Status: status, Outcome: json.RawMessage(`{"error_code":"engine_failed"}`)}); got != nil {
			t.Fatal("nonfailure classified", status, got)
		}
	}
}

func TestDiagnosticNativeClassification(t *testing.T) {
	codes := []string{"authentication_error", "rate_limit_exceeded", "usage_limit_exceeded", "server_overloaded", "server_error", "invalid_request", "resource_not_found", "request_timeout", "context_length_exceeded", "cyber_policy", "connection_failed"}
	catalog, err := os.ReadFile("../../../../contracts/agents-api/core-errors.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			if !strings.Contains(string(catalog), "`"+code+"`") {
				t.Fatal("uncatalogued code", code)
			}
			for _, status := range []any{nil, 100, 429, 599, 99, 600, 503.5, "503", true, map[string]any{"token": "secret-canary"}} {
				raw, _ := json.Marshal(map[string]any{"error_code": "engine_failed", "error": "secret-canary", "engine_error_code": code, "engine_http_status": status})
				got := turnDiagnosticFailure(store.Turn{Status: store.TurnFailed, Outcome: raw})
				encoded, _ := json.Marshal(got)
				if got.Code != code || strings.Contains(string(encoded), "canary") {
					t.Fatal(string(encoded))
				}
				params, _ := json.Marshal(got.Params)
				want := `{}`
				if code == "connection_failed" {
					want = `{"http_status":null}`
					if n, ok := status.(int); ok && n >= 100 && n <= 599 {
						b, _ := json.Marshal(map[string]int{"http_status": n})
						want = string(b)
					}
				}
				if string(params) != want {
					t.Fatalf("status=%v params=%s want=%s", status, params, want)
				}
			}
		})
	}
	for _, optional := range []string{``, `,"engine_error_code":null`, `,"engine_error_code":17`, `,"engine_error_code":{"code":"authentication_error"}`, `,"engine_error_code":"secret-canary"`, `,"done":{"engine_error_code":"authentication_error"}`, `,"Engine_Error_Code":"authentication_error"`} {
		got := turnDiagnosticFailure(store.Turn{Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"engine_failed"` + optional + `}`)})
		if got.Code != "harness_error" || len(got.Params) != 0 {
			t.Fatal(optional, got)
		}
	}
	for core, want := range map[string]string{"event_persistence_failed": "core_storage_failed", "artifact_capture_failed": "core_storage_failed", "event_stream_incomplete": "runtime_disconnected", "cancel_unconfirmed": "delivery_unconfirmed", "invalid_executor_result": "executor_protocol_error", "secret-canary": "internal_error"} {
		raw, _ := json.Marshal(map[string]any{"error_code": core, "engine_error_code": "connection_failed", "engine_http_status": 503})
		got := turnDiagnosticFailure(store.Turn{Status: store.TurnFailed, Outcome: raw})
		if got.Code != want || len(got.Params) != 0 {
			t.Fatal(got)
		}
	}
	for _, status := range []string{store.TurnQueued, store.TurnInProgress, store.TurnWaiting, store.TurnCompleted, store.TurnCancelled} {
		if got := turnDiagnosticFailure(store.Turn{Status: status, Outcome: json.RawMessage(`{"error_code":"engine_failed","engine_error_code":"authentication_error"}`)}); got != nil {
			t.Fatal(status, got)
		}
	}
}
