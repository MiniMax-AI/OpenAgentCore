package auth_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/auth"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
)

func writeProfile(t *testing.T, raw string) {
	t.Helper()
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	path, err := paths.AuthFile("default")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadReadsTheDeviceProfile(t *testing.T) {
	writeProfile(t, `{"server_url":"https://core.example.com/api/v1","runtime_id":"rt_abc123","runner_credential":"secret-credential","device_name":"engine host"}`)
	got, err := auth.Load("default")
	want := auth.Profile{ServerURL: "https://core.example.com/api/v1", RuntimeID: "rt_abc123", RunnerCredential: "secret-credential", DeviceName: "engine host"}
	if err != nil || got != want {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
}

func TestLoadMissingReturnsErrNoProfile(t *testing.T) {
	writeProfile(t, "")
	_, err := auth.Load("default")
	if !errors.Is(err, auth.ErrNoProfile) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load on missing profile returned %v, want ErrNoProfile wrapping fs.ErrNotExist", err)
	}
}

func TestLoadCorruptJSONReturnsError(t *testing.T) {
	writeProfile(t, "{not valid json")
	if _, err := auth.Load("default"); err == nil || errors.Is(err, auth.ErrNoProfile) {
		t.Fatalf("Load on corrupt JSON = %v, want a parse error", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	writeProfile(t, `{"server_url":"https://x","runtime_id":"rt","runner_credential":"c"}`)
	for range 2 {
		if err := auth.Delete("default"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	if _, err := auth.Load("default"); !errors.Is(err, auth.ErrNoProfile) {
		t.Fatalf("Load after Delete = %v, want ErrNoProfile", err)
	}
}
