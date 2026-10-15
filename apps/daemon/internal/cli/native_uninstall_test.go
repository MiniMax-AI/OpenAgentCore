package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The native daemon ends with the fix in its error when Core rejects its
// credential, so `start` reports it instead of parking.
func TestNativeStartExitsOnPermanentRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	environment, keyID := uuid.NewString(), uuid.NewString()
	credential := filepath.Join(home, "executor-key.json")
	raw, _ := json.Marshal(map[string]string{"key_id": keyID, "environment_id": environment, "executor_token": "private-canary"})
	if err := os.WriteFile(credential, raw, 0600); err != nil {
		t.Fatal(err)
	}
	rc := &runContext{stdout: new(bytes.Buffer), stderr: new(bytes.Buffer)}
	err := runEnvironmentConnect(t.Context(), rc, "default", false, true, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/agent-daemon/ws", environment, credential)
	if err == nil || !strings.Contains(err.Error(), "run oac-daemon uninstall") || !strings.Contains(err.Error(), keyID) || strings.Contains(err.Error(), "private-canary") {
		t.Fatalf("rejection: %v", err)
	}
}

func TestUninstallDeletesInstallationAndKeepsWorkspace(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	workspace := args[slices.Index(args, "--workspace")+1]
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "native-download")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	rc = &runContext{stdin: strings.NewReader("n\n"), stdout: output, stderr: output}
	if err := runUninstall(rc, nil); err == nil || !strings.Contains(output.String(), root) {
		t.Fatalf("declined uninstall: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "daemon", "installation.json")); err != nil {
		t.Fatal("declining deleted the installation")
	}
	output.Reset()
	rc.stdin = strings.NewReader("y\n")
	if err := runUninstall(rc, nil); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Revoke", "Executor credentials", "Delete the Session"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("missing %q in\n%s", text, output)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("installation directory remains")
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatal("workspace was deleted")
	}
	output.Reset()
	if err := runUninstall(rc, nil); err != nil || !strings.Contains(output.String(), "nothing is installed") {
		t.Fatalf("second run: %v\n%s", err, output)
	}
}

// Windows cannot delete its running executable; everything else must go.
func TestRemoveInstallationKeepsOnlyRunningExecutable(t *testing.T) {
	dir := t.TempDir()
	running := filepath.Join(dir, "bin", "oac-daemon.exe")
	for _, path := range []string{running, filepath.Join(dir, "bin", "other"), filepath.Join(dir, "daemon", "installation.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	remaining, err := removeInstallation(dir, running)
	if err != nil || !remaining {
		t.Fatalf("remaining=%v err=%v", remaining, err)
	}
	var left []string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, _ error) error {
		if !entry.IsDir() {
			left = append(left, path)
		}
		return nil
	})
	if len(left) != 1 || left[0] != running {
		t.Fatalf("left behind: %v", left)
	}
}
