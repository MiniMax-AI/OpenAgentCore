package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
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

// installedRevision reads the release the installation directory starts.
func (f *installFixture) installedRevision() string {
	raw, _ := os.ReadFile(filepath.Join(f.options.dir, "compose.yaml"))
	return composeRevision(raw)
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
	// The release served for download is always the running installer's.
	f.download = func(_ context.Context, address string) ([]byte, error) {
		if f.failure == "download" {
			return nil, os.ErrPermission
		}
		compose := []byte("services:\n  init:\n    environment:\n      OAC_REVISION: " + buildRevision + "\n")
		if strings.HasSuffix(address, "sums.txt") {
			return []byte(fmt.Sprintf("%x  compose.yaml\n", sha256.Sum256(compose))), nil
		}
		return compose, nil
	}
	// Calls against the staged release are recorded with a "staged " prefix and without their file arguments.
	f.docker = func(_ context.Context, dir string, args ...string) ([]byte, error) {
		if _, err := os.Stat(dir); dir != "" && err != nil {
			t.Fatalf("docker %v ran in a missing directory", args)
		}
		var words []string
		staged := false
		for i := 0; i < len(args); i++ {
			if args[i] == "-f" || args[i] == "--project-directory" {
				staged = staged || strings.HasPrefix(args[i+1], f.options.dir+".staging")
				i++
				continue
			}
			words = append(words, args[i])
		}
		command := strings.Join(words, " ")
		if staged {
			if f.installedRevision() == buildRevision {
				t.Fatal("release published before its checks")
			}
			f.calls = append(f.calls, "staged "+command)
		} else {
			f.calls = append(f.calls, command)
		}
		if f.failure != "" && strings.HasPrefix(f.calls[len(f.calls)-1], f.failure) {
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
			if _, err := os.Stat(filepath.Join(f.options.dir, ".env")); err != nil || f.installedRevision() != buildRevision {
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
	// The same release resumes with or without --upgrade.
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
	f.download = func(context.Context, string) ([]byte, error) {
		t.Fatal("resume downloaded replacement release")
		return nil, nil
	}
	f.options.publicURL = "https://replacement.invalid"
	f.options.upgrade = true
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
	// Init finishes a first start that stopped before it, so check-config finds the secrets.
	assertCallsInOrder(t, f.calls, "compose "+strings.Join(initVolume, " "), "compose "+strings.Join(checkConfig, " "), "compose "+strings.Join(startServices, " "))
}

func TestInstallUpgradesAnotherReleaseOnlyWhenRequested(t *testing.T) {
	f := newInstallFixture(t)
	setBuildRevision(t, "a1b2c3d4e5f6a7b8c9d0a1b2c3d4e5f6a7b8c9d0")
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.options.dir, "compose-sha256sums.txt"), []byte("previous release"), 0o600)
	before := installationFiles(t, f.options.dir)
	setBuildRevision(t, "development")
	os.WriteFile(f.executable, []byte("upgraded binary"), 0o700)

	f.calls = nil
	err := f.run()
	if err == nil || err.Error() != "This installation runs release a1b2c3d4e5f6; this installer is development. Back up the installation (Upgrade in the operations guide), then rerun with --upgrade." {
		t.Fatal(err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("refusal ran Docker beyond its checks: %v", f.calls)
	}

	f.options.upgrade = true
	f.failure = "staged compose run --rm --no-deps init"
	if err := f.run(); err == nil || !strings.HasPrefix(err.Error(), "Upgrade stopped; the running services are unchanged") {
		t.Fatal(err)
	}
	if after := installationFiles(t, f.options.dir); !maps.Equal(before, after) {
		t.Fatal("refused upgrade changed the installation directory")
	}
	if _, err := os.Stat(f.options.dir + ".staging"); !os.IsNotExist(err) {
		t.Fatal("refused upgrade left its stage")
	}

	f.failure = ""
	f.calls = nil
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	assertCallsInOrder(t, f.calls,
		"pull example/core:latest",
		"staged compose "+strings.Join(checkConfig, " "),
		"staged compose run --rm --no-deps init",
		"compose up -d --wait --wait-timeout 180 --pull never --remove-orphans")
	after := installationFiles(t, f.options.dir)
	if after[cliName()] != "upgraded binary" || after[".env"] != before[".env"] || f.installedRevision() != "development" {
		t.Fatal("upgrade did not publish the release with the saved settings")
	}
	if _, ok := after["compose-sha256sums.txt"]; ok {
		t.Fatal("upgrade kept the previous release checksum")
	}
}

func setBuildRevision(t *testing.T, revision string) {
	previous := buildRevision
	buildRevision = revision
	t.Cleanup(func() { buildRevision = previous })
}

func installationFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = string(raw)
	}
	return files
}

func assertCallsInOrder(t *testing.T, calls []string, want ...string) {
	t.Helper()
	next := 0
	for _, call := range calls {
		if next < len(want) && call == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("missing %q in order in %v", want[next], calls)
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
