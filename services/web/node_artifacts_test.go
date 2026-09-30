package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlineNodeArtifactsRedirectWithoutLocalPayload(t *testing.T) {
	root := t.TempDir()
	revision := strings.Repeat("a", 40)
	prefix := "releases/" + revision + "/"
	write := func(name string, data []byte) {
		t.Helper()
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256([]byte("node-data"))
	manifest := nodeManifest{SourceCommit: revision, ArtifactBaseURL: "https://github.com/MiniMax-AI/OpenAgentCore/releases/download/v1.2.3", Artifacts: map[string]nodeArtifact{}}
	for logical := range optionalPayloadFiles {
		manifest.Artifacts[logical] = nodeArtifact{Filename: "oac-" + revision + "-" + strings.ReplaceAll(logical, "/", "-"), Size: 9, SHA256: hex.EncodeToString(sum[:])}
	}
	raw, _ := json.Marshal(manifest)
	write(prefix+"manifest.json", raw)
	write("active.json", []byte(`{"source_commit":"`+revision+`"}`))
	payload, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer payload.Close()
	h := &console{nodePayload: payload}
	if got := strings.Join(h.nodeArtifacts(), ","); got != "docker,microsandbox" {
		t.Fatal(got)
	}
	name := manifest.Artifacts["images/runtime.tar.gz"].Filename
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		h.serveNodePayload(w, httptest.NewRequest(method, "/node-install/"+prefix+"artifacts/"+name, nil))
		if w.Code != 307 || w.Header().Get("Location") != manifest.ArtifactBaseURL+"/"+name {
			t.Fatal(w.Code, w.Header())
		}
	}
	if _, err := os.Stat(filepath.Join(root, prefix, "artifacts")); !os.IsNotExist(err) {
		t.Fatal("Web cached execution artifacts", err)
	}
	for _, name := range []string{"unknown", "../manifest.json", "private-key"} {
		w := httptest.NewRecorder()
		h.serveNodePayload(w, httptest.NewRequest("GET", "/node-install/"+prefix+"artifacts/"+name, nil))
		if w.Code != 404 {
			t.Fatal(w.Code, name)
		}
	}
	write(prefix+"artifacts/"+name, []byte("node-data"))
	w := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/node-install/"+prefix+"artifacts/"+name, nil)
	request.Header.Set("Range", "bytes=5-")
	h.serveNodePayload(w, request)
	if w.Code != 206 || w.Body.String() != "data" {
		t.Fatal("offline artifact did not take precedence", w.Code, w.Body.String())
	}
	// A damaged local file must not be hidden by a remote availability fallback.
	write(prefix+"artifacts/"+name, []byte("bad"))
	if len(h.nodeArtifacts()) != 0 {
		t.Fatal("damaged local Runtime was advertised")
	}
}

func TestRemoteNodeArtifactRequiresPinnedMetadata(t *testing.T) {
	revision := strings.Repeat("b", 40)
	name := "oac-" + revision + "-runtime.tar.gz"
	m := nodeManifest{SourceCommit: revision, ArtifactBaseURL: "https://release.example/download/v1", Artifacts: map[string]nodeArtifact{"images/runtime.tar.gz": {Filename: name, Size: 1, SHA256: strings.Repeat("c", 64)}}}
	if m.artifactURL(name) == "" {
		t.Fatal("valid manifest rejected")
	}
	for _, base := range []string{"http://release.example/v1", "https://release.example/latest", "https://user:pass@release.example/v1", "https://release.example/v1?token=secret", "https://release.example/v1#fragment", "https://release.example"} {
		invalid := m
		invalid.ArtifactBaseURL = base
		if invalid.artifactURL(name) != "" {
			t.Fatal("invalid release URL accepted", base)
		}
	}
	entry := m.Artifacts["images/runtime.tar.gz"]
	entry.SHA256 = "wrong"
	m.Artifacts["images/runtime.tar.gz"] = entry
	if m.artifactURL(name) != "" {
		t.Fatal("missing integrity metadata accepted")
	}
}
