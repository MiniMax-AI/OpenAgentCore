package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/auth"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"github.com/google/uuid"
)

func TestBootstrapConnectionDoesNotReadOrOverwritePrivateProfile(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	prior := auth.Profile{ServerURL: "https://other.example/api/v1", RuntimeID: "retained", RunnerCredential: "retained-secret"}
	profileDir, err := paths.ProfileDir("default")
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimefs.EnsurePrivateDir(profileDir); err != nil {
		t.Fatal(err)
	}
	priorRaw, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(profileDir, "auth.json"), priorRaw, 0600); err != nil {
		t.Fatal(err)
	}
	input := runtimebootstrap.Connection{Version: runtimebootstrap.Version, CoreURL: "https://core.example/api/v1", DeviceID: "da912024-1543-4242-a2c1-5f4f7ebbc6c7", Credential: "bootstrap-secret",
		EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Workspace: t.TempDir()}
	raw, err := input.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "connection.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(localworkspace.Unbind)
	for range 2 {
		p, _, err := bootstrapProfile(path)
		if err != nil || p.ServerURL != input.CoreURL || p.RuntimeID != input.DeviceID || p.RunnerCredential != input.Credential {
			t.Fatal("failed bootstrap/restart", err)
		}
		if bound := localworkspace.Current(); bound == nil || bound.Workspace() != input.Workspace || bound.Environment() != input.EnvironmentID {
			t.Fatal("bootstrap did not bind the workspace")
		}
	}
	got, err := auth.Load("default")
	if err != nil || got != prior {
		t.Fatal("private profile modified", err)
	}
	if err = os.WriteFile(path, []byte("bootstrap-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = bootstrapProfile(path); err == nil || strings.Contains(err.Error(), input.Credential) {
		t.Fatal("invalid input fell back or leaked")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err = bootstrapProfile(path); err == nil {
		t.Fatal("missing input fell back to private auth")
	}
}

func TestConnectBootstrapRejectsOtherCredentialSourcesBeforeSideEffects(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	ctx := &runContext{stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	for _, args := range [][]string{
		{"--remote", "wss://core.example/api/v1/agent-daemon/ws"},
		{"--credential-file", "/credential.json"},
		{"--environment-id", "foreign"}, {"--workspace", "/workspace"}, {"unexpected"},
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
