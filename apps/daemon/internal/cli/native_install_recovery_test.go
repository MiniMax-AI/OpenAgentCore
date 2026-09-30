package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeInstallationCleansOnlyReservedPartialFiles(t *testing.T) {
	rc, args, root, _ := nativeInstallFixture(t)
	names := []string{"components/.install-codex-12345/partial", "bin/.oac-daemon-9876", "daemon/.installation.json-0123456789abcdef01234567.tmp"}
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(root, "components", ".install-codex-user-notes")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// Cleanup must not happen before obtaining the operation lock.
	_, unlock, err := lockNativeInstallation(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = runInstall(rc, args); err == nil {
		t.Fatal("concurrent installation accepted")
	}
	for _, name := range names {
		if _, err = os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal("active staging was removed")
		}
	}
	unlock()
	if err = runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err = os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("stale staging remains: %s", name)
		}
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "keep" {
		t.Fatal("unrelated file changed")
	}
	output := rc.stdout.(*bytes.Buffer).String()
	if !strings.Contains(output, "Installing codex") || strings.Contains(output, "\r") || strings.Contains(output, "\x1b") {
		t.Fatalf("invalid redirected progress: %q", output)
	}
}

func TestNativeCopyPreservesIOErrorAndCancellation(t *testing.T) {
	failure := errors.New("synthetic filesystem failure")
	_, err := nativeCopy(context.Background(), nativeFailingWriter{failure}, strings.NewReader("component"))
	if !errors.Is(err, failure) {
		t.Fatalf("lost write error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = nativeCopy(ctx, new(bytes.Buffer), strings.NewReader("component"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if err = requireNativeSpace(t.TempDir(), ^uint64(0)); err == nil || !strings.Contains(err.Error(), "not enough disk space") {
		t.Fatalf("space overflow accepted: %v", err)
	}
}

type nativeFailingWriter struct{ err error }

func (w nativeFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestNativeStagingCleanupDoesNotFollowLinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "components")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(parent, ".install-codex-123")); err != nil {
		t.Skip("symlink permission unavailable")
	}
	if err := cleanNativeTemporaryFiles(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("removed link target")
	}
}

func TestNativeVerificationCancellationPreservesInstallation(t *testing.T) {
	rc, args, root, bundle := nativeInstallFixture(t)
	if err := runInstall(rc, args); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "daemon", "installation.json")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = verifyNativeComponents(ctx, root, []string{"codex"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification reported damage: %v", err)
	}
	b, err := readNativeBundle(bundle, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err = installNativeComponent(ctx, bundle, root, "codex", b.Components["codex"]); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reuse reported damage: %v", err)
	}
	after, err := os.ReadFile(config)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("cancellation changed installation")
	}
	if err = verifyNativeComponents(context.Background(), root, []string{"codex"}); err != nil {
		t.Fatal(err)
	}
}
