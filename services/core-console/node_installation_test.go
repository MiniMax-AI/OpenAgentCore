package main

import (
	"github.com/gorilla/websocket"
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

func TestPairedConsoleKeepsAdminAndNodeCredentialsSeparated(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		want := "Bearer server-admin"
		if nodeTransportRequest(r) {
			if r.Header.Get("X-Core-Console-Actor") != "" {
				t.Error("transport retained untrusted administrator actor")
			}
			want = "Bearer node-token"
		}
		if r.Header.Get("Authorization") != want {
			t.Errorf("incorrect upstream authority for %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	dist, payload := t.TempDir(), t.TempDir()
	for _, file := range []struct{ path, value string }{{filepath.Join(dist, "index.html"), "console"}, {filepath.Join(payload, "node-install.pyz"), "print('installer')"}, {filepath.Join(payload, "caller.key"), "must-not-be-served"}} {
		if err := os.WriteFile(file.path, []byte(file.value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h, err := newConsole(config{origin: testOrigin, upstream: u, dist: dist, password: "private-console-password", adminToken: "server-admin", nodePayloadDir: payload})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := httptest.NewServer(h)
	defer server.Close()
	for _, tc := range []struct {
		method, path, auth string
		status             int
	}{
		{"POST", "/core/v1/sandbox/deployment", "basic", 200},
		{"POST", "/core/v1/sandbox/deployment", "none", 401},
		{"PUT", "/core/v1/sandbox/deployment", "basic", 200},
		{"PUT", "/core/v1/sandbox/deployment", "none", 401},
		{"PATCH", "/core/v1/sandbox/deployment/maintenance", "basic", 200},
		{"PATCH", "/core/v1/sandbox/deployment/maintenance", "none", 401},
		{"GET", "/core/v1/sandbox/nodes", "node", 401},
		{"GET", "/core/v1/admin/projects", "basic", 200},
		{"POST", "/core/v1/sandbox/enroll", "node", 200},
		{"POST", "/core/v1/sandbox/enroll", "basic", 403},
		{"POST", "/api/v1/agent-daemon/bootstrap", "node", 200},
		{"POST", "/api/v1/agent-daemon/unknown", "node", 403},
		{"GET", "/console/config", "basic", 200},
		{"GET", "/console/config", "none", 401},
		{"GET", "/node-install/node-install.pyz", "none", 200},
		{"GET", "/node-install/caller.key", "none", 404},
		{"POST", "/node-install/node-install.pyz", "none", 405},
	} {
		r := consoleRequest(t, server, tc.method, tc.path)
		if tc.auth == "none" {
			r.Header.Del("Authorization")
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
		if tc.path == "/console/config" && tc.status == 200 && !strings.Contains(body, `"sandbox_admin":true`) {
			t.Fatal("paired mode missing")
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

func TestPairedConsoleProxiesAuthenticatedNodeWebSockets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer node-token" {
			t.Error("node authority replaced")
			http.Error(w, "unauthorized", 401)
			return
		}
		upgrader := websocket.Upgrader{}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.Close()
		if err := ws.WriteMessage(websocket.TextMessage, []byte("node-connected")); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("console"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := newConsole(config{origin: testOrigin, upstream: u, dist: dist, password: "console-password", adminToken: "server-admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := httptest.NewServer(h)
	defer server.Close()
	for _, path := range []string{"/core/v1/sandbox/node/connect", "/api/v1/agent-daemon/ws"} {
		ws, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, http.Header{"Host": {"127.0.0.1:8080"}, "Authorization": {"Bearer node-token"}})
		if err != nil {
			if response != nil {
				t.Fatalf("proxy handshake %d: %v", response.StatusCode, err)
			}
			t.Fatal(err)
		}
		_, data, err := ws.ReadMessage()
		ws.Close()
		if err != nil || string(data) != "node-connected" {
			t.Fatalf("proxy message=%q error=%v", data, err)
		}
	}
}
