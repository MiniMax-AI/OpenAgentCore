//go:build unix

package daemonize

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The native bootstrap holds its download lock on an inheritable descriptor.
// The detached daemon must not keep that lock after its caller exits.
func TestSpawnLeavesInheritedLockBehind(t *testing.T) {
	dir := privateTempDir(t)
	lockPath := filepath.Join(dir, "download.lock")
	inherited, err := syscall.Open(lockPath, syscall.O_RDWR|syscall.O_CREAT, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(inherited, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Setenv(spawnTestChildEnv, "1")
	pidPath := filepath.Join(dir, "child.pid")
	if _, err := Spawn([]string{"oac-daemon", "child-mode"}, ReExecOptions{LogPath: filepath.Join(dir, "child.log"), PIDPath: pidPath}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = StopPIDFile(pidPath, 2*time.Second) })
	if err := syscall.Close(inherited); err != nil {
		t.Fatal(err)
	}

	next, err := syscall.Open(lockPath, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(next)
	if err := unix.Flock(next, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("the background child kept the caller's lock: %v", err)
	}
}
