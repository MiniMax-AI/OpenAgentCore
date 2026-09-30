package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type turnReadStore struct {
	tenant, sessionID, turnID, cursor string
	limit                             int
	ascending                         bool
	session                           sessions.Session
	turn                              sessions.Turn
}

func (s *turnReadStore) GetSession(_ context.Context, tenant, id string) (sessions.Session, error) {
	s.tenant, s.sessionID = tenant, id
	return s.session, nil
}
func (s *turnReadStore) GetTurn(_ context.Context, tenant, session, id string) (sessions.Turn, error) {
	s.tenant, s.sessionID, s.turnID = tenant, session, id
	return s.turn, nil
}
func (s *turnReadStore) ListTurns(_ context.Context, tenant, session, cursor string, limit int, asc bool) (sessions.TurnPage, error) {
	s.tenant, s.sessionID, s.cursor, s.limit, s.ascending = tenant, session, cursor, limit, asc
	return sessions.TurnPage{Turns: []sessions.Turn{s.turn}, NextCursor: s.turn.ID}, nil
}

func TestTurnRoutesUseAuthenticatedScopeAndSafeProjection(t *testing.T) {
	s := &turnReadStore{session: sessions.Session{Configuration: json.RawMessage(`{"agent":{"id":"agent_snapshot"}}`)}, turn: sessions.Turn{ID: "turn", SessionID: "session", Status: sessions.TurnFailed, CreatedAt: time.Unix(1700000000, 999), Outcome: json.RawMessage(`{"error":"Bearer SECRET","done":{"metadata":{"password":"SECRET"}}}`)}}
	h, _, tenant := testHandler(t, func(_ *Dependencies, f *testFakes) {
		f.sessions.getSession, f.turns.getTurn, f.turns.listTurns = s.GetSession, s.GetTurn, s.ListTurns
	})
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("X-Tenant-ID", "untrusted")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request("/v1/agents/sessions/session/turns/turn")
	if w.Code != 200 || s.tenant != tenant || s.sessionID != "session" || s.turnID != "turn" || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("unsafe response: %d %s", w.Code, w.Body)
	}
	var got v1.Turn
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "agent_snapshot" || got.Error == nil || got.Error.Code != "internal_error" || got.CreatedAt != 1700000000 || got.StartedAt != nil || got.CompletedAt != nil || got.Usage != nil {
		t.Fatalf("bad projection: %+v", got)
	}
	if w := request("/v1/agents/sessions/session/turns?after=last&limit=2&order=asc"); w.Code != 200 || s.cursor != "last" || s.limit != 2 || !s.ascending {
		t.Fatalf("bad list: %d %s", w.Code, w.Body)
	}
	if w := request("/v1/agents/sessions/session/turns"); w.Code != 200 || s.limit != 20 || s.ascending {
		t.Fatalf("bad defaults: %d", w.Code)
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "limit=2&limit=3", "order=random"} {
		if w := request("/v1/agents/sessions/session/turns?" + query); w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"invalid_request_error"`) {
			t.Fatalf("accepted %s: %d %s", query, w.Code, w.Body)
		}
	}
	if w := request("/v1/agents/sessions/session/turns?agent_id=other&tenant_id=other&limit=3"); w.Code != 200 || s.tenant != tenant || s.limit != 3 {
		t.Fatalf("unknown list keys changed the page: %d %s", w.Code, s.tenant)
	}
	if w := request("/v1/agents/sessions/session/turns/turn?unknown=1&tenant_id=other"); w.Code != 200 || s.tenant != tenant || s.turnID != "turn" {
		t.Fatalf("retrieve query was not ignored: %d", w.Code)
	}
	s.session.Configuration = json.RawMessage(`{}`)
	if w := request("/v1/agents/sessions/session/turns/turn"); w.Code != 500 || strings.Contains(w.Body.String(), "snapshot") {
		t.Fatalf("missing identity: %d %s", w.Code, w.Body)
	}
}

func TestTurnProjectionPreservesLifecycle(t *testing.T) {
	session := sessions.Session{Configuration: json.RawMessage(`{"agent":{"id":"agent_snapshot"}}`)}
	for _, status := range []string{sessions.TurnQueued, sessions.TurnInProgress, sessions.TurnWaiting, sessions.TurnCompleted, sessions.TurnFailed, sessions.TurnCancelled} {
		turn := sessions.Turn{Status: status, CreatedAt: time.Unix(1700000000, 0), StartedAt: time.Unix(1700000001, 0), CompletedAt: time.Unix(1700000002, 0)}
		got, err := turnResponse(session, turn)
		if err != nil || got.Status != status || *got.StartedAt != 1700000001 || *got.CompletedAt != 1700000002 || (got.Error != nil) != (status == sessions.TurnFailed) {
			t.Fatalf("lifecycle %s: %+v %v", status, got, err)
		}
	}
}
