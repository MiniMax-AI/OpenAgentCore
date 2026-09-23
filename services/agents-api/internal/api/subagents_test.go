package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type subagentReadStore struct {
	method, tenant, session, subagent, turn, after string
	limit                                          int
	ascending                                      bool
	calls                                          int
	empty                                          bool
	err                                            error
}

func (s *subagentReadStore) record(method, tenant, session, subagent, turn, after string, limit int, ascending bool) {
	s.method, s.tenant, s.session, s.subagent, s.turn, s.after = method, tenant, session, subagent, turn, after
	s.limit, s.ascending = limit, ascending
	s.calls++
}

func sampleSubagent() v1.Subagent {
	return v1.Subagent{ID: "child", Object: "agent.session.subagent", SessionID: "session", ParentAgentID: "root", OpenedAt: 1700000000, Status: "active"}
}

func sampleSubagentTurn() v1.Turn {
	id := "child"
	return v1.Turn{ID: "turn", AgentID: id, SubagentID: &id, SessionID: "session", Object: "agent.session.turn", Status: "completed", CreatedAt: 1700000001}
}

func (s *subagentReadStore) GetSubagent(_ context.Context, tenant, session, subagent string) (v1.Subagent, error) {
	s.record("get", tenant, session, subagent, "", "", 0, false)
	return sampleSubagent(), s.err
}

func (s *subagentReadStore) ListSubagents(_ context.Context, tenant, session, after string, limit int, asc bool) (v1.SubagentList, error) {
	s.record("list", tenant, session, "", "", after, limit, asc)
	if s.empty {
		return v1.SubagentList{}, s.err
	}
	return v1.SubagentList{Data: []v1.Subagent{sampleSubagent()}, HasMore: true}, s.err
}

func (s *subagentReadStore) ListSubagentItems(_ context.Context, tenant, session, subagent, after string, limit int, asc bool) (v1.ItemList, error) {
	s.record("items", tenant, session, subagent, "", after, limit, asc)
	return s.items(), s.err
}

func (s *subagentReadStore) GetSubagentTurn(_ context.Context, tenant, session, subagent, turn string) (v1.Turn, error) {
	s.record("turn", tenant, session, subagent, turn, "", 0, false)
	return sampleSubagentTurn(), s.err
}

func (s *subagentReadStore) ListSubagentTurns(_ context.Context, tenant, session, subagent, after string, limit int, asc bool) (v1.TurnList, error) {
	s.record("turns", tenant, session, subagent, "", after, limit, asc)
	if s.empty {
		return v1.TurnList{}, s.err
	}
	return v1.TurnList{Data: []v1.Turn{sampleSubagentTurn()}, HasMore: true}, s.err
}

func (s *subagentReadStore) ListSubagentTurnItems(_ context.Context, tenant, session, subagent, turn, after string, limit int, asc bool) (v1.ItemList, error) {
	s.record("turn_items", tenant, session, subagent, turn, after, limit, asc)
	return s.items(), s.err
}

func (s *subagentReadStore) items() v1.ItemList {
	if s.empty {
		return v1.ItemList{}
	}
	text := "Child result."
	return v1.ItemList{Data: []v1.Item{{ID: "item", TurnID: "turn", Type: "agent_message", SenderAgentID: "child", RecipientAgentID: "root", Content: []v1.ItemContent{{Type: "output_text", Text: &text}}}}, HasMore: true}
}

var subagentRoutes = []struct {
	path, method, subagent, turn string
	list                         bool
}{
	{"", "list", "", "", true},
	{"/child", "get", "child", "", false},
	{"/child/items", "items", "child", "", true},
	{"/child/turns", "turns", "child", "", true},
	{"/child/turns/turn", "turn", "child", "turn", false},
	{"/child/turns/turn/items", "turn_items", "child", "turn", true},
}

func requestSubagents(h http.Handler, path, auth, beta string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/v1/agents/sessions/session/subagents"+path, nil)
	r.Header.Set("Authorization", auth)
	r.Header.Set("OpenAI-Beta", beta)
	r.Header.Set("X-Tenant-ID", "untrusted")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSubagentRoutesPreserveAuthenticatedParentScope(t *testing.T) {
	s := &subagentReadStore{}
	h, _, tenant := testHandler(t, WithSubagents(s))
	for _, route := range subagentRoutes {
		t.Run(route.method, func(t *testing.T) {
			w := requestSubagents(h, route.path, "Bearer test-api-key", "agents=v1")
			if w.Code != http.StatusOK || s.method != route.method || s.tenant != tenant || s.session != "session" || s.subagent != route.subagent || s.turn != route.turn {
				t.Fatalf("scope: %d %s; call=%+v", w.Code, w.Body, s)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if route.list {
				if s.limit != 20 || s.ascending || s.after != "" || string(body["has_more"]) != "true" {
					t.Fatalf("list defaults: %+v %s", s, w.Body)
				}
				w = requestSubagents(h, route.path+"?after=last&limit=2&order=asc", "Bearer test-api-key", "agents=v1")
				if w.Code != http.StatusOK || s.after != "last" || s.limit != 2 || !s.ascending {
					t.Fatalf("pagination: %+v %s", s, w.Body)
				}
			} else if route.method == "get" {
				for _, field := range []string{"closed_at", "name", "instructions"} {
					if string(body[field]) != "null" {
						t.Fatalf("%s missing null: %s", field, w.Body)
					}
				}
			} else if string(body["agent_id"]) != `"child"` || string(body["subagent_id"]) != `"child"` || string(body["usage"]) != "null" {
				t.Fatalf("child Turn identity: %s", w.Body)
			}
		})
	}
}

func TestSubagentRoutesRejectInvalidQueriesBeforeStore(t *testing.T) {
	s := &subagentReadStore{}
	h, _, _ := testHandler(t, WithSubagents(s))
	for _, route := range subagentRoutes {
		queries := []string{"unknown=1", "tenant_id=foreign"}
		if route.list {
			queries = append(queries, "limit=0", "limit=101", "limit=null", "limit=2&limit=3", "order=random", "order=asc&order=desc", "after=a&after=b")
		} else {
			queries = append(queries, "limit=1", "after=a", "order=asc")
		}
		for _, query := range queries {
			calls := s.calls
			w := requestSubagents(h, route.path+"?"+query, "Bearer test-api-key", "agents=v1")
			if w.Code != http.StatusBadRequest || s.calls != calls {
				t.Fatalf("accepted %s?%s: %d %s", route.path, query, w.Code, w.Body)
			}
		}
	}
}

func TestSubagentRoutesUseExistingAuthenticationAndErrors(t *testing.T) {
	s := &subagentReadStore{}
	h, _, _ := testHandler(t, WithSubagents(s))
	for _, route := range subagentRoutes {
		for _, tc := range []struct {
			auth, beta string
			status     int
		}{
			{"", "agents=v1", 401},
			{"Bearer wrong", "agents=v1", 401},
			{"Bearer test-api-key", "", 400},
			{"Bearer test-api-key", "agents=v2", 400},
		} {
			calls := s.calls
			w := requestSubagents(h, route.path, tc.auth, tc.beta)
			if w.Code != tc.status || s.calls != calls {
				t.Fatalf("authentication %s: %d %s", route.path, w.Code, w.Body)
			}
		}
		for _, tc := range []struct {
			err    error
			status int
		}{
			{store.ErrNotFound, 404},
			{store.ErrInvalidInput, 400},
			{errors.New("SECRET native failure"), 500},
		} {
			s.err = tc.err
			w := requestSubagents(h, route.path, "Bearer test-api-key", "agents=v1")
			if w.Code != tc.status || strings.Contains(w.Body.String(), "SECRET") {
				t.Fatalf("error %s: %d %s", route.path, w.Code, w.Body)
			}
		}
		s.err = nil
	}
}

func TestSubagentRoutesDistinguishEmptyFromUnavailable(t *testing.T) {
	s := &subagentReadStore{empty: true}
	h, _, _ := testHandler(t, WithSubagents(s))
	unavailable, _, _ := testHandler(t)
	for _, route := range subagentRoutes {
		w := requestSubagents(unavailable, route.path, "Bearer test-api-key", "agents=v1")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("unwired store fabricated success: %d %s", w.Code, w.Body)
		}
		if route.list {
			w = requestSubagents(h, route.path, "Bearer test-api-key", "agents=v1")
			expected := `{"object":"list","first_id":null,"last_id":null,"data":[],"has_more":false}`
			if route.method == "list" {
				expected = `{"data":[],"has_more":false}`
			}
			if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != expected {
				t.Fatalf("empty page: %d %s", w.Code, w.Body)
			}
		}
	}
}

func TestSubagentRoutesExposeOnlyOfficialReads(t *testing.T) {
	s := &subagentReadStore{}
	h, _, _ := testHandler(t, WithSubagents(s))
	for _, route := range subagentRoutes {
		for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
			r := httptest.NewRequest(method, "/v1/agents/sessions/session/subagents"+route.path, nil)
			r.Header.Set("Authorization", "Bearer test-api-key")
			r.Header.Set("OpenAI-Beta", "agents=v1")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusMethodNotAllowed || s.calls != 0 {
				t.Fatalf("unexpected mutation route %s %s: %d %s", method, route.path, w.Code, w.Body)
			}
		}
	}
}
