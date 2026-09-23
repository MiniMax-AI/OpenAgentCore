package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type emptyEventSessionStore struct {
	ResourceStore
	tenant, id string
	reads      int
}

func (s *emptyEventSessionStore) GetSession(_ context.Context, tenant, id string) (store.Session, error) {
	s.tenant, s.id = tenant, id
	s.reads++
	if id != "owned" {
		return store.Session{}, store.ErrNotFound
	}
	return store.Session{ID: id, TenantID: tenant, Configuration: json.RawMessage(`{"environment":{"type":"none"}}`)}, nil
}

func TestEmptyEventBatchAuthorizesWithoutExecutionEffects(t *testing.T) {
	for _, executor := range []bool{false, true} {
		t.Run(map[bool]string{false: "without executor", true: "with executor"}[executor], func(t *testing.T) {
			recorder := &inputRecorder{}
			var options []Option
			if executor {
				options = append(options, WithExecution(recorder))
			}
			h, base, tenant := testHandler(t, options...)
			sessions := &emptyEventSessionStore{}
			base.ResourceStore = sessions
			request := func(id, token, key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/"+id+"/events", strings.NewReader(`{"events":[]}`))
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set("OpenAI-Beta", "agents=v1")
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", key)
				r.Header.Set("X-Tenant-ID", "forged")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			for range 2 {
				w := request("owned", "test-api-key", "same-key")
				if w.Code != http.StatusAccepted || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" || sessions.tenant != tenant || sessions.id != "owned" || recorder.inputs != nil || recorder.key != "" {
					t.Fatalf("no-op reached execution or lost scope: %d %s %+v %+v", w.Code, w.Body, sessions, recorder)
				}
			}
			reads := sessions.reads
			for _, key := range []string{" ", strings.Repeat("x", 129)} {
				if w := request("owned", "test-api-key", key); w.Code != http.StatusBadRequest || sessions.reads != reads {
					t.Fatalf("invalid identity reached resource: %d %s", w.Code, w.Body)
				}
			}
			if w := request("owned", "wrong", "same-key"); w.Code != http.StatusUnauthorized || sessions.reads != reads {
				t.Fatalf("unauthorized read: %d", w.Code)
			}
			if w := request("foreign", "test-api-key", "same-key"); w.Code != http.StatusNotFound || recorder.inputs != nil {
				t.Fatalf("unknown resource accepted: %d", w.Code)
			}
			if executor {
				r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/owned/events", strings.NewReader(`{"events":[{"type":"agent.session.input.cancel"}]}`))
				r.Header.Set("Authorization", "Bearer test-api-key")
				r.Header.Set("OpenAI-Beta", "agents=v1")
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", "same-key")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusAccepted || recorder.key != "same-key" || len(recorder.inputs) != 1 {
					t.Fatalf("no-op consumed subsequent execution identity: %d %s", w.Code, w.Body)
				}
			}
		})
	}
}

func TestHistoryListWireEnvelopesPreserveReturnedOrder(t *testing.T) {
	for _, ids := range [][]string{nil, {"only"}, {"newest", "oldest"}} {
		sessions := make([]v1.Session, len(ids))
		turns := make([]v1.Turn, len(ids))
		items := make([]v1.Item, len(ids))
		for i, id := range ids {
			sessions[i].ID = id
			turns[i].ID = id
			items[i].ID = id
		}
		for name, page := range map[string]any{
			"sessions": sessionListResponse(sessions, len(ids) > 1),
			"turns":    turnListResponse(turns, len(ids) > 1),
			"items":    itemListResponse(items, len(ids) > 1),
		} {
			t.Run(name+"/"+strings.Join(ids, ","), func(t *testing.T) {
				raw, err := json.Marshal(page)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]json.RawMessage
				if json.Unmarshal(raw, &got) != nil {
					t.Fatal(string(raw))
				}
				if len(got) != 5 || string(got["object"]) != `"list"` || string(got["data"]) == "null" {
					t.Fatalf("incomplete envelope: %s", raw)
				}
				var first, last *string
				_ = json.Unmarshal(got["first_id"], &first)
				_ = json.Unmarshal(got["last_id"], &last)
				if len(ids) == 0 {
					if first != nil || last != nil || string(got["has_more"]) != "false" {
						t.Fatal(string(raw))
					}
					return
				}
				if first == nil || last == nil || *first != ids[0] || *last != ids[len(ids)-1] {
					t.Fatalf("cursors disagree with ordered data: %s", raw)
				}
				var values []struct{ ID string }
				_ = json.Unmarshal(got["data"], &values)
				actual := make([]string, len(values))
				for i, value := range values {
					actual[i] = value.ID
				}
				if !reflect.DeepEqual(actual, ids) {
					t.Fatalf("reordered page: %v", actual)
				}
			})
		}
	}
}
