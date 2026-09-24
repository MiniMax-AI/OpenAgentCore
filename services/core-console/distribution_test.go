package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentConnectionCredentialsStayScoped(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Bearer project-token"
		if r.URL.Path == "/api/v1/agent-daemon/enroll" || r.URL.Path == "/api/v1/agent-daemon/connection" {
			want = "Bearer executor-key"
		}
		if r.Header.Get("Authorization") != want {
			t.Errorf("incorrect authority on %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("console"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := newConsole(config{origin: testOrigin, upstream: u, dist: dist, password: "private-console-password", adminToken: "server-admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := httptest.NewServer(h)
	defer server.Close()
	for _, tc := range []struct {
		method, path, bearer string
		status               int
	}{
		{"POST", "/core/v1/environments/env/executor-credentials", "", 404},
		{"POST", "/core/v1/environments/env/executor-credentials", "project-token", 404},
		{"DELETE", "/core/v1/environments/env/executor-credentials/key", "project-token", 404},
		{"GET", "/core/v1/environments/env/executor-credentials", "", 404},
		{"POST", "/api/v1/agent-daemon/enroll", "executor-key", 200},
		{"POST", "/api/v1/agent-daemon/enroll", "", 403},
		{"GET", "/api/v1/agent-daemon/connection?environment_id=env", "executor-key", 200},
		{"GET", "/api/v1/agent-daemon/connection?environment_id=env", "", 403},
		{"POST", "/api/v1/agent-daemon/connection", "executor-key", 403},
	} {
		req := consoleRequest(t, server, tc.method, tc.path)
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
			req.Header.Del("Origin")
			req.Header.Del("Sec-Fetch-Site")
		}
		response, _ := responseBody(t, server, req)
		if response.StatusCode != tc.status {
			t.Errorf("%s %s: %d", tc.method, tc.path, response.StatusCode)
		}
	}
}

func TestOfflineArtifactsAreManifestAllowlisted(t *testing.T) {
	dist, payload := t.TempDir(), t.TempDir()
	for _, item := range []struct{ root, name, body string }{{dist, "index.html", "console"}, {payload, "node-install.pyz", "bootstrap"}} {
		if err := os.WriteFile(filepath.Join(item.root, item.name), []byte(item.body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(payload, "artifacts"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"artifacts": map[string]any{"native/bin/parsar-sandbox-node": map[string]string{"filename": "matched-node"}, "private/key": map[string]string{"filename": "private-key"}}}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(payload, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"matched-node", "private-key", "undeclared"} {
		if err := os.WriteFile(filepath.Join(payload, "artifacts", name), []byte("payload"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	upstream, _ := url.Parse("http://127.0.0.1:1")
	h, err := newConsole(config{origin: testOrigin, upstream: upstream, dist: dist, password: "private-console-password", adminToken: "server-admin", nodePayloadDir: payload})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := httptest.NewServer(h)
	defer server.Close()
	for _, name := range []string{"matched-node", "private-key", "undeclared"} {
		req := consoleRequest(t, server, "GET", "/node-install/artifacts/"+name)
		req.Header.Del("Authorization")
		response, body := responseBody(t, server, req)
		if name == "matched-node" {
			if response.StatusCode != 200 || !strings.Contains(body, "payload") {
				t.Fatal("matched artifact unavailable")
			}
		} else if response.StatusCode != 404 {
			t.Fatal("unexpected artifact exposed")
		}
	}
}
