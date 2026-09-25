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
	for _, item := range []struct{ root, name, body string }{{dist, "index.html", "console"}, {payload, "node-install.pyz", "bootstrap"}, {payload, "self-hosted-install.pyz", "bootstrap"}} {
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
	h, err := newConsole(config{origin: testOrigin, upstream: upstream, dist: dist, coreKey: "server-admin", nodePayloadDir: payload})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := httptest.NewServer(h)
	defer server.Close()
	for _, name := range []string{"matched-node", "private-key", "undeclared"} {
		req := consoleRequest(t, server, "GET", "/node-install/artifacts/"+name)
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

// /console/config names the providers whose node files this console holds, so
// Web offers Add node only when a node can download everything it needs. Node
// downloads can resume with HTTP Range.
func TestConsoleReportsServableNodeProviders(t *testing.T) {
	dist, payload := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dist, "index.html"), "console")
	write(filepath.Join(payload, "node-install.pyz"), "bootstrap")
	write(filepath.Join(payload, "self-hosted-install.pyz"), "executor bootstrap")
	artifacts := map[string]any{}
	for logical := range map[string]bool{"native/bin/parsar-sandbox-node": true, "images/runtime.tar.gz": true, "native/microsandbox/msb": true} {
		name := strings.ReplaceAll(logical, "/", "-")
		artifacts[logical] = map[string]any{"filename": name, "size": len("runtime-bytes")}
		write(filepath.Join(payload, "artifacts", name), "runtime-bytes")
	}
	raw, _ := json.Marshal(map[string]any{"artifacts": artifacts})
	write(filepath.Join(payload, "manifest.json"), string(raw))
	upstream, _ := url.Parse("http://127.0.0.1:1")
	h, err := newConsole(config{origin: testOrigin, upstream: upstream, dist: dist, coreKey: testCoreKey, nodePayloadDir: payload})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := serveSignedIn(t, h)
	if _, body := responseBody(t, server, consoleRequest(t, server, "GET", "/console/config")); !strings.Contains(body, `"node_artifacts":["docker"]`) {
		t.Fatal("microsandbox reported without its helper and firmware:", body)
	}
	request := consoleRequest(t, server, "GET", "/node-install/artifacts/images-runtime.tar.gz")
	request.Header.Set("Range", "bytes=8-")
	if response, body := responseBody(t, server, request); response.StatusCode != 206 || body != "bytes" {
		t.Fatal("artifact download cannot resume", response.StatusCode, body)
	}
	if err := os.RemoveAll(filepath.Join(payload, "artifacts")); err != nil {
		t.Fatal(err)
	}
	if _, body := responseBody(t, server, consoleRequest(t, server, "GET", "/console/config")); !strings.Contains(body, `"node_artifacts":[]`) {
		t.Fatal("a console without node files offered them:", body)
	}
	// A console without any node payload also reports an empty list, never null.
	bare, err := newConsole(config{origin: testOrigin, upstream: upstream, dist: dist, coreKey: testCoreKey})
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	bareServer := serveSignedIn(t, bare)
	if _, body := responseBody(t, bareServer, consoleRequest(t, bareServer, "GET", "/console/config")); !strings.Contains(body, `"node_artifacts":[]`) {
		t.Fatal("a console without a node payload did not report an empty list:", body)
	}
}
