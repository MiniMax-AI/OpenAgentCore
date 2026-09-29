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
