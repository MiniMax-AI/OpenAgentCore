package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestDiagnosticsCoreHandlerDatabaseBoundary(t *testing.T) {
	s, pool := diagnosticDatabase(t)
	h, _, tenant := adminTestHandler(t, func(h *Handler) { h.store = s })
	session, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: identity.Subject{Kind: "service_account", ID: "diagnostic-test"}, Engine: "codex", IdempotencyKey: "diagnostics", Configuration: json.RawMessage(`{"agent":{"id":"agent_root","model":"test"},"environment":{"type":"none"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.SubmitMessage(t.Context(), tenant, session.ID, "input", json.RawMessage(`{"text":"input-secret-canary"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionTurn(t.Context(), tenant, session.ID, receipt.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionTurn(t.Context(), tenant, session.ID, receipt.TurnID, store.TurnTransition{ExpectedStatus: store.TurnInProgress, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"device_disconnected","error":"Bearer raw-secret-canary https://private.example/key","done":{"native_id":"secret-native-canary"}}`)}); err != nil {
		t.Fatal(err)
	}
	base := adminSessionsPath + session.ID
	for _, path := range []string{base + "/diagnostics", base + "/turns/" + receipt.TurnID + "/diagnostics"} {
		if w := diagnosticRequest(h, path, ""); w.Code != 401 {
			t.Fatal("unauthed diagnostics", w.Code, w.Body)
		}
		if w := diagnosticRequest(h, path, "Bearer caller"); w.Code != 401 {
			t.Fatal("Project credential entered Core route", w.Code, w.Body)
		}
		w := diagnosticRequest(h, path, "Bearer admin")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"code":"runtime_disconnected"`) || strings.Contains(w.Body.String(), "canary") || strings.Contains(w.Body.String(), "private.example") {
			t.Fatal(w.Code, w.Body)
		}
	}
	missing := diagnosticRequest(h, base+"/turns/"+uuid.NewString()+"/diagnostics", "Bearer admin")
	malformed := diagnosticRequest(h, base+"/turns/malformed/diagnostics", "Bearer admin")
	if missing.Code != 404 || malformed.Code != 404 || missing.Body.String() != malformed.Body.String() {
		t.Fatal("missing path semantics changed", missing.Body, malformed.Body)
	}
	foreign := diagnosticRequest(h, strings.Replace(base, managementProjectID, uuid.NewString(), 1)+"/diagnostics", "Bearer admin")
	if foreign.Code != 404 {
		t.Fatal("foreign project visible", foreign.Code)
	}
	if _, err = pool.Exec(t.Context(), "UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base + "/diagnostics", base + "/turns/" + receipt.TurnID + "/diagnostics"} {
		if w := diagnosticRequest(h, path, "Bearer admin"); w.Code != 404 {
			t.Fatal("deleted root visible", w.Code, w.Body)
		}
	}
}

type diagnosticSnapshotStore struct {
	ResourceStore
	session store.Session
}

func (s diagnosticSnapshotStore) GetSessionDiagnosticsSnapshot(context.Context, string, string) (store.Session, error) {
	return s.session, nil
}
func (s diagnosticSnapshotStore) GetTurnDiagnosticsSnapshot(context.Context, string, string, string) (store.TurnDiagnosticsSnapshot, error) {
	return store.TurnDiagnosticsSnapshot{Session: s.session, Turn: *s.session.LastTurn, Items: []store.ItemDiagnosticTiming{}}, nil
}

func TestDiagnosticsFailurePrecedenceAndUnknownTime(t *testing.T) {
	id, turnID := uuid.NewString(), uuid.NewString()
	base := store.Session{ID: id, Configuration: json.RawMessage(`{"agent":{"id":"agent_root","model":"test"},"environment":{"type":"none"}}`), LastTurn: &store.Turn{ID: turnID, SessionID: id, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"engine_failed","error":"secret-canary"}`)}}
	for _, tc := range []struct {
		activity     *store.EnvironmentInputActivity
		code, source string
	}{{nil, "harness_error", "turn"}, {&store.EnvironmentInputActivity{Status: "failed"}, "environment_connection_timeout", "environment_input"}, {&store.EnvironmentInputActivity{Status: "failed", Failure: "model_provider_required"}, "model_provider_required", "environment_input"}, {&store.EnvironmentInputActivity{Status: "failed", Failure: "runtime_preparation_failed"}, "runtime_preparation_failed", "environment_input"}, {&store.EnvironmentInputActivity{Status: "failed", Failure: "secret-canary"}, "internal_error", "environment_input"}} {
		value := base
		value.EnvironmentInputActivity = tc.activity
		h, _, _ := adminTestHandler(t, func(h *Handler) { h.store = diagnosticSnapshotStore{session: value} })
		w := diagnosticRequest(h, adminSessionsPath+id+"/diagnostics", "Bearer admin")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || !strings.Contains(w.Body.String(), `"source":"`+tc.source+`"`) || !strings.Contains(w.Body.String(), `"failed_at":null`) || strings.Contains(w.Body.String(), "canary") {
			t.Fatal(w.Code, w.Body)
		}
	}
	base.EnvironmentInputActivity = &store.EnvironmentInputActivity{Status: "idle", LastActiveAt: time.Now()}
	h, _, _ := adminTestHandler(t, func(h *Handler) { h.store = diagnosticSnapshotStore{session: base} })
	if w := diagnosticRequest(h, adminSessionsPath+id+"/diagnostics", "Bearer admin"); w.Code != 200 || !strings.Contains(w.Body.String(), `"failure":null`) {
		t.Fatal("public activity precedence changed", w.Code, w.Body)
	}
}

func TestDiagnosticsHostedFailureOverridesInputWithoutParsingReason(t *testing.T) {
	session := hostedFailureSession()
	session.EnvironmentInputActivity = &store.EnvironmentInputActivity{Status: "failed", Failure: "environment_unavailable", LastActiveAt: time.Now()}
	session.LastTurn = &store.Turn{ID: uuid.NewString(), SessionID: session.ID, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"engine_failed"}`)}
	step, index, exit := "setup", 2, 7
	for _, detail := range []*store.ProvisioningFailureDetail{nil, {Step: &step, Index: &index, ExitCode: &exit}} {
		session.EnvironmentFailure = &store.EnvironmentFailure{Reason: "private-secret-canary setup_commands[99] exit 254", Detail: detail}
		h, _, _ := adminTestHandler(t, func(h *Handler) { h.store = diagnosticSnapshotStore{session: session} })
		w := diagnosticRequest(h, adminSessionsPath+session.ID+"/diagnostics", "Bearer admin")
		if w.Code != 200 || strings.Contains(w.Body.String(), "canary") || !strings.Contains(w.Body.String(), `"code":"environment_provisioning_failed"`) || !strings.Contains(w.Body.String(), `"source":"environment"`) {
			t.Fatal(w.Code, w.Body)
		}
		want := `"params":{"exit_code":null,"index":null,"step":null}`
		if detail != nil {
			want = `"params":{"exit_code":7,"index":2,"step":"setup"}`
		}
		if !strings.Contains(w.Body.String(), want) || !strings.Contains(w.Body.String(), `"failed_at":null`) {
			t.Fatal("historical reason parsed or time invented", w.Body)
		}
	}
}
