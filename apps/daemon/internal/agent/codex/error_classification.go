package codex

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Only the exact root terminal TurnError is authoritative. Error notifications,
// including exhausted/recovered retries, never become a sticky classification.
func classifyTurnError(failure *TurnError) proto.ErrorPayload {
	if failure == nil {
		return proto.ErrorPayload{}
	}
	var variant string
	var code string
	if json.Unmarshal(failure.CodexErrorInfo, &variant) == nil {
		switch variant {
		case "unauthorized":
			code = "authentication_error"
		case "usageLimitExceeded":
			code = "usage_limit_exceeded"
		case "rateLimitExceeded":
			code = "rate_limit_exceeded"
		case "contextWindowExceeded":
			code = "context_length_exceeded"
		case "serverOverloaded":
			code = "server_overloaded"
		case "internalServerError":
			code = "server_error"
		case "badRequest":
			code = "invalid_request"
		case "cyberPolicy":
			code = "cyber_policy"
		}
		return proto.ErrorPayload{Code: code}
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(failure.CodexErrorInfo, &object) != nil || len(object) != 1 {
		return proto.ErrorPayload{}
	}
	for variant, raw := range object {
		switch variant {
		case "httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts":
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || fields == nil {
				return proto.ErrorPayload{}
			}
			var status *int
			_ = json.Unmarshal(fields["httpStatusCode"], &status)
			code = "connection_failed"
			if variant == "responseTooManyFailedAttempts" && status != nil && *status == 429 {
				code = "rate_limit_exceeded"
			}
			code, status = proto.NormalizeEngineFailure(code, status)
			return proto.ErrorPayload{Code: code, HTTPStatus: status}
		}
	}
	return proto.ErrorPayload{}
}
