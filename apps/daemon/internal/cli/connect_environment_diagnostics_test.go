package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/transport"
)

// connect.log must say why the connection failed, and never carry the credential.
func TestEnvironmentDialErrorKeepsCause(t *testing.T) {
	err := environmentDialError(errors.New("transport.Dial: ws upgrade rejected with HTTP 502"))
	if message := err.Error(); !strings.Contains(message, "Environment connection failed") || !strings.Contains(message, "HTTP 502") {
		t.Fatalf("cause lost: %q", message)
	}
	if errors.Is(err, transport.ErrPermanent) {
		t.Fatal("a transient failure became permanent")
	}
}

// The installed bin/oac-daemon acts on its own installation even when
// OAC_RUNTIME_HOME names another directory.
func TestInstalledDaemonIgnoresRuntimeHomeEnvironment(t *testing.T) {
	if os.Getenv("OAC_TEST_INSTALLED_STATUS") != "" {
		if err := Execute([]string{"status"}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	installation := t.TempDir()
	if err := os.MkdirAll(filepath.Join(installation, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installation, "daemon"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installation, "daemon", "installation.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(installation, "bin", nativeExe("oac-daemon"))
	if err = os.WriteFile(executable, raw, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestInstalledDaemonIgnoresRuntimeHomeEnvironment$")
	cmd.Env = append(os.Environ(), "OAC_TEST_INSTALLED_STATUS=1", "OAC_RUNTIME_HOME="+t.TempDir())
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(installation, "daemon")
	if !strings.Contains(string(output), want) {
		t.Fatalf("status did not use the installation %s:\n%s", want, output)
	}
}

// A claim refused as already taken names the working recovery and leaves no
// credential file behind in the directory.
func TestClaimConflictNamesRecoveryAndRemovesCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusConflict) }))
	defer server.Close()
	directory := t.TempDir()
	held, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	o := &nativeInstallOptions{OnboardURL: server.URL + "/api/v1/agent-daemon/installation", Authorization: "grant"}
	o.Directory, o.Environment = directory, "11111111-1111-4111-8111-111111111111"
	err = prepareOnboardingCredential(t.Context(), o, held)
	if err == nil || !strings.Contains(err.Error(), "already claimed") || !strings.Contains(err.Error(), "Rotate") || !strings.Contains(err.Error(), "Reconnect an existing Session") {
		t.Fatalf("conflict message: %v", err)
	}
	if _, err := held.Stat("executor-credential.json"); !os.IsNotExist(err) {
		t.Fatal("unclaimed credential file was kept")
	}
}
