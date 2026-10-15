//go:build unix

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

func TestInstallExplainsPathsAnotherAccountOwns(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	// An unreadable directory stands in for one an earlier run with sudo left owned by root.
	for _, blocked := range []string{"parent", "lock"} {
		t.Run(blocked, func(t *testing.T) {
			f := newInstallFixture(t)
			parent := filepath.Dir(f.options.dir)
			t.Setenv("HOME", filepath.Dir(parent))
			path := parent
			if blocked == "lock" {
				path = f.options.dir + ".lock"
				os.Mkdir(path, 0o700)
			}
			os.Chmod(path, 0)
			t.Cleanup(func() { os.Chmod(path, 0o700) })
			want := "This account cannot access " + path + ". If an earlier run used sudo, return ownership to this account and retry: sudo chown -R \"$(id -un)\" '" + path + "'"
			if err := f.run(); err == nil || err.Error() != want {
				t.Fatalf("got %v", err)
			}
			os.Chmod(path, 0o700)
			if _, err := os.Stat(f.options.dir); !os.IsNotExist(err) {
				t.Fatal("refused installation created its directory")
			}
		})
	}
}

func TestInstallWriteFailureLeavesRetryableState(t *testing.T) {
	if os.Getenv("OAC_TEST_FILE_LIMIT") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestInstallWriteFailureLeavesRetryableState$")
		cmd.Env = append(os.Environ(), "OAC_TEST_FILE_LIMIT=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("write failure probe: %v\n%s", err, output)
		}
		return
	}
	f := newInstallFixture(t)
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	limited := original
	limited.Cur = 0
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
		t.Fatal(err)
	}
	err := f.run()
	if restore := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); restore != nil {
		t.Fatal(restore)
	}
	if err == nil {
		t.Fatal("installation succeeded without writable files")
	}
	if _, err := os.Stat(f.options.dir + ".staging"); !os.IsNotExist(err) {
		t.Fatal("failed installation retained staging")
	}
	if err := f.run(); err != nil {
		t.Fatalf("installation did not recover after restoring writes: %v", err)
	}
}
