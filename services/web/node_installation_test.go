package main

import (
	"crypto/sha256"
	"encoding/hex"
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
		if r.Header.Get("Authorization") != "Bearer server-admin" {
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
	dist, payload := t.TempDir(), t.TempDir()
	for _, file := range []struct{ path, value string }{{filepath.Join(dist, "index.html"), "console"}, {filepath.Join(payload, "node-install.pyz"), "print('installer')"}, {filepath.Join(payload, "self-hosted-install.pyz"), "print('self-hosted')"}, {filepath.Join(payload, "caller.key"), "must-not-be-served"}} {
		if err := os.WriteFile(file.path, []byte(file.value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h, err := newConsole(config{origin: testOrigin, upstream: u, dist: dist, coreKey: "server-admin", nodePayloadDir: payload})
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
		{"POST", "/api/v1/sandbox-node/enroll", "node", 404},
		{"GET", "/console/config", "session", 200},
		{"GET", "/console/config", "none", 401},
		{"GET", "/node-install/node-install.pyz", "none", 200},
		{"GET", "/node-install/caller.key", "none", 404},
		{"GET", "/node-install/self-hosted-install.pyz", "none", 404},
		{"POST", "/node-install/node-install.pyz", "none", 405},
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
		// Web verifies each downloaded installer against these digests before running it.
		nodeDigest := sha256.Sum256([]byte("print('installer')"))
		if tc.path == "/console/config" && tc.status == 200 && (!strings.Contains(body, `"node_installer":true`) ||
			!strings.Contains(body, `"node_installer_sha256":"`+hex.EncodeToString(nodeDigest[:])+`"`) || strings.Contains(body, "self_hosted_installer") ||
			strings.Contains(body, "sandbox_admin") || strings.Contains(body, "api_keys")) {
			t.Fatalf("console configuration = %s", body)
		}
	}
	if calls.Load() != 6 {
		t.Fatalf("unexpected upstream requests: %d", calls.Load())
	}
	r := consoleRequest(t, server, "POST", "/core/v1/sandbox/deployment")
	r.Header.Set("Origin", "https://foreign.invalid")
	response, _ := responseBody(t, server, r)
	if response.StatusCode != 403 {
		t.Fatal("cross-origin setup reached Core")
	}
}
