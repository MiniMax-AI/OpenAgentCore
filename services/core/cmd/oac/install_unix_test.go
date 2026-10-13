//go:build unix

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
)

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
