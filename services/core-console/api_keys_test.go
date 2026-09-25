package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAdminProxyUsesFixedActorAndServerCoreKey(t *testing.T) {
	var calls atomic.Int32
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+testCoreKey || r.Header.Get("X-Core-Console-Actor") != "console" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			t.Error("admin proxy credential or actor boundary failed")
		}
		w.WriteHeader(200)
	}))
	h := startConsole(t, c)
	paths := []struct{ method, path string }{{"GET", "/core/v1/admin/projects?limit=5"}, {"POST", "/core/v1/admin/projects"}, {"POST", "/core/v1/admin/projects/project/keys"}, {"POST", "/core/v1/admin/projects/project/archive"}, {"DELETE", "/core/v1/admin/projects/project/keys/key"}, {"GET", "/core/v1/admin/projects/key/sessions/session/artifacts/artifact/content"}, {"DELETE", "/core/v1/admin/projects/key/skills/skill/versions/1"}, {"GET", "/core/v1/admin/summary"}, {"GET", "/core/v1/admin/core-metrics?range=6h"}}
	for _, tc := range paths {
		if w := authRequest(h, tc.method, tc.path, `{}`, nil); w.Code != 401 {
			t.Errorf("unauthenticated %s = %d", tc.path, w.Code)
		}
	}
	cookie := signIn(t, h)
	for _, tc := range paths {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		r.Host = h.host
		r.Header.Set("Origin", h.origin)
		r.Header.Set("Authorization", "Bearer untrusted-browser-token")
		r.Header.Set("X-Core-Console-Actor", "spoofed")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("%s %s = %d", tc.method, tc.path, w.Code)
		}
	}
	if calls.Load() != int32(len(paths)) {
		t.Fatal("unexpected admin proxy count")
	}
	for _, tc := range []struct{ method, path string }{{"POST", "/core/v1/admin/projects/key/agents"}, {"PATCH", "/core/v1/admin/projects/key/agents/agent"}, {"POST", "/core/v1/admin/projects/key/sessions"}, {"POST", "/core/v1/admin/projects/key/sessions/session/events"}, {"GET", "/core/v1/admin/projects/key/sessions/session/events"}, {"GET", "/core/v1/admin/projects/key/files/file/content"}, {"GET", "/core/v1/admin/unknown"}, {"POST", "/core/v1/admin/core-metrics"}} {
		if w := authRequest(h, tc.method, tc.path, `{}`, cookie); w.Code != 404 {
			t.Errorf("unsupported %s %s = %d", tc.method, tc.path, w.Code)
		}
	}
	if calls.Load() != int32(len(paths)) {
		t.Fatal("unsupported management operation reached Core")
	}
}
func TestConsoleRequiresCoreKey(t *testing.T) {
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	c.coreKey = ""
	if _, err := newConsole(c); err == nil {
		t.Fatal("console started without the Core key")
	}
}
