package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPairedConsoleProxiesOnlyAdministration(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/api/v1/sandbox-node/enroll" {
			if r.Header.Get("Authorization") != "Bearer node-token" {
				t.Errorf("node credential was replaced for %s", r.URL.Path)
			}
		} else if strings.HasPrefix(r.URL.Path, "/api/v1/sandbox-node/install/") {
			if r.Header.Get("Authorization") != "" {
				t.Error("anonymous installation download acquired a Core credential")
			}
		} else if r.Header.Get("Authorization") != "Bearer server-admin" {
			t.Errorf("incorrect upstream authority for %s", r.URL.Path)
		}
		if r.URL.Path == "/core/v1/sandbox/deployment/maintenance" {
			w.WriteHeader(http.StatusNotFound)
		} else if r.Method == "DELETE" && r.URL.RawQuery != "expected_generation=7" {
			t.Error("reset cancellation query was lost")
		}
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("console"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := newConsole(config{origin: testOrigin, upstream: u, dist: dist, coreKey: "server-admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := serveSignedIn(t, h)
	for _, tc := range []struct {
		method, path, auth string
		status             int
	}{
		{"POST", "/core/v1/sandbox/deployment", "session", 200},
		{"POST", "/core/v1/sandbox/deployment", "none", 401},
		{"PUT", "/core/v1/sandbox/deployment", "session", 200},
		{"PUT", "/core/v1/sandbox/deployment", "none", 401},
		{"PATCH", "/core/v1/sandbox/deployment/maintenance", "session", 404},
		{"PATCH", "/core/v1/sandbox/deployment/maintenance", "none", 401},
		{"POST", "/core/v1/sandbox/deployment/reset", "session", 200},
		{"DELETE", "/core/v1/sandbox/deployment/reset?expected_generation=7", "session", 200},
		{"DELETE", "/core/v1/sandbox/deployment/reset?expected_generation=7", "none", 401},
		{"GET", "/core/v1/sandbox/nodes", "node", 401},
		{"GET", "/core/v1/projects", "session", 200},
		{"POST", "/api/v1/sandbox-node/enroll", "node", 200},
		{"GET", "/core/v1/installation", "session", 200},
		{"GET", "/core/v1/installation", "none", 401},
		{"GET", "/api/v1/sandbox-node/install/releases/" + strings.Repeat("a", 40) + "/node-install.pyz", "none", 200},
		{"HEAD", "/api/v1/sandbox-node/install/releases/" + strings.Repeat("a", 40) + "/node-install.pyz", "none", 200},
	} {
		r := consoleRequest(t, server, tc.method, tc.path)
		if tc.auth != "session" {
			r.Header.Del("Cookie")
		}
		if tc.auth == "node" {
			r.Header.Set("Authorization", "Bearer node-token")
			r.Header.Set("X-Core-Console-Actor", "spoofed")
			r.Header.Del("Origin")
			r.Header.Del("Sec-Fetch-Site")
		}
		response, body := responseBody(t, server, r)
		if response.StatusCode != tc.status {
			t.Errorf("%s %s/%s status=%d", tc.method, tc.path, tc.auth, response.StatusCode)
		}
		if strings.Contains(body, "server-admin") || strings.Contains(body, "project-token") || strings.Contains(body, "must-not-be-served") {
			t.Fatal("credential leaked")
		}
	}
	if calls.Load() != 10 {
		t.Fatalf("unexpected upstream requests: %d", calls.Load())
	}
	r := consoleRequest(t, server, "POST", "/core/v1/sandbox/deployment")
	r.Header.Set("Origin", "https://foreign.invalid")
	response, _ := responseBody(t, server, r)
	if response.StatusCode != 403 {
		t.Fatal("cross-origin setup reached Core")
	}
}
