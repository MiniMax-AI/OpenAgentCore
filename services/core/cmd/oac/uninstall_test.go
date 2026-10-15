package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testInstallationID = "0b0c8c0e-5a45-4b3a-9d3e-6a1d4f1f2a10"

type uninstallFixture struct {
	dir, keep string
	calls     []string
	downErr   error
}

func newUninstallFixture(t *testing.T) *uninstallFixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "core")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{".env": "COMPOSE_PROJECT_NAME=oac-abc123\nOAC_WEB_PORT='8080'\n", "compose.yaml": "services: {}\n", "oac": "binary"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(dir+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	return &uninstallFixture{dir: dir}
}

func (f *uninstallFixture) Run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, strings.Join(args, " "))
	if args[0] == "down" {
		return f.downErr
	}
	return nil
}

func (f *uninstallFixture) Output(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if args[0] == "run" {
		return []byte(testInstallationID + "\n"), nil
	}
	return []byte("ghcr.io/example/core:1\npostgres:16-alpine\n"), nil
}

func (f *uninstallFixture) uninstall(answer string) (string, error) {
	var out strings.Builder
	project, err := installedProject(f.dir)
	if err != nil {
		return "", err
	}
	err = uninstall(context.Background(), f, project, f.dir, f.keep, strings.NewReader(answer), &out)
	return out.String(), err
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestUninstallRefusedConfirmationChangesNothing(t *testing.T) {
	f := newUninstallFixture(t)
	out, err := f.uninstall("no\n")
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatal(err)
	}
	if !strings.Contains(out, testInstallationID) || !strings.Contains(out, "oac-abc123_data") {
		t.Fatal("the prompt must show what is deleted:", out)
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "down") {
			t.Fatal("down ran without confirmation")
		}
	}
	if !exists(f.dir) || !exists(f.dir+".lock") {
		t.Fatal("a refused uninstall removed files")
	}
}

func TestUninstallRemovesProjectDirectoryAndLock(t *testing.T) {
	for _, answer := range []string{"yes\n", "core\n"} {
		f := newUninstallFixture(t)
		out, err := f.uninstall(answer)
		if err != nil {
			t.Fatal(err)
		}
		if f.calls[len(f.calls)-1] != "down --volumes --remove-orphans" {
			t.Fatal(f.calls)
		}
		if exists(f.dir) || exists(f.dir+".lock") {
			t.Fatal("installation files remain")
		}
		for _, want := range []string{"Installation ID: " + testInstallationID, "docker image rm ghcr.io/example/core:1 postgres:16-alpine", "E2B"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q in %s", want, out)
			}
		}
		// The second run finds no .env and reports nothing installed.
		if _, err := installedProject(f.dir); !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestUninstallKeepsTheDirectoryWhenDockerFails(t *testing.T) {
	f := newUninstallFixture(t)
	f.downErr = os.ErrPermission
	if _, err := f.uninstall("yes\n"); err == nil || !strings.Contains(err.Error(), "rerun oac uninstall") {
		t.Fatal(err)
	}
	if !exists(filepath.Join(f.dir, ".env")) {
		t.Fatal("the directory was removed before Docker succeeded")
	}
}

func TestUninstallLeavesOnlyTheRunningBinary(t *testing.T) {
	f := newUninstallFixture(t)
	f.keep = "oac"
	out, err := f.uninstall("yes\n")
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(f.dir)
	if len(entries) != 1 || entries[0].Name() != "oac" || !strings.Contains(out, "Remove-Item") || exists(f.dir+".lock") {
		t.Fatal(entries, out)
	}
}

func TestInstallPortHintNamesAnOrphanedProject(t *testing.T) {
	f := newInstallFixture(t)
	listener, err := net.Listen("tcp", f.options.host+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	f.options.port = listener.Addr().(*net.TCPAddr).Port
	inner := f.docker
	f.docker = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if args[0] == "ps" {
			return []byte("oac-abc123\n"), nil
		}
		return inner(ctx, dir, args...)
	}
	err = f.run()
	if err == nil || !strings.Contains(err.Error(), "docker compose -p oac-abc123 down --volumes --remove-orphans") {
		t.Fatal(err)
	}
}
