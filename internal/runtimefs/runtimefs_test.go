package runtimefs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func privateRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(parent, "private")
	if err = EnsurePrivateDir(name); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root, name
}

func TestPrivateAtomicWriteAndExclusiveCreate(t *testing.T) {
	root, _ := privateRoot(t)
	for _, body := range []string{"first", "replacement"} {
		if err := WritePrivateAtomic(root, "credential.json", []byte(body)); err != nil {
			t.Fatal(err)
		}
		got, err := ReadPrivate(root, "credential.json", 100)
		if err != nil || string(got) != body {
			t.Fatal("private replacement failed", err)
		}
	}
	if _, err := ReadPrivate(root, "credential.json", 3); err == nil {
		t.Fatal("oversize accepted")
	}
	if _, err := OpenPrivate(root, "credential.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL); !errors.Is(err, os.ErrExist) {
		t.Fatal("exclusive create replaced file", err)
	}
	for _, name := range []string{"../escape", "nested/file", ".", "", `nested\file`} {
		if _, err := OpenPrivate(root, name, os.O_RDONLY); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
	entries, err := os.ReadDir(root.Name())
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary publication left behind", err)
	}
}

func TestPrivateDirectoryLockAcrossProcesses(t *testing.T) {
	root, name := privateRoot(t)
	unlock, err := LockDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			unlock()
		}
	}()
	child := func(expect string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestPrivateLockChild$")
		cmd.Env = append(os.Environ(), "OAC_TEST_PRIVATE_LOCK="+name, "OAC_TEST_PRIVATE_LOCK_EXPECT="+expect)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("lock child failed: %v %s", err, out)
		}
	}
	child("busy")
	unlock()
	held = false
	child("available")
}

func TestPrivateLockChild(t *testing.T) {
	name := os.Getenv("OAC_TEST_PRIVATE_LOCK")
	if name == "" {
		return
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	unlock, err := LockDirectory(root)
	if os.Getenv("OAC_TEST_PRIVATE_LOCK_EXPECT") == "busy" {
		if err == nil {
			unlock()
			t.Fatal("concurrent lock admitted")
		}
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("a held lock must report ErrLocked, got %v", err)
		}
	} else {
		if err != nil {
			t.Fatal(err)
		}
		unlock()
	}
}

func TestLocalPathValidation(t *testing.T) {
	for _, path := range []string{"", "relative", "a/../b", "/tmp/line\n"} {
		if ValidateLocalPath(path) == nil {
			t.Fatal("ambiguous path accepted")
		}
	}
	_, name := privateRoot(t)
	if err := ValidateLocalPath(name); err != nil {
		t.Fatal(err)
	}
}
