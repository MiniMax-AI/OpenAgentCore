//go:build linux

// Package sessionviewtest gives privileged view tests a cgroup parent for their views.
package sessionviewtest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// mount is a cgroup v2 hierarchy that this process mounted. In a container with a private cgroup namespace it is the container's own cgroup, writable for root with CAP_SYS_ADMIN.
var mount = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "oac-cgroup-")
	if err != nil {
		return "", err
	}
	return dir, unix.Mount("cgroup2", dir, "cgroup2", 0, "")
})

// CgroupParent returns a new cgroup v2 directory for the test's views. The test fails at its end when the directory still holds a cgroup.
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
		if err := unix.Rmdir(dir); err != nil {
			t.Errorf("remove the test's cgroup parent: %v", err)
		}
	})
	return dir
}

// Cgroups lists the cgroups in parent.
func Cgroups(t testing.TB, parent string) []string {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(parent, e.Name()))
		}
	}
	return dirs
}
