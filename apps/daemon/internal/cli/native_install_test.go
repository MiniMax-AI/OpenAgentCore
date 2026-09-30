package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func nativeInstallFixture(t *testing.T) (*runContext, []string, string, string) {
	t.Helper()
	root := t.TempDir()
	bundle := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	key := filepath.Join(root, "credential.json")
	environment := uuid.NewString()
	body, _ := json.Marshal(map[string]string{"key_id": uuid.NewString(), "executor_token": "private-test-credential", "environment_id": environment})
	if err := os.WriteFile(key, body, 0600); err != nil {
		t.Fatal(err)
	}
	b := nativeBundle{Schema: 1, DaemonVersion: Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]nativeComponent{}}
	for _, name := range []string{"node", "codex", "claude"} {
		data := []byte("fixture " + name)
		sum := sha256.Sum256(data)
		b.Components[name] = nativeComponent{Version: nativePins[name], Files: map[string]nativeFile{"program": {SHA256: hex.EncodeToString(sum[:]), Executable: true}}}
		dir := nativeComponentRoot(bundle, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "program"), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(b)
	if err := os.WriteFile(filepath.Join(bundle, "bundle.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	old := probeNativeInstallation
	probeNativeInstallation = func(context.Context, string, []string) error { return nil }
	t.Cleanup(func() { probeNativeInstallation = old })
	output := new(bytes.Buffer)
	rc := &runContext{stdin: strings.NewReader("must not read"), stdout: output, stderr: output}
	args := []string{"--non-interactive", "--bundle-dir", bundle, "--harness", "codex", "--remote", "ws://localhost:12345/api/v1/agent-daemon/ws", "--environment-id", environment, "--workspace", workspace, "--credential-file", key}
	return rc, args, root, bundle
}

func TestNativeInstallationAdditiveReuseAndVersionRejection(t *testing.T) {
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
		t.Fatal("reuse rewrote settings")
	}
	if err := runInstall(rc, append(args, "--harness", "claude")); err != nil {
		t.Fatal(err)
	}
	var installed nativeInstallation
	if err := readNativeJSON(file, &installed); err != nil {
		t.Fatal(err)
	}
	if strings.Join(installed.Harnesses, ",") != "claude,codex" {
		t.Fatal("lost or failed to add Harness")
	}
	if strings.Contains(rc.stdout.(*bytes.Buffer).String(), "private-test-credential") {
		t.Fatal("credential leaked")
	}
	if err := runInstall(rc, append(args, "--workspace", t.TempDir())); err == nil {
		t.Fatal("replaced connection settings")
	}
	installed.Version = "old-version"
	raw, _ := json.Marshal(installed)
	_ = os.WriteFile(file, raw, 0600)
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

func TestNativeInstallationFailureAndRecoveryPreserveExisting(t *testing.T) {
	rc, args, root, bundle := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "daemon", "installation.json")
	before, _ := os.ReadFile(file)
	probeNativeInstallation = func(context.Context, string, []string) error { return errors.New("synthetic unavailable") }
	if err := runInstall(rc, append(args, "--harness", "claude")); err == nil {
		t.Fatal("published failed installation")
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Fatal("failed addition changed settings")
	}
	probeNativeInstallation = func(context.Context, string, []string) error { return nil }
	if err := runInstall(rc, append(args, "--harness", "claude")); err != nil {
		t.Fatalf("could not reuse complete unpublished component: %v", err)
	}
	// Source corruption cannot affect an existing component, and cannot be used
	// to repair a modified installed one silently.
	_ = os.WriteFile(filepath.Join(nativeComponentRoot(root, "codex"), "program"), []byte("changed"), 0700)
	if err := runInstall(rc, args); err == nil {
		t.Fatal("silently repaired modified executable")
	}
	if got, _ := os.ReadFile(filepath.Join(nativeComponentRoot(root, "codex"), "program")); string(got) != "changed" {
		t.Fatal("overwrote installed data")
	}
	_ = bundle
}

func TestNativeInstallationMissingInputAndSecrets(t *testing.T) {
	rc, _, root, _ := nativeInstallFixture(t)
	for _, args := range [][]string{{"--non-interactive"}, {"--non-interactive", "--remote", "ws://user:secret@host/x"}, {"--interactive=secret"}, {"--token", "private-test-credential"}} {
		err := runInstall(rc, args)
		if err == nil {
			t.Fatal("accepted incomplete input")
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-test-credential") {
			t.Fatal("echoed sensitive argument")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "daemon", "installation.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete input wrote installation")
	}
}

func TestNativeInstallationInteractiveMultipleHarnesses(t *testing.T) {
	rc, args, root, bundle := nativeInstallFixture(t)
	var o nativeInstallOptions
	// Prompt and parameter paths feed the same installer options.
	o.Directory = root
	o.Bundle = bundle
	// Existing connection fields need no second interactive input.
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--remote":
			o.Remote = args[i+1]
		case "--environment-id":
			o.Environment = args[i+1]
		case "--workspace":
			o.Workspace = args[i+1]
		case "--credential-file":
			o.Credential = args[i+1]
		}
	}
	input := "codex,claude\n\n\n\n\n"
	if err := promptNativeInstall(strings.NewReader(input), rc.stdout, &o); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(rc, append(args, "--harness", o.Harness)); err != nil {
		t.Fatal(err)
	}
	var c nativeInstallation
	_ = readNativeJSON(filepath.Join(root, "daemon", "installation.json"), &c)
	if len(c.Harnesses) != 2 {
		t.Fatal("multi-selection not installed")
	}
	if err := promptNativeInstall(strings.NewReader(""), rc.stdout, &nativeInstallOptions{}); err == nil {
		t.Fatal("EOF accepted")
	}
}

func TestNativeInstallationLockAndManifestContainment(t *testing.T) {
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
	_ = readNativeJSON(filepath.Join(bundle, "bundle.json"), &b)
	c := b.Components["codex"]
	c.Files["../escaped"] = c.Files["program"]
	b.Components["codex"] = c
	raw, _ := json.Marshal(b)
	_ = os.WriteFile(filepath.Join(bundle, "bundle.json"), raw, 0600)
	if err = runInstall(rc, args); err == nil {
		t.Fatal("accepted manifest traversal")
	}
	if _, err = os.Stat(filepath.Join(root, "components")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("manifest validation happened after mutation")
	}
}

func TestNativeInstallationPlatformMismatchAndUnsupportedSelection(t *testing.T) {
	rc, args, _, bundle := nativeInstallFixture(t)
	var b nativeBundle
	_ = readNativeJSON(filepath.Join(bundle, "bundle.json"), &b)
	b.OS = "other"
	raw, _ := json.Marshal(b)
	_ = os.WriteFile(filepath.Join(bundle, "bundle.json"), raw, 0600)
	if err := runInstall(rc, args); err == nil {
		t.Fatal("foreign bundle accepted")
	}
	if _, err := selectedNativeHarnesses("anything"); err == nil {
		t.Fatal("unknown Harness accepted")
	}
	if runtime.GOOS == "windows" {
		if _, err := selectedNativeHarnesses("minimax"); err == nil {
			t.Fatal("unsupported Windows MiniMax accepted")
		}
	}
}

func TestNativeInstallationDoesNotRepairMissingDaemon(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bin", nativeExe("oac-daemon"))
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(rc, args); err == nil {
		t.Fatal("silently repaired a modified installation")
	}
	if _, err := os.Stat(binary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recreated a removed daemon")
	}
}

func TestNativeInstallationConnectCannotBypassValidation(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"installed", "missing-component"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "missing-component" {
				if err := os.Remove(filepath.Join(nativeComponentRoot(root, "codex"), "program")); err != nil {
					t.Fatal(err)
				}
			}
			for _, connection := range [][]string{
				nil,
				{"--remote", "ws://127.0.0.1:1/api/v1/agent-daemon/ws"},
				{"--url", "http://127.0.0.1:1", "--token", "private-test-token"},
			} {
				err := runConnect(rc, connection)
				if err == nil || !strings.Contains(err.Error(), "use oac-daemon start") || strings.Contains(err.Error(), "private-test-token") {
					t.Fatal("connect bypassed native installation validation", err)
				}
			}
		})
	}
}
