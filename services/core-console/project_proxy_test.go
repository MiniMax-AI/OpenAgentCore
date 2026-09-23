package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProjectBearerPassesThroughWithoutConsoleAuthority(t *testing.T) {
	var calls atomic.Int32
	c := accountConsoleConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != "" {
			t.Error("console cookie leaked upstream")
		}
		switch r.Header.Get("Authorization") {
		case "Bearer caller-supplied-project-key":
			w.WriteHeader(201)
		case "Bearer incorrect-key":
			w.WriteHeader(401)
		default:
			t.Error("explicit project credential was substituted")
			w.WriteHeader(500)
		}
	}))
	h := accountConsole(t, c)
	for _, tc := range []struct {
		authorization string
		status        int
	}{
		{"Bearer caller-supplied-project-key", 201}, {"Bearer incorrect-key", 401},
	} {
		for _, path := range []string{"/v1", "/v1/agents", "/v1/agents/sessions"} {
			r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
			r.Host = h.host
			r.Header.Set("Authorization", tc.authorization)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: strings.Repeat("x", 64)})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("WWW-Authenticate") != "" {
				t.Errorf("explicit project request returned %d", w.Code)
			}
		}
	}
	if calls.Load() != 6 {
		t.Fatal("explicit project requests did not reach Core")
	}
	for _, headers := range [][]string{
		nil, {"Basic YWRtaW46cGFzc3dvcmQ="}, {"Bearer"}, {"Bearer one two"},
		{"Bearer first", "Bearer second"},
	} {
		r := httptest.NewRequest("GET", "/v1/agents", nil)
		r.Host = h.host
		r.Header["Authorization"] = headers
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("ambiguous or missing bearer entered proxy: %d", w.Code)
		}
	}
	for _, path := range []string{"/v1-other", "/console/config", "/core/v1/sandbox/nodes"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = h.host
		r.Header.Set("Authorization", "Bearer caller-supplied-project-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("project bearer gained console authority on %s: %d", path, w.Code)
		}
	}
	if calls.Load() != 6 {
		t.Fatal("invalid project request bypassed console authorization")
	}
	// A browser session must not turn a rejected API key into the console's key.
	cookie := setupAccount(t, h)
	r := httptest.NewRequest("POST", "/v1/agents", nil)
	r.Host = h.host
	r.Header.Set("Authorization", "Bearer incorrect-key")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || calls.Load() != 7 {
		t.Fatal("console session replaced explicit rejected API authority")
	}
}

func TestProjectBearerBoundaryRejectsUnsafeTransport(t *testing.T) {
	c := accountConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unsafe request reached Core") }))
	h := accountConsole(t, c)
	for _, modify := range []func(*http.Request){
		func(r *http.Request) { r.Host = "attacker.example" },
		func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
		func(r *http.Request) { r.Header.Add("Origin", h.origin); r.Header.Add("Origin", h.origin) },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		func(r *http.Request) { r.Header.Set("Upgrade", "websocket") },
		func(r *http.Request) { r.Method = "TRACE" },
		func(r *http.Request) { r.Method = "CONNECT" },
		func(r *http.Request) { r.URL.Path = "/v1/../console/config" },
		func(r *http.Request) { r.URL.Path = "/v1/%2e%2e/console/config" },
		func(r *http.Request) { r.URL.Scheme, r.URL.Host = "http", h.host },
	} {
		r := httptest.NewRequest("POST", "/v1/agents", nil)
		r.Host = h.host
		r.Header.Set("Authorization", "Bearer caller-supplied-project-key")
		modify(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("unsafe request returned %d", w.Code)
		}
	}
}
