package nativeinstaller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

const nodeRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func nodeFixture(t *testing.T, remote string) (string, map[string]any) {
	t.Helper()
	root := t.TempDir()
	manifest := map[string]any{"source_commit": nodeRevision, "platform": "linux/amd64", "artifact_base_url": remote,
		"images":                 map[string]string{"runtime": "sha256:" + strings.Repeat("1", 64)},
		"image_manifest_digests": map[string]string{"runtime": "sha256:" + strings.Repeat("2", 64)},
		"runtime_ref":            "oac-runtime@sha256:" + strings.Repeat("3", 64),
		"microsandbox":           map[string]string{"runtime_sha256": strings.Repeat("4", 64), "firmware_sha256": strings.Repeat("5", 64)}}
	entries := map[string]any{}
	catalog, err := providers.Builtin().ArtifactCatalog()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("artifact"))
	for _, artifacts := range catalog {
		for _, artifact := range artifacts {
			entries[artifact.Path] = map[string]any{"filename": nodeRevision + "-" + artifact.Suffix, "size": 8, "sha256": hex.EncodeToString(sum[:])}
		}
	}
	manifest["artifacts"] = entries
	publishFixture(t, root, manifest)
	return root, manifest
}

func publishFixture(t *testing.T, root string, manifest map[string]any) {
	t.Helper()
	prefix := filepath.Join(root, "node-payload", "releases", nodeRevision)
	raw, _ := json.Marshal(manifest)
	files := map[string][]byte{"manifest.json": raw, "node-install.pyz": []byte("installer"), "runtime/seccomp.json": []byte("{}")}
	sums := ""
	for name, data := range files {
		sum := sha256.Sum256(data)
		sums += fmt.Sprintf("%x  %s\n", sum, name)
		writeNodeFile(t, filepath.Join(prefix, name), data)
	}
	writeNodeFile(t, filepath.Join(prefix, "SHA256SUMS"), []byte(sums))
	writeNodeFile(t, filepath.Join(root, "node-payload", "active.json"), []byte(`{"source_commit":"`+nodeRevision+`"}`))
}

func writeNodeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNodeCatalogMatchesCoreAndProviderDeclarations(t *testing.T) {
	root, _ := nodeFixture(t, "https://downloads.example/v1")
	c, err := Load(root, nodeRevision, providers.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	snapshot := c.NodeInstallation()
	if c.NativeAvailable() || snapshot == nil || len(snapshot.RuntimeReleases) != 2 || c.RuntimeRelease("e2b") != nil {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	release := c.RuntimeRelease("docker")
	if release.SourceCommit != nodeRevision || release.Artifacts["image_id"] != "sha256:"+strings.Repeat("1", 64) || len(release.Artifacts) != 2 {
		t.Fatal(release)
	}
	release.Artifacts["image_id"] = "mutated"
	if c.RuntimeRelease("docker").Artifacts["image_id"] == "mutated" {
		t.Fatal("caller mutated the catalog")
	}
	if c, err := Load(root, "", providers.Builtin()); c != nil || err != nil {
		t.Fatal("development build guessed a release", c, err)
	}
	if _, err := Load(root, strings.Repeat("b", 40), providers.Builtin()); err == nil {
		t.Fatal("mismatched build accepted")
	}
	if c, err := Load(t.TempDir(), nodeRevision, providers.Builtin()); c != nil || err != nil {
		t.Fatal("absent payload", c, err)
	}
}

func TestNodeCatalogRechecksSameReleaseArtifacts(t *testing.T) {
	root, manifest := nodeFixture(t, "")
	c, err := Load(root, nodeRevision, providers.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.NodeInstallation().RuntimeReleases) != 0 {
		t.Fatal("offline artifacts missing")
	}
	requirements, _ := providers.Builtin().ArtifactCatalog()
	entries := manifest["artifacts"].(map[string]any)
	for _, artifact := range requirements["docker"] {
		entry := entries[artifact.Path].(map[string]any)
		writeNodeFile(t, filepath.Join(root, "node-payload", "releases", nodeRevision, "artifacts", entry["filename"].(string)), []byte("artifact"))
	}
	if len(c.NodeInstallation().RuntimeReleases) != 1 || c.RuntimeRelease("docker") == nil || c.RuntimeRelease("microsandbox") != nil {
		t.Fatal("same-release additions not observed")
	}
	entry := entries[requirements["docker"][0].Path].(map[string]any)
	writeNodeFile(t, filepath.Join(root, "node-payload", "releases", nodeRevision, "artifacts", entry["filename"].(string)), []byte("wrong size"))
	if c.RuntimeRelease("docker") != nil {
		t.Fatal("corrupt local payload reported available")
	}
}

func TestNodeCatalogRejectsMalformedMetadata(t *testing.T) {
	for _, test := range []string{"checksum", "source", "platform", "identity", "active", "symlink", "artifact_hash", "artifact_name", "remote", "base_type", "artifacts_type"} {
		t.Run(test, func(t *testing.T) {
			root, m := nodeFixture(t, "https://downloads.example/v1")
			switch test {
			case "base_type":
				m["artifact_base_url"] = 42
				publishFixture(t, root, m)
			case "artifacts_type":
				m["artifacts"] = []any{}
				publishFixture(t, root, m)
			case "artifact_hash":
				m["artifacts"].(map[string]any)["native/bin/oac-node"].(map[string]any)["sha256"] = "bad"
				publishFixture(t, root, m)
			case "artifact_name":
				m["artifacts"].(map[string]any)["native/bin/oac-node"].(map[string]any)["filename"] = "../secret"
				publishFixture(t, root, m)
			case "remote":
				m["artifact_base_url"] = "https://downloads.example/latest"
				publishFixture(t, root, m)
			case "source":
				m["source_commit"] = strings.Repeat("b", 40)
				publishFixture(t, root, m)
			case "platform":
				m["platform"] = "linux/arm64"
				publishFixture(t, root, m)
			case "identity":
				m["images"] = map[string]string{"runtime": "invalid"}
				publishFixture(t, root, m)
			case "checksum":
				writeNodeFile(t, filepath.Join(root, "node-payload", "releases", nodeRevision, "node-install.pyz"), []byte("changed"))
			case "active":
				writeNodeFile(t, filepath.Join(root, "node-payload", "active.json"), []byte(`{}`))
			case "symlink":
				path := filepath.Join(root, "node-payload", "releases", nodeRevision, "node-install.pyz")
				os.Remove(path)
				if err := os.Symlink(filepath.Join(t.TempDir(), "secret"), path); err != nil {
					t.Fatal(err)
				}
			}
			if c, err := Load(root, nodeRevision, providers.Builtin()); err == nil {
				c.Close()
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}

func TestNodeDownloadsKeepAllowlistRangesAndPinnedRedirects(t *testing.T) {
	root, m := nodeFixture(t, "https://downloads.example/v1")
	c, err := Load(root, nodeRevision, providers.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	prefix := nodeInstallPath + "releases/" + nodeRevision + "/"
	get := func(method, path string, headers http.Header) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header = headers
		w := httptest.NewRecorder()
		c.ServeNodeHTTP(w, r)
		return w
	}
	full := get("GET", prefix+"node-install.pyz", nil)
	if full.Code != 200 || full.Body.String() != "installer" {
		t.Fatal(full)
	}
	head := get("HEAD", prefix+"node-install.pyz", nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "9" {
		t.Fatal(head)
	}
	partial := get("GET", prefix+"node-install.pyz", http.Header{"Range": []string{"bytes=1-3"}})
	if partial.Code != 206 || partial.Body.String() != "nst" {
		t.Fatal(partial)
	}
	unchanged := get("GET", prefix+"node-install.pyz", http.Header{"If-Modified-Since": []string{full.Header().Get("Last-Modified")}})
	if unchanged.Code != 304 {
		t.Fatal(unchanged)
	}
	invalid := get("GET", prefix+"node-install.pyz", http.Header{"Range": []string{"bytes=99-"}})
	if invalid.Code != 416 || !strings.Contains(invalid.Body.String(), `"error"`) {
		t.Fatal(invalid)
	}
	for _, path := range []string{"active.json", "secrets/core.key", "self-hosted-install.pyz", "../node-install.pyz", "artifacts/unknown", "releases/../../node-install.pyz", "releases/" + strings.Repeat("b", 40) + "/manifest.json", "%2e%2e/node-install.pyz"} {
		if w := get("GET", nodeInstallPath+path, nil); w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	if w := get("POST", prefix+"node-install.pyz", nil); w.Code != 405 {
		t.Fatal(w)
	}
	entries := m["artifacts"].(map[string]any)
	entry := entries["native/bin/oac-node"].(map[string]any)
	name := entry["filename"].(string)
	for _, method := range []string{"GET", "HEAD"} {
		w := get(method, prefix+"artifacts/"+name, nil)
		if w.Code != 307 || w.Header().Get("Location") != "https://downloads.example/v1/"+name {
			t.Fatal(w)
		}
	}
	secret := filepath.Join(t.TempDir(), "secret")
	writeNodeFile(t, secret, []byte("secret"))
	path := filepath.Join(root, "node-payload", "releases", nodeRevision, "artifacts", name)
	os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.Symlink(secret, path); err != nil {
		t.Fatal(err)
	}
	if w := get("GET", prefix+"artifacts/"+name, nil); w.Code != 404 {
		t.Fatal("symlink escaped", w)
	}
	for _, bad := range []string{"http://downloads.example/v1", "https://user:secret@downloads.example/v1", "https://downloads.example/latest", "https://downloads.example/v1?q=x", "https://downloads.example/v1#x", "https://downloads.example/", "https://downloads.example/v1\\bad", "https://downloads.example/v1 bad"} {
		manifest := c.nodes.manifest
		manifest.ArtifactBaseURL = bad
		if manifest.artifactURL(name) != "" {
			t.Fatal("unsafe redirect", bad)
		}
	}
}

func TestNodeRetainedReleaseAndLocalDownloadKeepTheirOwnIdentity(t *testing.T) {
	root, m := nodeFixture(t, "https://downloads.example/v1")
	c, err := Load(root, nodeRevision, providers.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	old := strings.Repeat("b", 40)
	retained := map[string]any{"source_commit": old, "artifacts": map[string]any{
		"native/bin/oac-node": map[string]any{"filename": "old-node", "size": 8, "sha256": strings.Repeat("1", 64)},
		"private/key":         map[string]any{"filename": "key", "size": 6, "sha256": strings.Repeat("2", 64)},
	}}
	raw, _ := json.Marshal(retained)
	prefix := filepath.Join(root, "node-payload", "releases", old)
	writeNodeFile(t, filepath.Join(prefix, "manifest.json"), raw)
	writeNodeFile(t, filepath.Join(prefix, "artifacts/old-node"), []byte("old-node"))
	writeNodeFile(t, filepath.Join(prefix, "artifacts/key"), []byte("secret"))
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", nodeInstallPath+path, nil)
		c.ServeNodeHTTP(w, r)
		return w
	}
	if w := request("releases/" + old + "/artifacts/old-node"); w.Code != 200 || w.Body.String() != "old-node" {
		t.Fatal(w)
	}
	if w := request("releases/" + old + "/artifacts/key"); w.Code != 404 {
		t.Fatal("nondeclared file exposed", w)
	}
	retained["source_commit"] = nodeRevision
	raw, _ = json.Marshal(retained)
	writeNodeFile(t, filepath.Join(prefix, "manifest.json"), raw)
	if w := request("releases/" + old + "/artifacts/old-node"); w.Code != 404 {
		t.Fatal("retained identity mismatch", w)
	}
	// The current catalog never follows an unrelated active pointer after load.
	writeNodeFile(t, filepath.Join(root, "node-payload", "active.json"), []byte(`{"source_commit":"`+old+`"}`))
	if w := request("node-install.pyz"); w.Code != 200 || w.Body.String() != "installer" {
		t.Fatal("current release changed", w)
	}
	entry := m["artifacts"].(map[string]any)["images/runtime.tar.gz"].(map[string]any)
	name := entry["filename"].(string)
	local := filepath.Join(root, "node-payload", "releases", nodeRevision, "artifacts", name)
	writeNodeFile(t, local, []byte("artifact"))
	r := httptest.NewRequest("GET", nodeInstallPath+"artifacts/"+name, nil)
	r.Header.Set("Range", "bytes=4-")
	w := httptest.NewRecorder()
	c.ServeNodeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "fact" || w.Header().Get("Location") != "" {
		t.Fatal("local artifact precedence/range", w)
	}
	writeNodeFile(t, local, []byte("bad"))
	if c.RuntimeRelease("docker") != nil {
		t.Fatal("remote hid corrupt local artifact")
	}
}

func TestCatalogSharesIndependentNativeAndNodeAvailability(t *testing.T) {
	root, _ := nodeFixture(t, "https://downloads.example/v1")
	native := Catalog{Version: nodeRevision, ProtocolVersion: proto.Version, Artifacts: map[string]Artifact{"linux-amd64": {SHA256: strings.Repeat("1", 64), URL: "https://downloads.example/v1/oac-native-" + nodeRevision + "-linux-amd64.tar.gz"}}}
	raw, _ := json.Marshal(native)
	writeNodeFile(t, filepath.Join(root, "native-installers", "catalog.json"), raw)
	c, err := Load(root, nodeRevision, providers.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.NativeAvailable() || c.NodeInstallation() == nil {
		t.Fatal("one distribution hid the other")
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/agent-daemon/install/"+nodeRevision+"/linux-amd64.tar.gz", nil))
	if w.Code != 307 || w.Header().Get("Location") != native.Artifacts["linux-amd64"].URL {
		t.Fatal(w)
	}
	if err := os.RemoveAll(filepath.Join(root, "node-payload")); err != nil {
		t.Fatal(err)
	}
	nativeOnly, err := Load(root, nodeRevision, providers.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	defer nativeOnly.Close()
	if !nativeOnly.NativeAvailable() || nativeOnly.NodeInstallation() != nil {
		t.Fatal("native-only catalog changed availability")
	}
}
