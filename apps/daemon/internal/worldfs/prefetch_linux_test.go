//go:build linux

package worldfs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"golang.org/x/sys/unix"
)

// Proof for syscall-local prefetch: real statx syscalls on a real FUSE mount of the real file service, observed by the BPF scope programs.

type opCounts map[sandboxfs.Op]int64

func (m *mounted) snapshot() opCounts {
	c := opCounts{}
	for op := sandboxfs.OpDescribe; op <= sandboxfs.OpOpenTree; op++ {
		c[op] = m.ops[op].Load()
	}
	return c
}

func (m *mounted) delta(t *testing.T, since opCounts, want opCounts) {
	t.Helper()
	now := m.snapshot()
	for op, n := range want {
		if got := now[op] - since[op]; got != n {
			t.Errorf("%s requests = %d, want %d", op, got, n)
		}
	}
}

func statx(t *testing.T, path string) (unix.Statx_t, error) {
	t.Helper()
	var st unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW|unix.AT_STATX_SYNC_AS_STAT, unix.STATX_ALL, &st)
	return st, err
}

func sameStat(a, b unix.Statx_t) bool {
	return a.Mode == b.Mode && a.Ino == b.Ino && a.Size == b.Size && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink && a.Mtime == b.Mtime && a.Ctime == b.Ctime && a.Dev_major == b.Dev_major && a.Dev_minor == b.Dev_minor && a.Mnt_id == b.Mnt_id
}

func TestScopedStatx(t *testing.T) {
	requireFUSE(t)
	backing := t.TempDir()
	if err := os.MkdirAll(filepath.Join(backing, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(backing, "a", "b", "c.txt"), "content")
	m, err := serve(t, backing, 0, sessionview.Mountpoint{Path: "/bin/sh"})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if err := m.world.ScopeErr(); err != nil {
		t.Fatalf("scopes unavailable: %v", err)
	}
	// A view sees the world at /; one locked thread of its own enters the mount as its root, so absolute paths are world paths as in a view, and the thread ends with the goroutine, leaving the cleanups an ordinary thread.
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_FS); err != nil {
			t.Error(err)
			return
		}
		if err := unix.Chdir(m.dir); err != nil {
			t.Error(err)
			return
		}
		scopedStatx(t, m, backing)
	}()
	<-done
}

func scopedStatx(t *testing.T, m *mounted, backing string) {
	p := "/a/b/c.txt"
	sandbox := func(fn func() error) error {
		done := make(chan error, 1)
		go func() { done <- fn() }() // another thread: not chrooted
		return <-done
	}

	// A relative path has no scope: one LOOKUP per component and one GETATTR, as the Uncached profile requires.
	s0 := m.snapshot()
	base, err := statx(t, "a/b/c.txt")
	if err != nil {
		t.Error(err)
		return
	}
	if err := unix.Chroot(m.dir); err != nil {
		t.Error(err)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	m.delta(t, s0, opCounts{sandboxfs.OpLookup: 3, sandboxfs.OpGetAttr: 1, sandboxfs.OpWalk: 0})

	// An absolute path is scoped: the first LOOKUP runs one Walk and the rest of the syscall consumes it. Same statx output.
	s1 := m.snapshot()
	got, err := statx(t, p)
	if err != nil {
		t.Error(err)
		return
	}
	if !sameStat(base, got) {
		t.Errorf("scoped statx differs:\n%+v\n%+v", base, got)
	}
	m.delta(t, s1, opCounts{sandboxfs.OpWalk: 1, sandboxfs.OpLookup: 0, sandboxfs.OpGetAttr: 0})

	// Missing leaf: the Walk failure answers the leaf LOOKUP with ENOENT.
	s2 := m.snapshot()
	if _, err := statx(t, "/a/b/missing"); err != unix.ENOENT {
		t.Errorf("missing: %v, want ENOENT", err)
	}
	m.delta(t, s2, opCounts{sandboxfs.OpWalk: 1, sandboxfs.OpLookup: 0, sandboxfs.OpGetAttr: 0})

	// No cross-syscall reuse: a sandbox-side rename between two statx calls is observed by the second, which is a new scope and a new Walk.
	if err := sandbox(func() error {
		return os.Rename(filepath.Join(backing, "a", "b", "c.txt"), filepath.Join(backing, "a", "b", "e.txt"))
	}); err != nil {
		t.Fatal(err)
	}
	// The cached positive dentry of c.txt is revalidated by the Walk's ENOENT (consumed), then the kernel looks the name up afresh: that one Lookup is the ordinary request.
	s3 := m.snapshot()
	if _, err := statx(t, p); err != unix.ENOENT {
		t.Errorf("after rename: %v, want ENOENT", err)
	}
	m.delta(t, s3, opCounts{sandboxfs.OpWalk: 1, sandboxfs.OpLookup: 1})

	// A pinned first component is never walked: the original pinned path answers.
	s4 := m.snapshot()
	if _, err := statx(t, "/bin/sh"); err != nil {
		t.Errorf("pinned: %v", err)
	}
	m.delta(t, s4, opCounts{sandboxfs.OpWalk: 0})

	// Readlink of a regular file: one Walk, the kernel answers EINVAL itself. One component is asked a second time (observed: one Lookup), which the consumed entry no longer answers.
	s5 := m.snapshot()
	if _, err := os.Readlink("/a/b/e.txt"); err == nil {
		t.Errorf("readlink of a regular file succeeded")
	}
	m.delta(t, s5, opCounts{sandboxfs.OpWalk: 1, sandboxfs.OpReadlink: 0})
	t.Logf("readlink extra Lookup requests = %d", m.snapshot()[sandboxfs.OpLookup]-s5[sandboxfs.OpLookup])

}
