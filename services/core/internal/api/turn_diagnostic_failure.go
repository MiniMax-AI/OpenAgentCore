package api

import (
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func turnDiagnosticFailure(turn sessions.Turn) *DiagnosticFailure {
	if turn.Status != sessions.TurnFailed {
		return nil
	}
	var outcome map[string]json.RawMessage
	var coreCode string
	if json.Unmarshal(turn.Outcome, &outcome) == nil {
		_ = json.Unmarshal(outcome["error_code"], &coreCode)
	}
	code := "internal_error"
	params := CoreErrorDetails{}
	switch coreCode {
	case "engine_failed":
		code = "harness_error"
		// Optional malformed metadata cannot hide the authoritative Core error.
		var nativeCode string
		var nativeStatus *int
		_ = json.Unmarshal(outcome["engine_error_code"], &nativeCode)
		_ = json.Unmarshal(outcome["engine_http_status"], &nativeStatus)
		if classified, status := proto.NormalizeEngineFailure(nativeCode, nativeStatus); classified != "" {
			code = classified
			if code == "connection_failed" {
				params["http_status"] = CoreErrorNull()
				if status != nil {
					params["http_status"] = CoreErrorNumber(float64(*status))
				}
			}
		}
	case "model_provider_required":
		code = "model_provider_required"
	case "execution_device_unavailable", "execution_unavailable":
		code = "runtime_unavailable"
	case "device_disconnected", "event_stream_incomplete":
		code = "runtime_disconnected"
	case "preparation_start_failed", "preparation_interrupted":
		code = "runtime_preparation_failed"
	case "execution_interrupted":
		code = "execution_interrupted"
	case "delivery_unknown", "input_outcome_unknown", "cancel_unconfirmed", "cancel_outcome_unavailable", "function_result_unconfirmed":
		code = "delivery_unconfirmed"
	case "invalid_input", "input_not_applied", "message_input_unsupported", "input_invalid_input", "input_run_inactive", "input_input_conflict", "input_input_limit", "input_unsupported", "input_rejected", "input_not_ready", "input_busy":
		code = "input_rejected"
	case "invalid_executor_result", "execution_state_unavailable", "execution_state_changed", "function_call_invalid", "function_result_invalid":
		code = "executor_protocol_error"
	case "event_persistence_failed", "artifact_capture_failed":
		code = "core_storage_failed"
	}
	return &DiagnosticFailure{Code: code, Params: params, FailedAt: diagnosticTime(turn.CompletedAt)}
}

// publicTurnErrors maps the diagnostics code to the official
// SessionTurnErrorCodeResource with a fixed customer-safe sentence: native
// model categories keep their names and a Runtime that disconnected or failed
// its preparation is sandbox_error. runtime_unavailable stays internal_error
// because it also covers Core failing to dispatch the Turn. Native text never
// reaches it.
var publicTurnErrors = map[string]string{
	"context_length_exceeded": "The request exceeds the model's context window.",
	"usage_limit_exceeded":    "The organization has reached a usage, plan, or billing limit.",
	"rate_limit_exceeded":     "The request exceeds the available rate limit.",
	"server_overloaded":       "The model service is temporarily overloaded.",
	"cyber_policy":            "The request was rejected by a safety policy.",
	"connection_failed":       "The request could not connect to the model service.",
	"server_error":            "The model service encountered an unexpected error.",
	"authentication_error":    "The API credentials are invalid or lack the required access.",
	"invalid_request":         "The request contains invalid input or configuration.",
	"resource_not_found":      "The requested model or resource is unavailable.",
	"request_timeout":         "The request timed out before the model service responded.",
	"sandbox_error":           "The request could not complete in its execution environment.",
}

func publicTurnError(diagnosticCode string) *v1.TurnError {
	code := diagnosticCode
	switch diagnosticCode {
	case "runtime_disconnected", "runtime_preparation_failed":
		code = "sandbox_error"
	}
	if message, ok := publicTurnErrors[code]; ok {
		return &v1.TurnError{Code: code, Message: message}
	}
	return &v1.TurnError{Code: "internal_error", Message: "The execution could not complete."}
}
