package auth_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/auth"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", dir)
	return dir
}

func profileDir(t *testing.T, profile string) string {
	t.Helper()
	dir, err := paths.ProfileDir(profile)
	if err != nil {
		t.Fatalf("ProfileDir: %v", err)
	}
	if err := runtimefs.EnsurePrivateDir(dir); err != nil {
		t.Fatalf("EnsurePrivateDir: %v", err)
	}
	return dir
}

func writeProfile(t *testing.T, profile string, p auth.Profile) {
	t.Helper()
	dir := profileDir(t, profile)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
}

func TestLoadReadsProfile(t *testing.T) {
	_ = withTempHome(t)
	want := auth.Profile{
		ServerURL:        "https://core.example.com",
		RuntimeID:        "rt_abc123",
		RunnerCredential: "secret-credential",
		DeviceName:       "alice-mac",
	}
	writeProfile(t, "test", want)
	got, err := auth.Load("test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("Load mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestLoadIgnoresLegacyProfileFields(t *testing.T) {
	_ = withTempHome(t)
	raw := `{"server_url":"https://core.example.com/api/v1","runtime_id":"rt","runner_credential":"c","device_name":"d",` +
		`"hostname":"h","paired_at":"2026-06-04T12:00:00Z","runner_public_key":"pub","runner_private_key":"priv"}`
	if err := os.WriteFile(filepath.Join(profileDir(t, "legacy"), "auth.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	got, err := auth.Load("legacy")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := auth.Profile{ServerURL: "https://core.example.com/api/v1", RuntimeID: "rt", RunnerCredential: "c", DeviceName: "d"}
	if got != want {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadMissingReturnsErrNotPaired(t *testing.T) {
	_ = withTempHome(t)
	_, err := auth.Load("default")
	if !errors.Is(err, auth.ErrNotPaired) {
		t.Fatalf("Load on missing profile returned %v, want ErrNotPaired", err)
	}
	// ErrNotPaired must also wrap fs.ErrNotExist for the canonical
	// "missing file" check.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ErrNotPaired must wrap fs.ErrNotExist, got %v", err)
	}
}

func TestLoadCorruptJSONReturnsError(t *testing.T) {
	_ = withTempHome(t)
	dir := profileDir(t, "default")
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	_, err := auth.Load("default")
	if err == nil {
		t.Fatal("Load returned nil error on corrupt JSON")
	}
	if errors.Is(err, auth.ErrNotPaired) {
		t.Fatalf("Load on corrupt JSON should not be ErrNotPaired: %v", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	_ = withTempHome(t)
	if err := auth.Delete("default"); err != nil {
		t.Fatalf("Delete on missing profile returned %v, want nil (idempotent)", err)
	}
	writeProfile(t, "default", auth.Profile{ServerURL: "https://x", RuntimeID: "rt", RunnerCredential: "c"})
	if err := auth.Delete("default"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := auth.Load("default"); !errors.Is(err, auth.ErrNotPaired) {
		t.Fatalf("Load after Delete = %v, want ErrNotPaired", err)
	}
	// Second Delete must still succeed.
	if err := auth.Delete("default"); err != nil {
		t.Fatalf("Delete second call = %v, want nil", err)
	}
}
