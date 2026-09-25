package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Applications (/v1) and machines (/api/v1) reach Core directly through the
// reverse proxy; the console forwards neither, whatever credential is presented.
func TestCoreDirectRoutesNeverPassThroughConsole(t *testing.T) {
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("direct Core route reached Core through console") }))
	h := startConsole(t, c)
	cookie := signIn(t, h)
	for _, path := range []string{"/v1", "/v1/agents", "/v1/agents/sessions", "/v1/files/file/content", "/console/api-keys",
		"/api/v1", "/api/v1/sandbox-node/enroll", "/api/v1/sandbox-node/configuration", "/api/v1/sandbox-node/connect", "/api/v1/agent-daemon/enroll", "/api/v1/agent-daemon/ws"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			for _, authorization := range []string{"", "Bearer project-key", "Bearer node-token", "Bearer " + testCoreKey, "Basic YWRtaW46cGFzc3dvcmQ="} {
				for _, upgrade := range []string{"", "websocket"} {
					r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
					r.Host = h.host
					r.Header.Set("Origin", h.origin)
					r.Header.Set("Authorization", authorization)
					r.Header.Set("Upgrade", upgrade)
					r.AddCookie(cookie)
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					if w.Code != 404 {
						t.Errorf("%s %s upgrade=%q = %d", method, path, upgrade, w.Code)
					}
				}
			}
		}
	}
}

// Encoded or doubled separators and dot segments cannot turn a /core/v1
// request into a forwarded /v1 or /api/v1 request.
func TestConsoleRejectsSmuggledMachinePaths(t *testing.T) {
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("smuggled path reached Core") }))
	h := startConsole(t, c)
	cookie := signIn(t, h)
	for _, target := range []string{"/core/v1/sandbox/..%2F..%2F..%2Fapi/v1/sandbox-node/enroll", "//api/v1/sandbox-node/enroll",
		"/core/v1/sandbox/nodes/%2E%2E/%2E%2E/%2E%2E/%2E%2E/api/v1/agent-daemon/ws", "/core/v1/../../v1/agents", "/core/v1/%2E%2E/%2E%2E/v1/agents",
		"/core/v1/projects/p/..%2F..%2F..%2F..%2Fv1/agents", "/core/v1/projects/%252E%252E/v1", "/core/v1//v1/agents", "/core/v1/projects/%5C..%5C..%5Cv1"} {
		r := httptest.NewRequest("POST", target, strings.NewReader(`{}`))
		r.Host = h.host
		r.Header.Set("Origin", h.origin)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 && w.Code != 404 {
			t.Errorf("%s = %d", target, w.Code)
		}
	}
}
