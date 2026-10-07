package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/google/uuid"
)

func initFixture(t *testing.T) (string, releaseIdentity, map[string][]byte) {
	t.Helper()
	previous := chown
	chown = func(string, int, int) error { return nil }
	t.Cleanup(func() { chown = previous })
	release := releaseIdentity{strings.Repeat("d", 40)}
	files := map[string][]byte{}
	for _, name := range releaseMembers {
		files[name] = []byte("fixture")
	}
	files["manifest.json"], _ = json.Marshal(map[string]string{"source_commit": release.revision, "platform": "linux/amd64"})
	return t.TempDir(), release, files
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	saved := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		relative, _ := filepath.Rel(root, path)
		saved[filepath.ToSlash(relative)] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return saved
}

func fixed(files map[string][]byte) func() (map[string][]byte, error) {
	return func() (map[string][]byte, error) { return files, nil }
}

func refuseDownload(t *testing.T) func() (map[string][]byte, error) {
	return func() (map[string][]byte, error) {
		t.Fatal("initialization must not download")
		return nil, nil
	}
}

func TestInitializeKeepsIdentityAndKeysAcrossRestarts(t *testing.T) {
	root, release, files := initFixture(t)
	if err := initialize(root, release, fixed(files)); err != nil {
		t.Fatal(err)
	}
	saved := snapshot(t, root)
	key := strings.TrimSpace(saved["secrets/web/core.key"])
	assertCoreKeyFormat(t, key)
	var digests []string
	if err := json.Unmarshal([]byte(saved["secrets/core/core-key-digests.json"]), &digests); err != nil || len(digests) != 1 || digests[0] != keyDigest(key) {
		t.Fatalf("digests = %v, %v", digests, err)
	}
	if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(saved["secrets/core/credential.key"])); err != nil || len(raw) != 32 {
		t.Fatalf("credential key: %v", err)
	}
	if _, err := uuid.Parse(strings.TrimSpace(saved["secrets/core/installation.id"])); err != nil {
		t.Fatal(err)
	}
	var identity struct {
		RuntimeID  string `json:"runtime_id"`
		Credential string `json:"credential"`
	}
	if err := json.Unmarshal([]byte(saved["secrets/agent-host/identity.json"]), &identity); err != nil || uuid.Validate(identity.RuntimeID) != nil {
		t.Fatalf("agent-host identity: %v", err)
	}
	if raw, err := base64.StdEncoding.DecodeString(identity.Credential); err != nil || len(raw) != 32 {
		t.Fatalf("agent-host credential: %v", err)
	}
	for _, name := range []string{"secrets/web/core.key", "secrets/database/password", "secrets/core/credential.key", "secrets/agent-host/identity.json"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
			t.Fatalf("%s: %v %v", name, info.Mode(), err)
		}
	}
	if err := initialize(root, release, refuseDownload(t)); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, root); !maps.Equal(saved, after) {
		t.Fatal("a verified restart changed installation files")
	}
}

func TestInitializeRefusesDataWithoutItsInstallationFiles(t *testing.T) {
	root, release, _ := initFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "database"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "database", "PG_VERSION"), []byte("16"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initialize(root, release, refuseDownload(t)); err == nil || !strings.Contains(err.Error(), "original installation") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "secrets", "web", "core.key")); err == nil {
		t.Fatal("refused initialization generated a key")
	}
}

func TestInitializeRefusesChangedMissingOrForeignFiles(t *testing.T) {
	root, release, files := initFixture(t)
	if err := initialize(root, release, fixed(files)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "secrets", "database", "password")
	original, _ := os.ReadFile(path)
	_ = os.WriteFile(path, []byte("different"), 0o600)
	if err := initialize(root, release, fixed(files)); err == nil || !strings.Contains(err.Error(), "files changed") {
		t.Fatalf("changed: %v", err)
	}
	_ = os.Remove(path)
	if err := initialize(root, release, fixed(files)); err == nil || !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	_ = os.WriteFile(path, original, 0o600)
	release.revision = strings.Repeat("f", 40)
	if err := initialize(root, release, fixed(files)); err == nil || !strings.Contains(err.Error(), "another release") {
		t.Fatalf("foreign: %v", err)
	}
}

func TestInterruptedInitializationKeepsGeneratedKeys(t *testing.T) {
	root, release, files := initFixture(t)
	if err := initialize(root, release, fixed(files)); err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(filepath.Join(root, "secrets", "web", "core.key"))
	_ = os.Remove(filepath.Join(root, "installation.json"))
	if err := initialize(root, release, fixed(files)); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(root, "secrets", "web", "core.key")); !bytes.Equal(key, again) {
		t.Fatal("re-initialization replaced the core key")
	}
}

func metadataDirectory(t *testing.T, files map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	sums := ""
	for _, name := range releaseMembers {
		if name == "SHA256SUMS" {
			continue
		}
		sum := sha256.Sum256(files[name])
		sums += hex.EncodeToString(sum[:]) + "  " + name + "\n"
	}
	for name, data := range files {
		if name == "SHA256SUMS" {
			data = []byte(sums)
		}
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestReadBundledReleaseRejectsCorruptMissingAndForeignMetadata(t *testing.T) {
	_, release, files := initFixture(t)
	root := metadataDirectory(t, files)
	got, err := readRelease(root, release)
	if err != nil || len(got) != len(releaseMembers) {
		t.Fatalf("files = %v, err = %v", got, err)
	}
	path := filepath.Join(root, "node-install.pyz")
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRelease(root, release); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("corrupt: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := readRelease(root, release); !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	root = metadataDirectory(t, files)
	release.revision = strings.Repeat("f", 40)
	if _, err := readRelease(root, release); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("foreign: %v", err)
	}
}

func TestInitRejectsMismatchedImageBeforeTouchingData(t *testing.T) {
	t.Setenv("OAC_REVISION", strings.Repeat("f", 40))
	if err := initCommand(); err == nil || !strings.Contains(err.Error(), "image does not match") {
		t.Fatalf("err = %v", err)
	}
}

func captureInitLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(log.NewContextHandler(slog.NewJSONHandler(&output, nil))))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

func TestInitializationLogsLifecycleWithoutCredentials(t *testing.T) {
	root, release, files := initFixture(t)
	output := captureInitLogs(t)
	if err := initialize(root, release, fixed(files)); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"Initialization started", "Node metadata file copied", "Credential file generated", "Installation initialized", "Initialization completed"} {
		if !strings.Contains(output.String(), message) {
			t.Fatalf("missing %s", message)
		}
	}
	var traceID string
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["component"] != "oac-init" || entry["revision"] != release.revision {
			t.Fatalf("missing initialization identity: %v", entry)
		}
		trace, ok := entry["trace_id"].(string)
		if !ok || trace == "" {
			t.Fatal("missing trace ID")
		}
		if traceID == "" {
			traceID = trace
		}
		if traceID != trace {
			t.Fatal("initialization logs have different trace IDs")
		}
		if duration, ok := entry["duration_ms"].(float64); ok && duration < 0 {
			t.Fatal("negative duration")
		}
	}
	for _, name := range []string{"secrets/web/core.key", "secrets/database/password", "secrets/core/credential.key", "secrets/core/core-key-digests.json", "secrets/agent-host/identity.json"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{strings.TrimSpace(string(data)), keyDigest(strings.TrimSpace(string(data)))} {
			if strings.Contains(output.String(), value) {
				t.Fatalf("credential or digest leaked from %s", name)
			}
		}
	}
	output.Reset()
	if err := initialize(root, release, refuseDownload(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Existing installation verified") || strings.Contains(output.String(), "Credential file generated") {
		t.Fatal("restart logs do not describe verification only")
	}
	output.Reset()
	if err := os.Remove(filepath.Join(root, "installation.json")); err != nil {
		t.Fatal(err)
	}
	if err := initialize(root, release, fixed(files)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Credential file retained") || strings.Contains(output.String(), "Credential file generated") {
		t.Fatal("interrupted initialization logs do not describe credential reuse")
	}
}

func TestInitializationLogsFailureStep(t *testing.T) {
	root, release, _ := initFixture(t)
	output := captureInitLogs(t)
	failure := errors.New("invalid bundled metadata")
	if err := initialize(root, release, func() (map[string][]byte, error) { return nil, failure }); !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["level"] != "ERROR" || entry["step"] != "verify_bundled_metadata" || entry["error"] != failure.Error() {
		t.Fatalf("failure log = %v", entry)
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Fatal("failure duration missing")
	}
	if strings.Contains(output.String(), "Initialization completed") || strings.Contains(output.String(), "Credential file generated") {
		t.Fatal("failure logged success or generated credentials")
	}
}

func TestInitializationRetainsRotatedKeyAndRepairsItsDerivedDigest(t *testing.T) {
	root, release, files := initFixture(t)
	if err := initialize(root, release, fixed(files)); err != nil {
		t.Fatal(err)
	}
	key, err := generateCoreKey()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate interruption after publishing the new key but before its digest.
	if err := writeOwned(filepath.Join(root, "secrets", "web", "core.key"), []byte(key+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := initialize(root, release, refuseDownload(t)); err != nil {
		t.Fatal(err)
	}
	after, _ := coreKey(root)
	if after != key {
		t.Fatal("rotated key replaced")
	}
	digest, _ := os.ReadFile(filepath.Join(root, "secrets", "core", "core-key-digests.json"))
	if !strings.Contains(string(digest), keyDigest(key)) {
		t.Fatal("derived digest not repaired")
	}
}
