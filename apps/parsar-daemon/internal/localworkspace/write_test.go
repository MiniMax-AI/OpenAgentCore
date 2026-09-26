package localworkspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
)

func writableBinding(t *testing.T, script string) *Binding {
	t.Helper()
	b, _ := testBinding(t)
	// macOS test roots may contain /var aliases. Writer deployment paths must be canonical.
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b.workspace = filepath.Join(parent, "workspace")
	staging := filepath.Join(parent, "staging")
	for _, p := range []string{b.workspace, staging} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	helper, err := filepath.EvalSymlinks(b.helper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := b.bindWriter(helper, staging); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWriteRejectsUnsafeBindings(t *testing.T) {
	b := writableBinding(t, "exit 1\n")
	link := filepath.Join(filepath.Dir(b.workspace), "alias")
	if err := os.Symlink(b.writer.staging, link); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(b.workspace, "helper")
	if err := os.WriteFile(inside, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"", b.writer.staging}, {b.writer.helper, ""}, {b.writer.helper, b.workspace}, {b.writer.helper, link}, {inside, b.writer.staging}, {b.writer.helper, filepath.Dir(b.workspace)}} {
		if err := b.bindWriter(pair[0], pair[1]); err == nil {
			t.Fatalf("unsafe writer accepted: %q", pair)
		}
	}
}

func TestWriteReceiptAndCredentialBoundary(t *testing.T) {
	t.Setenv("OAC_TEST_PRIVATE_CREDENTIAL", "synthetic-secret")
	// Files.create always selects the helper's explicit create mode.
	b := writableBinding(t, "[ -z \"$OAC_TEST_PRIVATE_CREDENTIAL\" ] || exit 13\n[ \"$#\" = 5 ] && [ \"$5\" = create ] || exit 14\ncat >/dev/null\nprintf '%s' '{\"version\":1,\"outcome\":\"completed\",\"size_bytes\":3}'\n")
	result, err := b.WriteWorkspaceFile(t.Context(), "file", []byte{0, 1, 2})
	if err != nil || result.SizeBytes != 3 {
		t.Fatalf("write: %+v %v", result, err)
	}
	for _, p := range []string{"", ".", "..", "/etc/passwd", "a/../b", "a//b", "a\\b", "a\nb"} {
		if _, err := b.WriteWorkspaceFile(t.Context(), p, nil); !errors.Is(err, agent.ErrWorkspaceWriteInvalid) {
			t.Fatalf("path %q: %v", p, err)
		}
	}
	if _, err := b.WriteWorkspaceFile(t.Context(), "file", make([]byte, WriteMaxBytes+1)); !errors.Is(err, agent.ErrWorkspaceWriteInvalid) {
		t.Fatal(err)
	}
}

func TestWriteDetachmentAndConcurrentAdmission(t *testing.T) {
	b := writableBinding(t, "cat >/dev/null\ntouch \"$1/started\"\nwhile [ ! -f \"$1/release\" ]; do sleep 0.01; done\nprintf '%s' '{\"version\":1,\"outcome\":\"completed\",\"size_bytes\":0}'\n")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := b.WriteWorkspaceFile(ctx, "file", nil); done <- err }()
	defer os.WriteFile(filepath.Join(b.workspace, "release"), nil, 0600)
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(b.workspace, "started")); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("helper did not start")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if _, err := b.WriteWorkspaceFile(t.Context(), "other", nil); !errors.Is(err, agent.ErrWorkspaceWriteBusy) {
		t.Fatalf("overlap: %v", err)
	}
	if err := os.WriteFile(filepath.Join(b.workspace, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("detached admitted write lost receipt", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write did not finish")
	}
}

func TestWriteUncertaintyStopsSuccessor(t *testing.T) {
	for name, script := range map[string]string{
		"missing":    "cat >/dev/null\nexit 0\n",
		"wrong-size": "cat >/dev/null\nprintf '%s' '{\"version\":1,\"outcome\":\"completed\",\"size_bytes\":8}'\n",
		"overflow":   "cat >/dev/null\nhead -c 2048 /dev/zero\n",
		"exit":       "cat >/dev/null\nprintf '%s' '{\"version\":1,\"outcome\":\"completed\",\"size_bytes\":0}'\nexit 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			b := writableBinding(t, script)
			if _, err := b.WriteWorkspaceFile(t.Context(), "file", nil); !errors.Is(err, agent.ErrWorkspaceWriteUncertain) {
				t.Fatalf("first: %v", err)
			}
			if err := os.WriteFile(b.writer.helper, []byte("#!/bin/sh\ntouch \"$1/forbidden\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := b.WriteWorkspaceFile(t.Context(), "other", nil); !errors.Is(err, agent.ErrWorkspaceWriteUncertain) {
				t.Fatalf("successor: %v", err)
			}
			if _, err := os.Stat(filepath.Join(b.workspace, "forbidden")); !os.IsNotExist(err) {
				t.Fatal("unknown write admitted successor")
			}
		})
	}
}

func TestWriteReceiptValidation(t *testing.T) {
	for _, response := range []string{
		`{"version":1,"outcome":"completed"}`,
		`{"version":1,"outcome":"completed","size_bytes":0,"error":"write_failed"}`,
		`{"version":1,"outcome":"failed","size_bytes":0,"error":"write_failed"}`,
		`{"version":1,"outcome":"unknown","error":"write_failed"}`,
		`{"version":1,"outcome":"failed","error":"other"}`,
		`{"version":1,"outcome":"completed","size_bytes":0} {}`,
		`{"version":1,"outcome":"completed","size_bytes":0,"extra":true}`,
		`{"version":1,"outcome":"unknown","error":"unsafe_destination"}`,
		`{"version":1,"outcome":"failed","size_bytes":0,"error":"destination_directory"}`,
	} {
		if _, err := decodeWrite([]byte(response), 0); !errors.Is(err, agent.ErrWorkspaceWriteUncertain) {
			t.Fatalf("unsafe receipt %s: %v", response, err)
		}
	}
	for code, want := range map[string]error{
		"invalid_input":         agent.ErrWorkspaceWriteRejected,
		"write_failed":          agent.ErrWorkspaceWriteRejected,
		"destination_directory": agent.ErrWorkspaceWriteDirectory,
		"unsafe_destination":    agent.ErrWorkspaceWriteUnsafe,
	} {
		_, err := decodeWrite([]byte(fmt.Sprintf(`{"version":1,"outcome":"failed","error":%q}`, code)), 0)
		if err != want || !errors.Is(err, agent.ErrWorkspaceWriteRejected) {
			t.Fatal(code, err)
		}
	}
}

func TestLocalWriteNativeInstaller(t *testing.T) {
	helper := os.Getenv("OAC_TEST_LOCAL_WRITE_HELPER")
	if helper == "" {
		t.Skip("requires the built native installer")
	}
	b := writableBinding(t, "exit 1\n")
	if err := b.bindWriter(helper, b.writer.staging); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(b.workspace, "escape")
	if err := os.Symlink(b.writer.staging, escape); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.workspace, "setup.txt"), []byte("setup"), 0600); err != nil {
		t.Fatal(err)
	}
	// Missing parents are created; an existing destination is never replaced.
	for _, size := range []int{0, 3, (1 << 20) + 17, WriteMaxBytes} {
		path := fmt.Sprintf("n1/n2/file-%d", size)
		data := bytes.Repeat([]byte{0, 255, 17}, (size+2)/3)[:size]
		got, err := b.WriteWorkspaceFile(t.Context(), path, data)
		if err != nil || got.SizeBytes != int64(size) {
			t.Fatalf("size %d: %+v %v", size, got, err)
		}
		actual, err := os.ReadFile(filepath.Join(b.workspace, path))
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatalf("size %d bytes differ: %v", size, err)
		}
	}
	if info, err := os.Lstat(filepath.Join(b.workspace, "n1")); err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("parent directory", info, err)
	}
	for path, want := range map[string]error{
		"n1/n2/file-3": agent.ErrWorkspaceWriteUnsafe,
		"setup.txt":    agent.ErrWorkspaceWriteUnsafe,
		"escape":       agent.ErrWorkspaceWriteUnsafe,
		"escape/new":   agent.ErrWorkspaceWriteUnsafe,
		"n1/n2":        agent.ErrWorkspaceWriteDirectory,
		"n1":           agent.ErrWorkspaceWriteDirectory,
	} {
		if _, err := b.WriteWorkspaceFile(t.Context(), path, []byte("denied")); err != want {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(b.workspace, "n1/n2/file-3")); err != nil || !bytes.Equal(data, []byte{0, 255, 17}) {
		t.Fatal("existing file changed", err)
	}
	if data, err := os.ReadFile(filepath.Join(b.workspace, "setup.txt")); err != nil || string(data) != "setup" {
		t.Fatal("untracked file changed", err)
	}
	if _, err := b.WriteWorkspaceFile(t.Context(), "after-rejection", nil); err != nil {
		t.Fatal("known rejection blocked next write", err)
	}
	entries, err := os.ReadDir(b.writer.staging)
	if err != nil || len(entries) != 0 {
		t.Fatal("staging retained successful/known-rejected data", err)
	}
}
