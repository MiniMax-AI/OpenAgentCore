package localworkspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func exportBinding(t *testing.T, program string) *Binding {
	t.Helper()
	b, _ := testBinding(t)
	b.exportHelper = b.helper
	if err := os.WriteFile(b.helper, []byte("#!/bin/sh\n"+program+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return b
}

type heldExportWriter struct {
	bytes.Buffer
	entered chan struct{}
	release chan struct{}
}

func (w *heldExportWriter) Write(p []byte) (int, error) {
	if w.entered != nil {
		close(w.entered)
		w.entered = nil
		<-w.release
	}
	return w.Buffer.Write(p)
}

func TestExportOutputsAllowsConsumerBackpressureAfterHelperExit(t *testing.T) {
	b := exportBinding(t, "head -c 3072 /dev/zero")
	entered, release := make(chan struct{}), make(chan struct{})
	w := &heldExportWriter{entered: entered, release: release}
	done := make(chan error, 1)
	go func() { done <- b.ExportOutputs(t.Context(), w) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("helper produced no output")
	}
	// The helper can exit while the pull transport is waiting for its consumer.
	time.Sleep(1500 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), make([]byte, 3072)) {
		t.Fatalf("incomplete output: %d bytes", w.Len())
	}
}

func TestExportOutputsRequiresSuccessfulHelperExit(t *testing.T) {
	b := exportBinding(t, "printf complete-prefix; exit 7")
	var output bytes.Buffer
	if err := b.ExportOutputs(t.Context(), &output); err == nil || output.String() != "complete-prefix" {
		t.Fatalf("failed helper accepted: output=%q error=%v", output.String(), err)
	}
}

func TestExportOutputsBoundsInheritedPipeAfterHelperExit(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	b := exportBinding(t, "sleep 30 &\necho $! > '"+pidFile+"'\nprintf prefix")
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var output bytes.Buffer
	err := b.ExportOutputs(ctx, &output)
	if !errors.Is(err, os.ErrDeadlineExceeded) || ctx.Err() != nil || output.String() != "prefix" {
		t.Fatalf("inherited pipe not bounded: output=%q error=%v context=%v", output.String(), err, ctx.Err())
	}
}

func TestExportOutputsCancellationUsesConsumerCloseContract(t *testing.T) {
	b := exportBinding(t, "printf prefix; exec sleep 30")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	// The dispatch owner closes the consumer on cancellation, releasing Write.
	stop := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()) })
	defer stop()
	done := make(chan error, 1)
	go func() { err := b.ExportOutputs(ctx, writer); _ = writer.CloseWithError(err); done <- err }()
	got := make([]byte, 1)
	if _, err := reader.Read(got); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled export succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not settle exporter")
	}
}

type failedExportWriter struct{ err error }

func (w failedExportWriter) Write([]byte) (int, error) { return 0, w.err }

func TestExportOutputsConsumerFailureStopsHelper(t *testing.T) {
	b := exportBinding(t, "printf prefix; exec sleep 30")
	want := errors.New("consumer rejected output")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := b.ExportOutputs(ctx, failedExportWriter{want}); !errors.Is(err, want) || ctx.Err() != nil {
		t.Fatalf("consumer failure did not stop helper: %v context=%v", err, ctx.Err())
	}
}
