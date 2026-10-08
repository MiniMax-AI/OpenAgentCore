package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func nativeInstallFixture(t *testing.T) (*runContext, []string, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux amd64 installer")
	}
	root, bundle, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	// Fixed machine directories are exercised separately; each installer fixture
	// prepares its declared workspace without changing this development machine.
	previous := prepareNativeEnvironment
	prepareNativeEnvironment = func(workspace string) error { return os.MkdirAll(workspace, 0700) }
	t.Cleanup(func() { prepareNativeEnvironment = previous })
	key := filepath.Join(root, "credential.json")
	environment := uuid.NewString()
	body, _ := json.Marshal(map[string]string{"key_id": uuid.NewString(), "executor_token": "private-test-credential", "environment_id": environment})
	if err := os.WriteFile(key, body, 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(nativeBundle{Schema: 1, DaemonVersion: Version, OS: runtime.GOOS, Arch: runtime.GOARCH})
	if err := os.WriteFile(filepath.Join(bundle, "bundle.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range nativeBundlePrograms {
		if err := os.WriteFile(filepath.Join(bundle, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	output := new(bytes.Buffer)
	rc := &runContext{stdin: strings.NewReader("must not read"), stdout: output, stderr: output}
	args := []string{"--non-interactive", "--bundle-dir", bundle, "--remote", "ws://localhost:12345/api/v1/agent-daemon/ws", "--environment-id", environment, "--workspace", workspace, "--credential-file", key}
	return rc, args, root, bundle
}

func TestNativeInstallationReuseAndVersionRejection(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "daemon", "installation.json")
	first, _ := os.ReadFile(file)
	if err := runInstall(rc, args); err != nil {
		t.Fatalf("compatible rerun: %v", err)
	}
	again, _ := os.ReadFile(file)
	if !bytes.Equal(first, again) {
		t.Fatal("reuse changed settings")
	}
	var persisted map[string]any
	if err := json.Unmarshal(first, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 4 {
		t.Fatalf("unexpected persisted settings: %v", persisted)
	}
	if strings.Contains(rc.stdout.(*bytes.Buffer).String(), "private-test-credential") {
		t.Fatal("credential leaked")
	}
	if err := runInstall(rc, append(args, "--remote", "ws://localhost:4321/api/v1/agent-daemon/ws")); err == nil {
		t.Fatal("replaced connection settings")
	}
	var installed nativeInstallation
	if err := readNativeJSON(file, &installed); err != nil {
		t.Fatal(err)
	}
	installed.Version = "old-version"
	raw, _ := json.Marshal(installed)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(rc, args); err == nil {
		t.Fatal("accepted historical installation")
	}
	if err := runStart(rc, []string{"--foreground"}); err == nil {
		t.Fatal("started historical installation")
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, raw) {
		t.Fatal("modified historical state")
	}
}

func TestNativeInstallationDoesNotRepairPrograms(t *testing.T) {
	for _, program := range []string{"oac-daemon", "oac-sandbox-io"} {
		for _, missing := range []bool{true, false} {
			t.Run(program+"/"+map[bool]string{true: "missing", false: "modified"}[missing], func(t *testing.T) {
				rc, args, root, _ := nativeInstallFixture(t)
				if err := runInstall(rc, args); err != nil {
					t.Fatal(err)
				}
				config := filepath.Join(root, "daemon", "installation.json")
				before, _ := os.ReadFile(config)
				binary := filepath.Join(root, "bin", program)
				var err error
				if missing {
					err = os.Remove(binary)
				} else {
					err = os.WriteFile(binary, []byte("modified"), 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := runInstall(rc, args); err == nil {
					t.Fatal("silently repaired installed executable")
				}
				if missing {
					if _, err := os.Stat(binary); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("recreated missing program")
					}
				} else {
					if raw, _ := os.ReadFile(binary); string(raw) != "modified" {
						t.Fatal("replaced modified program")
					}
				}
				if after, _ := os.ReadFile(config); !bytes.Equal(before, after) {
					t.Fatal("failure changed settings")
				}
			})
		}
	}
}

func TestNativeInstallationMissingInputAndSecrets(t *testing.T) {
	rc, _, root, _ := nativeInstallFixture(t)
	for _, args := range [][]string{{"--non-interactive"}, {"--non-interactive", "--remote", "ws://user:secret@host/x"}, {"--interactive=secret"}, {"--token", "private-test-credential"}, {"--harness", "codex"}, {"--capability-directory", "/tmp"}, {"--tool-env-file", "/tmp"}} {
		err := runInstall(rc, args)
		if err == nil {
			t.Fatal("accepted incomplete or obsolete input")
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-test-credential") {
			t.Fatal("echoed sensitive argument")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "daemon", "installation.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete input wrote installation")
	}
}

func TestNativeInstallationLockAndPlatformMismatch(t *testing.T) {
	rc, args, root, bundle := nativeInstallFixture(t)
	_, unlock, err := lockNativeInstallation(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = runInstall(rc, args); err == nil {
		t.Fatal("concurrent installation accepted")
	}
	unlock()
	var b nativeBundle
	if err := readNativeJSON(filepath.Join(bundle, "bundle.json"), &b); err != nil {
		t.Fatal(err)
	}
	b.Arch = "arm64"
	raw, _ := json.Marshal(b)
	if err := os.WriteFile(filepath.Join(bundle, "bundle.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(rc, args); err == nil {
		t.Fatal("foreign bundle accepted")
	}
}

func TestNativeInstallationDirectoryFailurePrecedesClaim(t *testing.T) {
	rc, _, root, bundle := nativeInstallFixture(t)
	environment := uuid.NewString()
	claims := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/claim") {
			claims++
			w.WriteHeader(204)
			return
		}
		t.Error("unexpected request")
	}))
	defer server.Close()
	prepareNativeEnvironment = func(string) error { return errors.New("directory not writable") }
	options := nativeInstallOptions{nativeInstallation: nativeInstallation{Environment: environment, Remote: "ws://localhost:12345/api/v1/agent-daemon/ws"}, Directory: root, Bundle: bundle, Workspace: "/workspace", OnboardURL: server.URL}
	if err := installNativeOptions(t.Context(), rc, &options); err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("wrong failure: %v", err)
	}
	if claims != 0 {
		t.Fatal("claimed credential before directory preparation")
	}
	if _, err := os.Stat(filepath.Join(root, "daemon", "executor-credential.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("credential created before directory preparation")
	}
}

func TestNativeUnsupportedPlatformPrecedesInputAndState(t *testing.T) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		t.Skip("unsupported-platform build")
	}
	rc := &runContext{stdout: io.Discard, stderr: io.Discard}
	t.Setenv("OAC_RUNTIME_HOME", filepath.Join(t.TempDir(), "absent"))
	for _, run := range []func(*runContext, []string) error{runInstall, runStart} {
		err := run(rc, []string{"--invalid"})
		var platform *UnsupportedPlatformError
		if !errors.As(err, &platform) {
			t.Fatalf("expected typed platform error before parsing: %v", err)
		}
	}
}

func TestNativeDirectoryFailureIdentifiesPreparation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(file, "workspace")
	err := nativeInstallError(prepareNativeEnvironmentDirectories(directory))
	if err == nil || !strings.Contains(err.Error(), directory) || !strings.Contains(err.Error(), "current account") {
		t.Fatalf("not actionable: %v", err)
	}
}
