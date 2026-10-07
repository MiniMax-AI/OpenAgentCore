package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type installFixture struct {
	installer
	options installOptions
	calls   []string
	failure string
	cached  bool
}

func newInstallFixture(t *testing.T) *installFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	exe := filepath.Join(root, "source")
	if err := os.WriteFile(exe, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &installFixture{options: installOptions{dir: filepath.Join(root, "installation with spaces"), version: "latest", host: "127.0.0.1", port: port}}
	f.executable = exe
	compose := []byte("services: {}\n")
	f.download = func(_ context.Context, address, path string) error {
		if f.failure == "download" {
			return os.ErrPermission
		}
		raw := compose
		if strings.HasSuffix(address, "sums.txt") {
			raw = []byte(fmt.Sprintf("%x  compose.yaml\n", sha256.Sum256(compose)))
		}
		return os.WriteFile(path, raw, 0o600)
	}
	f.docker = func(_ context.Context, dir string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		f.calls = append(f.calls, command)
		if f.failure != "" && strings.HasPrefix(command, f.failure) {
			return nil, os.ErrPermission
		}
		switch command {
		case "info --format {{.OSType}}/{{.Architecture}}":
			return []byte("linux/aarch64"), nil
		case "compose version --short":
			return []byte("v2.26.0"), nil
		case "version --format {{.Server.APIVersion}}":
			return []byte("1.45"), nil
		case "compose config --images":
			return []byte("example/core:latest\nexample/web:latest"), nil
		case "compose config --environment":
			return []byte("OAC_PUBLIC_URL=http://localhost:8080"), nil
		}
		if strings.HasPrefix(command, "image inspect") && !f.cached {
			return nil, os.ErrNotExist
		}
		if strings.HasPrefix(command, "compose up") {
			if _, err := os.Stat(filepath.Join(f.options.dir, ".env")); err != nil {
				t.Fatal("services started before configuration publication")
			}
		}
		return nil, nil
	}
	return f
}
func (f *installFixture) run() error { return f.install(context.Background(), f.options) }

func TestInstallFailureBeforePublicationAndRetry(t *testing.T) {
	for _, failure := range []string{"download", "compose config --quiet", "pull"} {
		t.Run(failure, func(t *testing.T) {
			f := newInstallFixture(t)
			f.failure = failure
			if err := f.run(); err == nil {
				t.Fatal("expected failure")
			}
			for _, path := range []string{f.options.dir, f.options.dir + ".staging"} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("unpublished state remains: %s", path)
				}
			}
			f.failure = ""
			if err := f.run(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestInstallResumePreservesConfigAndUsesCachedImages(t *testing.T) {
	f := newInstallFixture(t)
	f.failure = "compose up"
	if err := f.run(); err == nil {
		t.Fatal("expected startup failure")
	}
	saved, err := os.ReadFile(filepath.Join(f.options.dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	f.failure = ""
	f.cached = true
	f.calls = nil
	f.download = func(context.Context, string, string) error {
		t.Fatal("resume downloaded replacement release")
		return nil
	}
	f.options.publicURL = "https://replacement.invalid"
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(f.options.dir, ".env"))
	if string(saved) != string(after) {
		t.Fatal("saved configuration replaced")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "pull") || strings.Contains(call, "down") {
			t.Fatal(call)
		}
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "--pull never --no-recreate") {
		t.Fatal(f.calls)
	}
}
func TestInstallRecoversRecognizedStageAndPreservesUnknownData(t *testing.T) {
	for _, owned := range []bool{true, false} {
		t.Run(fmt.Sprint(owned), func(t *testing.T) {
			f := newInstallFixture(t)
			stage := f.options.dir + ".staging"
			os.Mkdir(stage, 0o700)
			os.WriteFile(filepath.Join(stage, "partial"), []byte("untouched"), 0o600)
			if owned {
				os.Mkdir(filepath.Join(stage, stageMarker(f.options.dir)), 0o700)
			}
			err := f.run()
			if owned && err != nil {
				t.Fatal(err)
			}
			if !owned {
				if err == nil {
					t.Fatal("unrecognized staging accepted")
				}
				if raw, _ := os.ReadFile(filepath.Join(stage, "partial")); string(raw) != "untouched" {
					t.Fatal("unknown contents removed")
				}
			}
		})
	}
	f := newInstallFixture(t)
	os.Mkdir(f.options.dir, 0o700)
	os.WriteFile(filepath.Join(f.options.dir, "user-file"), []byte("keep"), 0o600)
	if err := f.run(); err == nil {
		t.Fatal("unrelated directory accepted")
	}
	if raw, _ := os.ReadFile(filepath.Join(f.options.dir, "user-file")); string(raw) != "keep" {
		t.Fatal("user data lost")
	}
}
func TestInstallRejectsBusyPortAndLock(t *testing.T) {
	f := newInstallFixture(t)
	listener, err := net.Listen("tcp", net.JoinHostPort(f.options.host, fmt.Sprint(f.options.port)))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.run(); err == nil {
		t.Fatal("busy port accepted")
	}
	listener.Close()
	if err := withLock(f.options.dir, func() error {
		if err := f.run(); err == nil {
			t.Fatal("concurrent installation accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestInstallRefusesModifiedCompose(t *testing.T) {
	f := newInstallFixture(t)
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.options.dir, "compose.yaml"), []byte("changed"), 0o600)
	f.calls = nil
	if err := f.run(); err == nil {
		t.Fatal("changed compose accepted")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "compose up") {
			t.Fatal("started modified compose")
		}
	}
}
func TestInstallerInterruptedProcess(t *testing.T) {
	if root := os.Getenv("OAC_TEST_INSTALL_KILL"); root != "" {
		err := withLock(root, func() error {
			stage := root + ".staging"
			os.Mkdir(stage, 0o700)
			os.Mkdir(filepath.Join(stage, stageMarker(root)), 0o700)
			os.WriteFile(filepath.Join(stage, "ready"), nil, 0o600)
			time.Sleep(time.Minute)
			return nil
		})
		if err != nil {
			os.Exit(2)
		}
		return
	}
	f := newInstallFixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallerInterruptedProcess$")
	cmd.Env = append(os.Environ(), "OAC_TEST_INSTALL_KILL="+f.options.dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(f.options.dir+".staging", "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never acquired lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if err := f.run(); err != nil {
		t.Fatalf("killed installer left unrecoverable state: %v", err)
	}
}
