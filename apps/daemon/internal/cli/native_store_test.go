package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeInstallationsShareOneReadOnlyCopy(t *testing.T) {
	rc, args, first, bundle := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	store := nativeTestStore(t)
	// The second installation needs no distribution: it finds every component in the store.
	if err := os.RemoveAll(filepath.Join(bundle, "components")); err != nil {
		t.Fatal(err)
	}
	args = otherNativeInstallation(t, args)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	second := args[slices.Index(args, "--install-dir")+1]
	for _, dir := range []string{first, second} {
		if _, err := os.Stat(filepath.Join(dir, "components")); err == nil {
			t.Fatal("installation holds its own component copy")
		}
		var installed nativeInstallation
		if err := readNativeJSON(filepath.Join(dir, "daemon", "installation.json"), &installed); err != nil || installed.ComponentDirectory != store {
			t.Fatalf("installation does not reference the store: %v %q", err, installed.ComponentDirectory)
		}
	}
	if info, err := os.Stat(filepath.Join(store, "codex", ".oac-install.json")); err != nil || info.Mode().Perm()&0200 != 0 {
		t.Fatalf("component file is writable: %v", err)
	}
	if users, _ := countNativeUsers(store); users != 2 {
		t.Fatalf("store has %d users, want 2", users)
	}
	if output := uninstallNative(t, first); !strings.Contains(output, "Shared components kept: 1 other") {
		t.Fatalf("first uninstall:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(store, "codex")); err != nil {
		t.Fatal("uninstalling one installation removed components another uses")
	}
	if output := uninstallNative(t, second); !strings.Contains(output, "Shared components removed") {
		t.Fatalf("last uninstall:\n%s", output)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatal("the last uninstall left the version directory")
	}
}

func TestNativeStoreDropsGoneInstallationsAndUnusedVersions(t *testing.T) {
	rc, args, first, _ := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	store := nativeTestStore(t)
	// A version whose only installation is gone, with read-only content like a published component.
	old := filepath.Join(filepath.Dir(store), "old-version")
	gone := filepath.Join(t.TempDir(), "deleted")
	if err := addNativeUser(old, gone); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(old, "node"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "node", "program"), nil, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(old, "node"), 0500); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(rc, otherNativeInstallation(t, args)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("unused version directory remains")
	}
	if users, _ := countNativeUsers(store); users != 1 {
		t.Fatalf("store has %d users, want only the new installation", users)
	}
}

// safeBuffer lets the test read the output while the installation still writes it.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestNativeStoreWaitsForAnotherInstallation(t *testing.T) {
	rc, args, _, _ := nativeInstallFixture(t)
	output := &safeBuffer{}
	rc.stdout = output
	oldRetry, oldWait := nativeStoreRetry, nativeStoreWait
	nativeStoreRetry = 5 * time.Millisecond
	t.Cleanup(func() { nativeStoreRetry, nativeStoreWait = oldRetry, oldWait })
	root := filepath.Dir(nativeTestStore(t))
	unlock, err := lockNativeStore(context.Background(), io.Discard, root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runInstall(rc, args) }()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(output.String(), "Waiting for another installation"); {
		select {
		case err = <-done:
			t.Fatalf("installation did not wait: %v", err)
		case <-time.After(5 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the wait was not announced")
		}
	}
	unlock()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	unlock, err = lockNativeStore(context.Background(), io.Discard, root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	nativeStoreWait = 20 * time.Millisecond
	if _, err = lockNativeStore(context.Background(), io.Discard, root); err == nil || !strings.Contains(err.Error(), "held the shared components") {
		t.Fatalf("expected a timeout, got %v", err)
	}
}

func TestNativeStoreKeepsWhatItDidNotCreateOrCannotParse(t *testing.T) {
	rc, args, _, _ := nativeInstallFixture(t)
	store := nativeTestStore(t)
	// Another program's directory in components/ is not a version directory.
	unrelated := filepath.Join(filepath.Dir(store), "unrelated")
	if err := os.MkdirAll(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	// A failed first installation leaves no entry; the version is collected by a later install.
	probeNativeInstallation = func(context.Context, string, []string) error { return errors.New("synthetic unavailable") }
	if err := runInstall(rc, args); err == nil {
		t.Fatal("published a failed installation")
	}
	if users, _ := countNativeUsers(store); users != 0 {
		t.Fatal("a failed first installation pinned the shared components")
	}
	probeNativeInstallation = func(context.Context, string, []string) error { return nil }
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	// An entry that cannot be read as a path counts as a user; a writable top directory is sealed again.
	if err := os.WriteFile(filepath.Join(store, "users", "damaged"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(store, "codex"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := runInstall(rc, otherNativeInstallation(t, args)); err != nil {
		t.Fatal(err)
	}
	if users, _ := countNativeUsers(store); users != 3 {
		t.Fatalf("store has %d users, want 3", users)
	}
	if info, err := os.Stat(filepath.Join(store, "codex")); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0500) {
		t.Fatalf("component directory not sealed: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("collection removed a directory it did not create")
	}
}
