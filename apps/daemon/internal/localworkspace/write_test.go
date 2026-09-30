package localworkspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"os"
	"path/filepath"
	"testing"
)

func nativeFileBinding(t *testing.T) *Binding {
	t.Helper()
	return &Binding{workspace: t.TempDir(), writer: &fileWriter{}}
}
func TestNativeFileCreateAndNoReplace(t *testing.T) {
	b := nativeFileBinding(t)
	for _, size := range []int{0, 3, (1 << 20) + 17, WriteMaxBytes} {
		name := fmt.Sprintf("nested/file-%d", size)
		data := bytes.Repeat([]byte{17}, size)
		got, err := b.WriteWorkspaceFile(t.Context(), name, data)
		if err != nil || got.SizeBytes != int64(size) {
			t.Fatal(size, got, err)
		}
		if _, err = b.WriteWorkspaceFile(t.Context(), name, []byte("overwrite")); !errors.Is(err, agent.ErrWorkspaceWriteUnsafe) {
			t.Fatal("existing file replaced", err)
		}
		raw, err := os.ReadFile(filepath.Join(b.workspace, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(raw, data) {
			t.Fatal("file content changed", err)
		}
	}
	if _, err := b.WriteWorkspaceFile(t.Context(), "nested", nil); !errors.Is(err, agent.ErrWorkspaceWriteDirectory) {
		t.Fatal(err)
	}
	if _, err := b.WriteWorkspaceFile(t.Context(), "after-rejection", nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(b.workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Name()) >= 11 && entry.Name()[:11] == ".oac-write-" {
			t.Fatal("temporary retained")
		}
	}
}
func TestNativeFileAdmission(t *testing.T) {
	b := nativeFileBinding(t)
	for _, path := range []string{"", ".", "..", "/etc/passwd", "a/../b", "a//b", "a\\b", "a\nb"} {
		if _, err := b.WriteWorkspaceFile(t.Context(), path, nil); !errors.Is(err, agent.ErrWorkspaceWriteInvalid) {
			t.Fatal(path, err)
		}
	}
	if _, err := b.WriteWorkspaceFile(t.Context(), "large", make([]byte, WriteMaxBytes+1)); !errors.Is(err, agent.ErrWorkspaceWriteInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := b.WriteWorkspaceFile(ctx, "cancelled", nil); !errors.Is(err, agent.ErrWorkspaceWriteUnavailable) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.workspace, "cancelled")); !os.IsNotExist(err) {
		t.Fatal("cancelled request mutated workspace")
	}
	b.writer.mu.Lock()
	_, err := b.WriteWorkspaceFile(t.Context(), "busy", nil)
	b.writer.mu.Unlock()
	if !errors.Is(err, agent.ErrWorkspaceWriteBusy) {
		t.Fatal(err)
	}
	b.writer.uncertain = true
	if _, err = b.WriteWorkspaceFile(t.Context(), "uncertain", nil); !errors.Is(err, agent.ErrWorkspaceWriteUncertain) {
		t.Fatal(err)
	}
}
