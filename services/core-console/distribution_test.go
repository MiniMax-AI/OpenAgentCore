package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
