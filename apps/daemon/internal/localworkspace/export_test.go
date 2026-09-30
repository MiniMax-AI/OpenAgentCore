package localworkspace

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func outputBinding(t *testing.T) *Binding {
	t.Helper()
	b := nativeFileBinding(t)
	if _, err := b.WriteWorkspaceFile(t.Context(), "outputs/nested/proof.bin", []byte{0, 255, 17}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteWorkspaceFile(t.Context(), "outputs/empty", nil); err != nil {
		t.Fatal(err)
	}
	return b
}
func TestNativeOutputArchive(t *testing.T) {
	b := outputBinding(t)
	var output bytes.Buffer
	if err := b.ExportOutputs(t.Context(), &output); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&output)
	got := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatal(header)
		}
		raw, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		got[header.Name] = raw
	}
	if len(got) != 2 || !bytes.Equal(got["outputs/nested/proof.bin"], []byte{0, 255, 17}) {
		t.Fatal(got)
	}
}
func TestNativeExportMissingOutputsAndBound(t *testing.T) {
	b := nativeFileBinding(t)
	var output bytes.Buffer
	if err := b.ExportOutputs(t.Context(), &output); err != nil {
		t.Fatal(err)
	}
	if _, err := tar.NewReader(&output).Next(); err != io.EOF {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(b.workspace, "outputs"), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(b.workspace, "outputs", "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(artifactFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err = b.ExportOutputs(t.Context(), io.Discard); err == nil {
		t.Fatal("oversize artifact accepted")
	}
}

type failedExportWriter struct{ err error }

func (w failedExportWriter) Write([]byte) (int, error) { return 0, w.err }
func TestNativeExportCancellationAndConsumerFailure(t *testing.T) {
	b := outputBinding(t)
	want := errors.New("consumer rejected")
	if err := b.ExportOutputs(t.Context(), failedExportWriter{want}); !errors.Is(err, want) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	defer reader.Close()
	stop := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()) })
	defer stop()
	done := make(chan error, 1)
	go func() { err := b.ExportOutputs(ctx, writer); _ = writer.CloseWithError(err); done <- err }()
	var prefix [1]byte
	if _, err := reader.Read(prefix[:]); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled export succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("export did not settle")
	}
}

type changingOutput struct {
	bytes.Buffer
	change func()
}

func (w *changingOutput) Write(data []byte) (int, error) {
	if w.change != nil {
		change := w.change
		w.change = nil
		change()
	}
	return w.Buffer.Write(data)
}
func TestNativeExportRejectsChangedFile(t *testing.T) {
	b := nativeFileBinding(t)
	if _, err := b.WriteWorkspaceFile(t.Context(), "outputs/file", []byte("before")); err != nil {
		t.Fatal(err)
	}
	w := &changingOutput{change: func() {
		if err := os.WriteFile(filepath.Join(b.workspace, "outputs", "file"), []byte("changed-length"), 0600); err != nil {
			t.Error(err)
		}
	}}
	if err := b.ExportOutputs(t.Context(), w); err == nil {
		t.Fatal("changed artifact accepted")
	}
}
