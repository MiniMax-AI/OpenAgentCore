//go:build unix

package localworkspace

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestNativeExportRejectsReplacementFIFO(t *testing.T) {
	for _, directory := range []bool{false, true} {
		name := "file"
		if directory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			path := filepath.Join(workspace, "replaced")
			if directory {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			exporter := nativeExport{ctx: t.Context(), root: root, archive: tar.NewWriter(io.Discard)}
			done := make(chan error, 1)
			go func() {
				if directory {
					done <- exporter.walk("replaced", 0)
				} else {
					done <- exporter.append("replaced")
				}
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("replacement FIFO accepted")
				}
			case <-time.After(time.Second):
				// Release a blocking regression before reporting it.
				peer, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err == nil {
					peer.Close()
				}
				t.Fatal("opening the replacement FIFO blocked")
			}
		})
	}
}
