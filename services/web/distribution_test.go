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

// activeRelease publishes an empty node payload release under root the way
// `oac init` does and returns the release directory.
func activeRelease(t *testing.T, root string, manifest map[string]any) string {
	t.Helper()
	revision := strings.Repeat("a", 40)
	release := filepath.Join(root, "releases", revision)
	if err := os.MkdirAll(release, 0700); err != nil {
		t.Fatal(err)
	}
	manifest["source_commit"] = revision
	raw, _ := json.Marshal(manifest)
	if os.WriteFile(filepath.Join(release, "manifest.json"), raw, 0600) != nil ||
		os.WriteFile(filepath.Join(root, "active.json"), []byte(`{"source_commit":"`+revision+`"}`), 0600) != nil {
		t.Fatal("cannot publish the node payload")
	}
	return release
}

func TestConsoleRequiresPublishedNodePayload(t *testing.T) {
	payload := t.TempDir()
	if err := os.WriteFile(filepath.Join(payload, "node-install.pyz"), []byte("bootstrap"), 0600); err != nil {
		t.Fatal(err)
	}
	upstream, _ := url.Parse("http://127.0.0.1:1")
	if _, err := newConsole(config{origin: testOrigin, upstream: upstream, dist: t.TempDir(), coreKey: testCoreKey, nodePayloadDir: payload}); err == nil {
		t.Fatal("console started from a node payload without active.json")
	}
}

func TestOfflineArtifactsAreManifestAllowlisted(t *testing.T) {
	dist, payload := t.TempDir(), t.TempDir()
	release := activeRelease(t, payload, map[string]any{"artifacts": map[string]any{"native/bin/oac-node": map[string]string{"filename": "matched-node"}, "private/key": map[string]string{"filename": "private-key"}, "native/bin/oac-selfhost": map[string]string{"filename": "retired-launcher"}}})
	for _, item := range []struct{ root, name, body string }{{dist, "index.html", "console"}, {release, "node-install.pyz", "bootstrap"}} {
		if err := os.WriteFile(filepath.Join(item.root, item.name), []byte(item.body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(release, "artifacts"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"matched-node", "private-key", "undeclared", "retired-launcher"} {
		if err := os.WriteFile(filepath.Join(release, "artifacts", name), []byte("payload"), 0600); err != nil {
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
	for _, name := range []string{"matched-node", "private-key", "undeclared", "retired-launcher"} {
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
	artifacts := map[string]any{}
	for _, logical := range []string{"native/bin/oac-node", "images/runtime.tar.gz", "native/microsandbox/msb", "runtime/seccomp.json"} {
		artifacts[logical] = map[string]any{"filename": strings.ReplaceAll(logical, "/", "-"), "size": len("runtime-bytes")}
	}
	release := activeRelease(t, payload, map[string]any{"artifacts": artifacts})
	write(filepath.Join(dist, "index.html"), "console")
	write(filepath.Join(release, "node-install.pyz"), "bootstrap")
	for logical := range artifacts {
		write(filepath.Join(release, "artifacts", strings.ReplaceAll(logical, "/", "-")), "runtime-bytes")
	}
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
	if err := os.RemoveAll(filepath.Join(release, "artifacts")); err != nil {
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

func TestRetainedPayloadsRemainHTTPReachableAfterPublication(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, revision := range []string{a, b} {
		prefix := "releases/" + revision + "/"
		write(prefix+"manifest.json", `{"source_commit":"`+revision+`","artifacts":{"native/bin/oac-node":{"filename":"node"},"private/key":{"filename":"key"}}}`)
		write(prefix+"node-install.pyz", "installer-"+revision)
		write(prefix+"artifacts/node", "binary-"+revision)
		write(prefix+"artifacts/key", "secret-must-not-be-served")
	}
	payload, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer payload.Close()
	h := &console{nodePayload: payload}
	request := func(path string) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		h.serveNodePayload(w, httptest.NewRequest("GET", "/node-install/"+path, nil))
		return w.Code, w.Body.String()
	}
	for _, revision := range []string{a, b} {
		write("active.json", `{"source_commit":"`+revision+`"}`)
		if code, body := request("node-install.pyz"); code != 200 || body != "installer-"+revision {
			t.Fatal("active release not served", code, body)
		}
		if code, body := request("releases/" + a + "/artifacts/node"); code != 200 || body != "binary-"+a {
			t.Fatal("retained release unreachable", code, body)
		}
		for _, name := range []string{"active.json", "releases/" + a + "/artifacts/key", "releases/" + a + "/secrets/core.key", "releases/../../node-install.pyz"} {
			if code, _ := request(name); code != 404 {
				t.Fatal("non-public file exposed", name, code)
			}
		}
	}
	write("releases/"+a+"/manifest.json", `{"source_commit":"`+b+`"}`)
	if code, _ := request("releases/" + a + "/node-install.pyz"); code != 404 {
		t.Fatal("mismatched release identity was served")
	}
	write("active.json", `{"source_commit":"../../"}`)
	if code, _ := request("node-install.pyz"); code != 404 {
		t.Fatal("invalid pointer did not fail closed")
	}
}
