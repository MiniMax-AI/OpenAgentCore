package localworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
)

func TestNativeDirectoryAPI(t *testing.T) {
	b := nativeFileBinding(t)
	if err := os.Mkdir(filepath.Join(b.workspace, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.workspace, "nested", "data.bin"), []byte{0, 1, 255, 17}, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := b.ListWorkspaceDirectory(t.Context(), "nested", 10)
	if err != nil || got.Truncated || len(got.Entries) != 1 || got.Entries[0].Name != "data.bin" || got.Entries[0].SizeBytes == nil || *got.Entries[0].SizeBytes != 4 {
		t.Fatal(got, err)
	}
	for _, path := range []string{"missing", "nested/data.bin"} {
		if _, err := b.ListWorkspaceDirectory(t.Context(), path, 10); !errors.Is(err, dispatch.ErrWorkspaceNotDirectory) {
			t.Fatal(path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(b.workspace, "second"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = b.ListWorkspaceDirectory(t.Context(), "", 1)
	if err != nil || !got.Truncated || len(got.Entries) != 1 {
		t.Fatal(got, err)
	}
}
