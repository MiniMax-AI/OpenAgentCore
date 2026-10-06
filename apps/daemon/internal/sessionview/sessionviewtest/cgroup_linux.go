//go:build linux

// Package sessionviewtest gives privileged view tests a cgroup parent for their views.
package sessionviewtest

import (
	"errors"
	"os"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// mount is a cgroup v2 hierarchy that this process mounted.
var mount = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "oac-cgroup-")
	if err != nil {
		return "", err
	}
	if err := unix.Mount("cgroup2", dir, "cgroup2", 0, ""); err != nil {
		os.Remove(dir)
		return "", err
	}
	return dir, nil
})

// CgroupParent returns a new cgroup v2 directory for the test's views. It mounts the cgroup v2 hierarchy once per process, which in a container with a private cgroup namespace shows the container's own cgroup, writable for root with CAP_SYS_ADMIN. The test fails at its end when the directory still holds a cgroup.
func CgroupParent(t testing.TB) string {
	t.Helper()
	root, err := mount()
	if err != nil {
		t.Fatalf("mount a cgroup v2 hierarchy: %v", err)
	}
	dir, err := os.MkdirTemp(root, "test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Rmdir(dir); errors.Is(err, unix.EBUSY) {
			t.Errorf("view cgroups remain in %s", dir)
		} else if err != nil {
			t.Error(err)
		}
	})
	return dir
}
