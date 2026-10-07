package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"
)

func TestBootstrapConnectionDoesNotReadOrOverwritePrivateProfile(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	profile, err := paths.AuthFile("default")
	if err != nil {
		t.Fatal(err)
	}
	prior := []byte(`{"server_url":"https://other.example/api/v1","runtime_id":"retained","runner_credential":"retained-secret"}`)
	if err = os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(profile, prior, 0o600); err != nil {
		t.Fatal(err)
	}
	input := runtimebootstrap.Connection{Version: runtimebootstrap.Version, CoreURL: "https://core.example/api/v1", DeviceID: "da912024-1543-4242-a2c1-5f4f7ebbc6c7", Credential: "bootstrap-secret"}
	raw, err := input.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "connection.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		p, err := bootstrapProfile(path)
		if err != nil || p.ServerURL != input.CoreURL || p.RuntimeID != input.DeviceID || p.RunnerCredential != input.Credential {
			t.Fatal("failed bootstrap/restart", err)
		}
	}
	got, err := os.ReadFile(profile)
	if err != nil || !bytes.Equal(got, prior) {
		t.Fatal("private profile modified", err)
	}
	if err = os.WriteFile(path, []byte("bootstrap-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = bootstrapProfile(path); err == nil || strings.Contains(err.Error(), input.Credential) {
		t.Fatal("invalid input fell back or leaked")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = bootstrapProfile(path); err == nil {
		t.Fatal("missing input fell back to private auth")
	}
}

func TestConnectBootstrapRejectsOtherCredentialSourcesBeforeSideEffects(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	ctx := &runContext{stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	for _, args := range [][]string{
		{"--remote", "wss://core.example/api/v1/agent-daemon/ws"},
		{"--credential-file", "/credential.json"},
		{"--environment-id", "foreign"}, {"unexpected"},
	} {
		err := runConnect(ctx, append([]string{"--bootstrap-file", "/not-present"}, args...))
		if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatal("mixed startup source accepted", err)
		}
	}
	path, err := paths.AuthFile("default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created private state")
	}
}
