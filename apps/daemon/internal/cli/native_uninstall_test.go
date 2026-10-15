package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	credential := args[slices.Index(args, "--credential-file")+1]
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	// A file and a directory the installation does not own.
	foreignFile, foreignDir := filepath.Join(root, "notes.txt"), filepath.Join(root, "core")
	if err := os.MkdirAll(foreignDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	rc = &runContext{stdin: strings.NewReader("n\n"), stdout: output, stderr: output}
	if err := runUninstall(rc, nil); err == nil || !strings.Contains(output.String(), filepath.Join(root, "bin")) {
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
	for _, text := range []string{"Revoke", "Executor credentials", "Delete the Session", "Kept: " + foreignFile, "Kept: " + foreignDir, "Kept: " + credential, "Rotate"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("missing %q in\n%s", text, output)
		}
	}
	for _, owned := range nativeInstallationEntries {
		if _, err := os.Lstat(filepath.Join(root, owned)); !os.IsNotExist(err) {
			t.Fatalf("%s remains", owned)
		}
	}
	for _, kept := range []string{workspace, foreignFile, foreignDir, credential} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was deleted", kept)
		}
	}
	output.Reset()
	if err := runUninstall(rc, nil); err != nil || !strings.Contains(output.String(), "nothing is installed") {
		t.Fatalf("second run: %v\n%s", err, output)
	}
}

// An installation with nothing else in its directory leaves no directory behind.
func TestUninstallRemovesEmptiedInstallationDirectory(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	rc = &runContext{stdin: strings.NewReader("y\n"), stdout: new(bytes.Buffer), stderr: new(bytes.Buffer)}
	if err := runUninstall(rc, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("installation directory remains")
	}
}

// A file that cannot be deleted fails the uninstall with the installation's
// record and executable still in place, so the command can run again.
func TestUninstallFailureCanBeRerun(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the user cannot write to")
	}
	rc, args, root, _ := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "components", "locked")
	if err := os.MkdirAll(locked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
	rc = &runContext{stdin: strings.NewReader("y\n"), stdout: new(bytes.Buffer), stderr: new(bytes.Buffer)}
	err := runUninstall(rc, nil)
	if err == nil || !strings.Contains(err.Error(), "run the command again") {
		t.Fatalf("uninstall: %v", err)
	}
	for _, path := range []string{filepath.Join(root, "daemon", "installation.json"), filepath.Join(root, "bin")} {
		if _, err = os.Stat(path); err != nil {
			t.Fatalf("%s was deleted before the failure: %v", path, err)
		}
	}
	if err = os.Chmod(locked, 0700); err != nil {
		t.Fatal(err)
	}
	rc = &runContext{stdin: strings.NewReader("y\n"), stdout: new(bytes.Buffer), stderr: new(bytes.Buffer)}
	if err = runUninstall(rc, nil); err != nil {
		t.Fatalf("rerun: %v", err)
	}
}

// Install writes only into an empty directory or an installation, never beside
// a Runtime home's other contents.
func TestInstallRefusesDirectoryHoldingOtherFiles(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "core"), 0700); err != nil {
		t.Fatal(err)
	}
	err := runInstall(rc, args)
	if err == nil || !strings.Contains(err.Error(), root) || !strings.Contains(err.Error(), "core") {
		t.Fatalf("install into a foreign directory: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, "daemon")); !os.IsNotExist(err) {
		t.Fatal("refused install still wrote into the directory")
	}
}

// Without --install-dir an installation gets its own directory in the Runtime home.
func TestInstallDefaultsToEnvironmentDirectory(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	args = slices.Delete(args, slices.Index(args, "--install-dir"), slices.Index(args, "--install-dir")+2)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	environment := args[slices.Index(args, "--environment-id")+1]
	if _, err := os.Stat(filepath.Join(root, "environments", environment, "daemon", "installation.json")); err != nil {
		t.Fatalf("installation is not in the default directory: %v", err)
	}
}

// Windows cannot delete its running executable; everything else must go.
func TestRemoveTreeKeepsOnlyRunningExecutable(t *testing.T) {
	dir := t.TempDir()
	running := filepath.Join(dir, "bin", "oac-daemon.exe")
	for _, path := range []string{running, filepath.Join(dir, "bin", "other")} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	keep, err := os.Stat(running)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := removeTree(filepath.Join(dir, "bin"), keep)
	if err != nil || !kept {
		t.Fatalf("kept=%v err=%v", kept, err)
	}
	if _, err = os.Stat(running); err != nil {
		t.Fatal("running executable was deleted")
	}
	if _, err = os.Stat(filepath.Join(dir, "bin", "other")); !os.IsNotExist(err) {
		t.Fatal("other file remains")
	}
}
