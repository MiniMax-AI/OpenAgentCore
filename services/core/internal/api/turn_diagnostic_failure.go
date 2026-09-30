package api

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func turnDiagnosticFailure(turn store.Turn) *DiagnosticFailure {
	if turn.Status != store.TurnFailed {
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
	case "invalid_executor_result", "interaction_not_supported", "execution_state_unavailable", "execution_state_changed", "function_call_invalid", "function_result_invalid":
		code = "executor_protocol_error"
	case "event_persistence_failed", "artifact_capture_failed":
		code = "core_storage_failed"
	}
	return &DiagnosticFailure{Code: code, Params: params, FailedAt: diagnosticTime(turn.CompletedAt)}
}
