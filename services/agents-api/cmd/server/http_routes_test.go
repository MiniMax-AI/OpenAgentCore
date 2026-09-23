package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The daemon-enabled configuration canonicalizes paths before the ServeMux:
// no request is redirected, and a dirty or encoded path reaches exactly the
// handler its canonical path reaches, with the body intact (HP-17/HP-18).
func TestServerHandlerRoutesCanonicalPaths(t *testing.T) {
	type observation struct{ route, method, path, rawPath, body string }
	var seen []observation
	sentinel := func(route string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			seen = append(seen, observation{route, r.Method, r.URL.Path, r.URL.RawPath, string(body)})
			w.WriteHeader(http.StatusNoContent)
		})
	}
	handler := serverHandler(sentinel("api"), &daemonRoutes{gateway: sentinel("gateway"), enrollment: sentinel("enrollment"),
		connection: sentinel("connection"), nodeConnect: sentinel("node")})
	for _, test := range []struct{ target, route, path, rawPath string }{
		{"/v1/agents/a", "api", "/v1/agents/a", ""},
		{"/v1//agents/a", "api", "/v1/agents/a", ""},
		{"//v1/agents", "api", "/v1/agents", ""},
		{"/v1/agents/x/../a", "api", "/v1/agents/a", ""},
		{"/v1/agents/agent%5Fa", "api", "/v1/agents/agent_a", ""},
		{"/v1/agents/a%2Fb", "api", "/v1/agents/a/b", "/v1/agents/a%2Fb"},
		{"/api/v1/agent-daemon/%2E%2E/%2E%2E/%2E%2E/v1/agents", "api", "/v1/agents", ""},
		{"/api/v1/agent-daemon/../../../core/v1/sandbox/nodes", "api", "/core/v1/sandbox/nodes", ""},
		{"/api/v1/agent-daemon%2Fenroll", "api", "/api/v1/agent-daemon/enroll", "/api/v1/agent-daemon%2Fenroll"},
		{"/api/v1/agent-daemon/ws", "gateway", "/api/v1/agent-daemon/ws", ""},
		{"/api/v1//agent-daemon/ws", "gateway", "/api/v1/agent-daemon/ws", ""},
		{"/v1/../api/v1/agent-daemon/enroll", "enrollment", "/api/v1/agent-daemon/enroll", ""},
		{"/v1/%2E%2E/api/v1/agent-daemon/connection", "connection", "/api/v1/agent-daemon/connection", ""},
		{"/api/v1/agent-daemon/%63onnection", "connection", "/api/v1/agent-daemon/connection", ""},
		{"/core/v1/sandbox/node//connect", "node", "/core/v1/sandbox/node/connect", ""},
		{"/core/v1/sandbox/nodes/%2E%2E/node/connect", "node", "/core/v1/sandbox/node/connect", ""},
	} {
		seen = nil
		request := httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(`{"model":"x"}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := observation{test.route, http.MethodPost, test.path, test.rawPath, `{"model":"x"}`}
		if response.Code != http.StatusNoContent || response.Header().Get("Location") != "" || len(seen) != 1 || seen[0] != want {
			t.Errorf("%s = %d %v, want %v", test.target, response.Code, seen, want)
		}
	}
}
