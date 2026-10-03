package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		saved[relative] = string(data)
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
	for _, name := range []string{"secrets/web/core.key", "secrets/database/password", "secrets/core/credential.key"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.Mode().Perm() != 0o600 {
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
