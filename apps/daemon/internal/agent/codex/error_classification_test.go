package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestPinnedTerminalErrorClassification(t *testing.T) {
	for value, want := range map[string]string{
		"unauthorized": "authentication_error", "usageLimitExceeded": "usage_limit_exceeded", "rateLimitExceeded": "rate_limit_exceeded",
		"contextWindowExceeded": "context_length_exceeded", "serverOverloaded": "server_overloaded", "internalServerError": "server_error",
		"badRequest": "invalid_request", "cyberPolicy": "cyber_policy", "sessionBudgetExceeded": "", "misalignmentPolicyViolation": "",
		"threadRollbackFailed": "", "sandboxError": "", "other": "", "unknownFuture": "",
	} {
		raw, _ := json.Marshal(value)
		got := classifyTurnError(&TurnError{Message: "unauthorized 429 timeout", CodexErrorInfo: raw})
		if got.Code != want || got.HTTPStatus != nil {
			t.Fatalf("%s: %+v", value, got)
		}
	}
	for _, tc := range []struct {
		raw, code string
		status    int
	}{
		{`{"httpConnectionFailed":{"httpStatusCode":401}}`, "connection_failed", 401},
		{`{"responseStreamConnectionFailed":{"httpStatusCode":503}}`, "connection_failed", 503},
		{`{"responseStreamDisconnected":{"httpStatusCode":null}}`, "connection_failed", 0},
		{`{"responseTooManyFailedAttempts":{"httpStatusCode":429}}`, "rate_limit_exceeded", 0},
		{`{"responseTooManyFailedAttempts":{"httpStatusCode":502}}`, "connection_failed", 502},
		{`{"httpConnectionFailed":{"httpStatusCode":"401"}}`, "connection_failed", 0},
		{`{"httpConnectionFailed":{"httpStatusCode":65535}}`, "connection_failed", 0},
		{`{"httpConnectionFailed":null}`, "", 0}, {`{"activeTurnNotSteerable":{"turnKind":"review"}}`, "", 0},
		{`{"httpConnectionFailed":{},"other":{}}`, "", 0}, {`null`, "", 0},
	} {
		got := classifyTurnError(&TurnError{CodexErrorInfo: json.RawMessage(tc.raw)})
		status := 0
		if got.HTTPStatus != nil {
			status = *got.HTTPStatus
		}
		if got.Code != tc.code || status != tc.status {
			t.Fatalf("%s: %+v", tc.raw, got)
		}
	}
}

func TestClassificationOnlyFromRootTerminalError(t *testing.T) {
	for _, mode := range []string{"failed", "recovered", "notification-only"} {
		t.Run(mode, func(t *testing.T) {
			out := make(chan proto.Envelope, 16)
			s := &Session{runID: "run", out: out, cancelCtx: context.Background(), cfg: defaultSessionConfig(), rpc: NewJSONRPCClient(JSONRPCConfig{})}
			s.registerHandlers()
			s.setThreadID("root")
			scopeNotification(t, s, "turn/started", `{"threadId":"root","turn":{"id":"turn"}}`)
			scopeNotification(t, s, "error", `{"threadId":"root","turnId":"turn","error":{"message":"retry","codexErrorInfo":"unauthorized"},"willRetry":true}`)
			scopeNotification(t, s, "turn/completed", `{"threadId":"child","turn":{"id":"turn","status":"failed","error":{"message":"child","codexErrorInfo":"usageLimitExceeded"}}}`)
			status, errJSON := "failed", `{"message":"terminal","codexErrorInfo":"serverOverloaded"}`
			if mode == "recovered" {
				status = "completed"
			}
			if mode == "notification-only" {
				errJSON = "null"
			}
			scopeNotification(t, s, "turn/completed", `{"threadId":"root","turn":{"id":"turn","status":"`+status+`","usage":{"inputTokens":7,"outputTokens":2},"error":`+errJSON+`}}`)
			errors, done := 0, false
			for env := range out {
				if env.Type == proto.TypeError {
					errors++
					var p proto.ErrorPayload
					_ = env.DecodePayload(&p)
					want := ""
					if mode == "failed" {
						want = "server_overloaded"
					}
					if p.Code != want {
						t.Fatal(p)
					}
				}
				if env.Type == proto.TypeDone {
					done = true
					var p proto.DonePayload
					_ = env.DecodePayload(&p)
					if p.Metadata[proto.DoneMetaAgentSessionID] != "root" || p.Usage.InputTokens != 7 {
						t.Fatal(p)
					}
				}
			}
			if !done || errors != map[bool]int{true: 0, false: 1}[mode == "recovered"] {
				t.Fatal("terminal lost", done, errors)
			}
		})
	}
}
