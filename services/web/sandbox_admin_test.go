package main

import (
	"net/http"
	"sync/atomic"
	"testing"
)

func adminRequest(t *testing.T, serverURL, method, path, token string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, serverURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}
func TestSandboxAdminUsesAuthenticatedConsoleAndServerCredential(t *testing.T) {
	var calls atomic.Int32
	server, _ := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer private-core-key" || r.Header.Get("X-Core-Console-Actor") != "console" {
			t.Error("console did not supply the Core key and fixed actor label")
		}
		w.WriteHeader(204)
	}))
	for _, tc := range []struct{ method, path string }{{"GET", "/core/v1/sandbox/deployment"}, {"GET", "/core/v1/sandbox/nodes?limit=5"}, {"GET", "/core/v1/sandbox/nodes/node?range=1h"}, {"PATCH", "/core/v1/sandbox/nodes/node"}, {"DELETE", "/core/v1/sandbox/nodes/node"}, {"GET", "/core/v1/sandbox/nodes/node/allocations"}, {"POST", "/core/v1/sandbox/enrollment-tokens"}} {
		req := consoleRequest(t, server, tc.method, tc.path)
		req.Header.Set("X-Core-Console-Actor", "spoofed")
		response, _ := responseBody(t, server, req)
		if response.StatusCode != 204 {
			t.Errorf("%s = %d", tc.path, response.StatusCode)
		}
	}
	for _, token := range []string{"project-key", "private-core-key", "incorrect"} {
		response, _ := responseBody(t, server, adminRequest(t, server.URL, "GET", "/core/v1/sandbox/nodes", token))
		if response.StatusCode != 401 {
			t.Fatal("bearer bypassed console login")
		}
	}
	if calls.Load() != 7 {
		t.Fatal("unexpected sandbox proxy calls")
	}
}
func TestSandboxAdminKeepsOriginAndPathBoundary(t *testing.T) {
	server, _ := testConsole(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unsafe request reached Core") }))
	for _, tc := range []struct {
		change func(*http.Request)
		status int
	}{{func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403}, {func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403}, {func(r *http.Request) { r.Host = "attacker.example" }, 403}, {func(r *http.Request) { r.Header.Del("Origin"); r.Header.Del("Sec-Fetch-Site") }, 403}, {func(r *http.Request) { r.URL.Path = "/core/v1/sandbox/../enroll" }, 400}, {func(r *http.Request) { r.Header.Set("Upgrade", "websocket") }, 400}} {
		req := consoleRequest(t, server, "POST", "/core/v1/sandbox/enrollment-tokens")
		tc.change(req)
		response, _ := responseBody(t, server, req)
		if response.StatusCode != tc.status {
			t.Errorf("unsafe status %d want%d", response.StatusCode, tc.status)
		}
	}
}
