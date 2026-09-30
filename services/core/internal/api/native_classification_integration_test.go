package api

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestNativeClassificationPostgresRoundTripAndPublicPrivacy(t *testing.T) {
	s, pool := diagnosticDatabase(t)
	h, _, tenant := adminTestHandler(t, func(h *Handler) { h.store = s })
	for _, code := range []string{"authentication_error", "connection_failed", "secret-canary"} {
		t.Run(code, func(t *testing.T) {
			session, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: identity.Subject{Kind: "service_account", ID: "native-classification"}, Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"id":"agent_root","model":"test"},"environment":{"type":"none"}}`)})
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := s.SubmitMessage(t.Context(), tenant, session.ID, "input", json.RawMessage(`{"text":"test"}`))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.TransitionTurn(t.Context(), tenant, session.ID, receipt.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
				t.Fatal(err)
			}
			status := 503
			result := execution.Result{ErrorCode: "engine_failed", Error: "Bearer secret-canary https://private.example/key", EngineErrorCode: code, EngineHTTPStatus: &status, Done: proto.DonePayload{Usage: proto.Usage{InputTokens: 7, OutputTokens: 3}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-secret-canary"}}}
			outcome, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.TransitionTurn(t.Context(), tenant, session.ID, receipt.TurnID, store.TurnTransition{ExpectedStatus: store.TurnInProgress, Status: store.TurnFailed, Outcome: outcome}); err != nil {
				t.Fatal(err)
			}
			snap, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
			if err != nil {
				t.Fatal(err)
			}
			var saved execution.Result
			if err = json.Unmarshal(snap.Turn.Outcome, &saved); err != nil || saved.EngineErrorCode != code || saved.EngineHTTPStatus == nil || *saved.EngineHTTPStatus != 503 || saved.Done.Usage.InputTokens != 7 || saved.Done.Usage.OutputTokens != 3 || saved.Done.Metadata[proto.DoneMetaAgentSessionID] != "native-secret-canary" {
				t.Fatal("outcome round trip lost facts", err)
			}
			want := code
			if code == "secret-canary" {
				want = "harness_error"
			}
			base := adminSessionsPath + session.ID
			for _, path := range []string{base + "/diagnostics", base + "/turns/" + receipt.TurnID + "/diagnostics"} {
				w := diagnosticRequest(h, path, "Bearer admin")
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"`+want+`"`) || strings.Contains(w.Body.String(), "canary") || strings.Contains(w.Body.String(), "private.example") {
					t.Fatal(w.Code, w.Body)
				}
				if code == "connection_failed" && !strings.Contains(w.Body.String(), `"http_status":503`) {
					t.Fatal(w.Body)
				}
			}
			paths := []string{"/v1/agents/sessions/" + session.ID, "/v1/agents/sessions/" + session.ID + "/turns/" + receipt.TurnID, "/v1/agents/sessions/" + session.ID + "/items"}
			before := make([][]byte, len(paths))
			for i, path := range paths {
				w := diagnosticRequest(h, path, "Bearer caller")
				if w.Code != 200 || strings.Contains(w.Body.String(), "canary") || strings.Contains(w.Body.String(), "engine_error") || strings.Contains(w.Body.String(), "http_status") {
					t.Fatal(w.Code, w.Body)
				}
				before[i] = append([]byte(nil), w.Body.Bytes()...)
			}
			events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			eventBytes, _ := json.Marshal(events)
			if strings.Contains(string(eventBytes), "canary") || strings.Contains(string(eventBytes), "engine_error") || strings.Contains(string(eventBytes), "http_status") {
				t.Fatal("private metadata entered SSE")
			}
			// Remove only additive private fields. All public response bytes must match.
			if _, err = pool.Exec(t.Context(), `UPDATE turns SET outcome=outcome-'engine_error_code'-'engine_http_status' WHERE id=$1`, receipt.TurnID); err != nil {
				t.Fatal(err)
			}
			for i, path := range paths {
				w := diagnosticRequest(h, path, "Bearer caller")
				if w.Code != 200 || !bytes.Equal(before[i], w.Body.Bytes()) {
					t.Fatal("private classification changed public response", path, w.Body)
				}
			}
		})
	}
}
