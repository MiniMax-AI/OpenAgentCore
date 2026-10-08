package nativeinstaller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
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
		{"build/linux-amd64.tar.gz", 200}, {"build/linux-amd64.sha256", 200}, {"other/linux-amd64.tar.gz", 404}, {"build/windows-arm64.tar.gz", 404}, {"build/bootstrap.ps1", 404}, {"build/bootstrap.sh", 200}, {"build/catalog.json", 404}, {"build/../../catalog.json", 404},
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

func TestMissingCatalogServesNoInstallers(t *testing.T) {
	if catalog, err := Load(t.TempDir(), "build"); catalog != nil || err != nil {
		t.Fatal(catalog, err)
	}
}

func TestCatalogRejectsUnsupportedPlatforms(t *testing.T) {
	for _, platform := range []string{"linux-arm64", "darwin-arm64", "windows-amd64"} {
		t.Run(platform, func(t *testing.T) {
			dir := t.TempDir()
			manifest := Catalog{Version: "build", ProtocolVersion: proto.Version, Artifacts: map[string]Artifact{platform: {SHA256: strings.Repeat("0", 64), URL: "https://downloads.example/v1/oac-native-build-" + platform + ".tar.gz"}}}
			raw, _ := json.Marshal(manifest)
			if err := os.WriteFile(filepath.Join(dir, "catalog.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(dir, "build"); err == nil {
				t.Fatal("unsupported platform accepted")
			}
		})
	}
}

func TestDownloadResponsesKeepConditionalRangesAndJSONErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "linux-amd64.tar.gz")
	if err := os.WriteFile(file, []byte("archive fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog := &Catalog{Version: "build", directory: dir, local: map[string]bool{"linux-amd64": true}, Artifacts: map[string]Artifact{"linux-amd64": {}}}
	target := "/api/v1/agent-daemon/install/build/linux-amd64.tar.gz"
	get := func(method string, headers http.Header) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, target, nil)
		if headers != nil {
			request.Header = headers
		}
		response := httptest.NewRecorder()
		catalog.ServeHTTP(response, request)
		return response
	}
	full := get("GET", nil)
	if full.Code != 200 || full.Body.String() != "archive fixture" || full.Header().Get("Content-Length") != "15" || full.Header().Get("Last-Modified") == "" {
		t.Fatalf("full: %v", full)
	}
	head := get("HEAD", nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "15" || head.Header().Get("Last-Modified") != full.Header().Get("Last-Modified") {
		t.Fatalf("head: %v", head)
	}
	for _, method := range []string{"GET", "HEAD"} {
		cached := get(method, http.Header{"If-Modified-Since": {full.Header().Get("Last-Modified")}})
		if cached.Code != 304 || cached.Body.Len() != 0 {
			t.Fatalf("conditional %s: %v", method, cached)
		}
	}
	partial := get("GET", http.Header{"Range": {"bytes=2-5"}})
	if partial.Code != 206 || partial.Body.String() != "chiv" || partial.Header().Get("Content-Range") != "bytes 2-5/15" || partial.Header().Get("Content-Length") != "4" {
		t.Fatalf("range: %v", partial)
	}
	invalid := get("GET", http.Header{"Range": {"bytes=99-100"}})
	if invalid.Code != 416 || invalid.Header().Get("Content-Range") != "bytes */15" {
		t.Fatalf("invalid range: %v", invalid)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	missing := get("GET", nil)
	if missing.Code != 404 {
		t.Fatalf("missing: %v", missing)
	}
	for _, response := range []*httptest.ResponseRecorder{invalid, missing} {
		var body v1.ErrorResponse
		if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Content-Length") != "" || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Error.Message == "" {
			t.Fatalf("download error: %v", response)
		}
	}
}
