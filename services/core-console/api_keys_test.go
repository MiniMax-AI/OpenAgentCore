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
	// Every /core/v1 operation is forwarded; Core alone decides which routes exist.
	paths := []struct{ method, path string }{{"GET", "/core/v1/projects?limit=5"}, {"POST", "/core/v1/projects"}, {"POST", "/core/v1/projects/project/keys"}, {"POST", "/core/v1/projects/project/archive"}, {"DELETE", "/core/v1/projects/project/keys/key"}, {"GET", "/core/v1/projects/key/sessions/session/artifacts/artifact/content"}, {"DELETE", "/core/v1/projects/key/skills/skill/versions/1"}, {"GET", "/core/v1/summary"}, {"GET", "/core/v1/metrics?range=6h"}, {"GET", "/core/v1/sandbox/runtime-observations"},
		{"POST", "/core/v1/projects/p/environments/e/executor-credentials"}, {"DELETE", "/core/v1/projects/p/environments/e/executor-credentials/k"}, {"PATCH", "/core/v1/future/operation"}, {"GET", "/core/v1/admin/projects"}}
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
	// Only /core/v1 is forwarded.
	for _, tc := range []struct{ method, path string }{{"GET", "/core"}, {"GET", "/core/"}, {"GET", "/core/v1"}, {"GET", "/core/v2/projects"}, {"GET", "/core/projects"}} {
		if w := authRequest(h, tc.method, tc.path, `{}`, cookie); w.Code != 404 {
			t.Errorf("outside /core/v1 %s %s = %d", tc.method, tc.path, w.Code)
		}
	}
	if calls.Load() != int32(len(paths)) {
		t.Fatal("a request outside /core/v1 reached Core")
	}
}
func TestConsoleRequiresCoreKey(t *testing.T) {
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	c.coreKey = ""
	if _, err := newConsole(c); err == nil {
		t.Fatal("console started without the Core key")
	}
}
