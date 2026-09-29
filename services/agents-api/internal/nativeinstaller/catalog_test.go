package nativeinstaller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestCatalogRequiresMatchedImmutableArtifacts(t *testing.T) {
	dir := t.TempDir()
	data := []byte("archive fixture")
	hash := sha256.Sum256(data)
	manifest := Catalog{Version: "build", ProtocolVersion: proto.Version, Artifacts: map[string]Artifact{"linux-amd64": {SHA256: hex.EncodeToString(hash[:])}}}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-amd64.tar.gz"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "wrong-build"); err == nil {
		t.Fatal("accepted mismatched Core")
	}
	catalog, err := Load(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		path string
		code int
	}{
		{"build/linux-amd64.tar.gz", 200}, {"build/linux-amd64.sha256", 200}, {"other/linux-amd64.tar.gz", 404}, {"build/windows-arm64.tar.gz", 404}, {"build/catalog.json", 404}, {"build/../../catalog.json", 404},
	} {
		w := httptest.NewRecorder()
		catalog.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/agent-daemon/install/"+request.path, nil))
		if w.Code != request.code {
			t.Fatalf("%s: %d", request.path, w.Code)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-amd64.tar.gz"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "build"); err == nil {
		t.Fatal("accepted corrupt archive")
	}
}

func TestOnlineCatalogRedirectsOnlyDeclaredMatchedArchives(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("qualified archive")
	sum := sha256.Sum256(payload)
	manifest := Catalog{Version: "build", ProtocolVersion: proto.Version, Artifacts: map[string]Artifact{
		"linux-amd64": {SHA256: hex.EncodeToString(sum[:]), URL: "https://downloads.example/v1/oac-native-build-linux-amd64.tar.gz"},
	}}
	save := func() {
		t.Helper()
		raw, _ := json.Marshal(manifest)
		if err := os.WriteFile(filepath.Join(dir, "catalog.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	catalog, err := Load(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		catalog.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/agent-daemon/install/build/linux-amd64.tar.gz", nil))
		if w.Code != 307 || w.Header().Get("Location") != manifest.Artifacts["linux-amd64"].URL {
			t.Fatalf("redirect: %v", w)
		}
	}
	for _, bad := range []string{"http://downloads.example/v1/oac-native-build-linux-amd64.tar.gz", "https://user:secret@downloads.example/v1/oac-native-build-linux-amd64.tar.gz", "https://downloads.example/latest/oac-native-build-linux-amd64.tar.gz", "https://downloads.example/v1/foreign.tar.gz"} {
		previous := manifest.Artifacts["linux-amd64"]
		modified := previous
		modified.URL = bad
		manifest.Artifacts["linux-amd64"] = modified
		save()
		if _, err := Load(dir, "build"); err == nil {
			t.Fatalf("accepted %s", bad)
		}
		manifest.Artifacts["linux-amd64"] = previous
	}
	save()
	if err := os.WriteFile(filepath.Join(dir, "linux-amd64.tar.gz"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err = Load(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	catalog.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/agent-daemon/install/build/linux-amd64.tar.gz", nil))
	if w.Code != 200 || w.Body.String() != string(payload) || w.Header().Get("Location") != "" {
		t.Fatal("offline archive not served locally")
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-amd64.tar.gz"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "build"); err == nil {
		t.Fatal("corruption must not fall back to online download")
	}
}
