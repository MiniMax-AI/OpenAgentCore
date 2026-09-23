package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type itemReadStore struct {
	ResourceStore
	tenant, session, cursor string
	limit                   int
	ascending               bool
}

func (s *itemReadStore) ListItems(_ context.Context, tenant, session, cursor string, limit int, asc bool) (store.ItemPage, error) {
	s.tenant, s.session, s.cursor, s.limit, s.ascending = tenant, session, cursor, limit, asc
	return store.ItemPage{Items: []v1.Item{}, HasMore: false}, nil
}
func TestItemRouteUsesAuthenticationAndSharedPagination(t *testing.T) {
	h, record, tenant := testHandler(t)
	s := &itemReadStore{}
	record.ResourceStore = s
	request := func(query, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/agents/sessions/session/items"+query, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("X-Tenant-ID", "untrusted")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("?after=last&limit=2&order=asc", "test-api-key"); w.Code != 200 || s.tenant != tenant || s.session != "session" || s.cursor != "last" || s.limit != 2 || !s.ascending {
		t.Fatal(w.Code, w.Body, s)
	}
	if w := request("", "test-api-key"); w.Code != 200 || s.limit != 20 || s.ascending || w.Body.String() != "{\"object\":\"list\",\"first_id\":null,\"last_id\":null,\"data\":[],\"has_more\":false}\n" {
		t.Fatal(w.Code, w.Body, s)
	}
	for q, limit := range map[string]int{"?limit=0": 1, "?limit=101": 100, "?limit=1000&tenant_id=other&unknown=1": 100} {
		if w := request(q, "test-api-key"); w.Code != 200 || s.limit != limit || s.tenant != tenant {
			t.Fatal(q, w.Code, s.limit, s.tenant)
		}
	}
	for _, q := range []string{"?limit=-1", "?limit=abc", "?order=bad", "?limit=2&limit=3"} {
		if w := request(q, "test-api-key"); w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"invalid_request_error"`) {
			t.Fatal(q, w.Code, w.Body)
		}
	}
	if w := request("", "invalid"); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
