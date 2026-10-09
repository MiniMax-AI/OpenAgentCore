//go:build linux

package agenthost

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func exportOwner(w *world) *environment {
	return &environment{id: "export", workspace: "/workspace", world: w, lost: new(atomic.Bool), sem: make(chan struct{}, 1)}
}

func readExport(t *testing.T, data []byte) ([]string, map[string]string) {
	t.Helper()
	r := tar.NewReader(bytes.NewReader(data))
	var order []string
	files := map[string]string{}
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		order = append(order, h.Name)
		files[h.Name] = string(body)
	}
	return order, files
}

func TestExportOutputsPreservesOrderAndBounds(t *testing.T) {
	for _, limit := range []uint32{1, 2, 3, 4} {
		t.Run(string(rune('0'+limit)), func(t *testing.T) {
			s := &treeService{maxHandles: limit}
			files := map[string]string{"workspace/outputs/a": "a", "workspace/outputs/b/one": "1", "workspace/outputs/b/two": "2", "workspace/outputs/c": "", "workspace/outputs/d": strings.Repeat("large", (1<<20)/5+100), "workspace/outputs/e": "e", "workspace/outputs/f": "f", "workspace/outputs/g": "g"}
			w, dir := treeWorld(t, s, files)
			if err := os.Symlink("/outside", filepath.Join(dir, "workspace/outputs/b/link")); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := exportOwner(w).ExportOutputs(t.Context(), &out); err != nil {
				t.Fatal(err)
			}
			order, got := readExport(t, out.Bytes())
			want := []string{"outputs/a", "outputs/b/one", "outputs/b/two", "outputs/c", "outputs/d", "outputs/e", "outputs/f", "outputs/g"}
			if !reflect.DeepEqual(order, want) {
				t.Fatalf("order %v", order)
			}
			for name, body := range files {
				if got[strings.TrimPrefix(name, "workspace/")] != body {
					t.Fatalf("content %s", name)
				}
			}
			if len(w.refs) != 0 || s.held.Load() != 0 || s.refused.Load() != 0 || s.opens.Load() != s.releases.Load() {
				t.Fatalf("leaks/limit: refs=%d held=%d refused=%d opens=%d release=%d", len(w.refs), s.held.Load(), s.refused.Load(), s.opens.Load(), s.releases.Load())
			}
		})
	}
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "empty"}[empty], func(t *testing.T) {
			w, dir := treeWorld(t, &treeService{}, nil)
			p := "workspace"
			if empty {
				p += "/outputs"
			}
			if err := os.MkdirAll(filepath.Join(dir, p), 0700); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := exportOwner(w).ExportOutputs(t.Context(), &out); err != nil {
				t.Fatal(err)
			}
			order, _ := readExport(t, out.Bytes())
			if len(order) != 0 {
				t.Fatal(order)
			}
		})
	}
}

type exportReadService struct {
	sandboxfs.Service
	afterRead    func()
	openPossible bool
}

func (s *exportReadService) Read(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReadRequest) (*sandboxfs.ReadResponse, error) {
	r, err := s.Service.Read(ctx, a, q)
	if s.afterRead != nil {
		s.afterRead()
	}
	return r, err
}
func (s *exportReadService) Open(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.OpenRequest) (*sandboxfs.OpenResponse, error) {
	r, err := s.Service.Open(ctx, a, q)
	if err == nil && s.openPossible {
		return nil, sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectPossible, "uncertain open")
	}
	return r, err
}

func TestExportOutputsRejectsChangesAndSettlesOpen(t *testing.T) {
	for _, kind := range []string{"mtime", "size", "uncertain-open", "file-limit", "total-limit"} {
		t.Run(kind, func(t *testing.T) {
			proxy := &exportReadService{openPossible: kind == "uncertain-open"}
			s := &treeService{intercept: func(real sandboxfs.Service) sandboxfs.Service { proxy.Service = real; return proxy }}
			w, dir := treeWorld(t, s, map[string]string{"workspace/outputs/a": "abc"})
			p := filepath.Join(dir, "workspace/outputs/a")
			if err := os.Chmod(p, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "mtime":
				proxy.afterRead = func() {
					if err := os.Chtimes(p, time.Unix(1, 0), time.Unix(2, 0)); err != nil {
						t.Error(err)
					}
				}
			case "size":
				proxy.afterRead = func() {
					if err := os.Truncate(p, 1); err != nil {
						t.Error(err)
					}
				}
			case "file-limit":
				if err := os.Truncate(p, exportFileBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			var err error
			if kind == "total-limit" {
				e, eerr := w.lookup(t.Context(), w.root, "workspace")
				if eerr != nil {
					t.Fatal(eerr)
				}
				e, eerr = w.lookup(t.Context(), e.Node, "outputs")
				if eerr != nil {
					t.Fatal(eerr)
				}
				x := export{w: w, archive: tar.NewWriter(&out), bytes: exportBatchBytes - 2}
				err = x.walk(t.Context(), e.Node, "outputs", 0)
			} else {
				err = exportOwner(w).ExportOutputs(t.Context(), &out)
			}
			if err == nil {
				t.Fatal("invalid export succeeded")
			}
			if s.opens.Load() != s.releases.Load() {
				select {
				case <-w.c.Done():
				default:
					t.Fatalf("unconfirmed cleanup left admission open: open=%d release=%d", s.opens.Load(), s.releases.Load())
				}
			}
		})
	}
}

type exportFailWriter struct{}

func (exportFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportOutputsJoinsWorkersOnError(t *testing.T) {
	for _, kind := range []string{"cancel", "writer", "read", "release"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan struct{}, 4)
			s := &treeService{releaseError: kind == "release"}
			s.read = func(ctx context.Context) error {
				started <- struct{}{}
				if kind == "cancel" {
					<-ctx.Done()
					return ctx.Err()
				}
				if kind == "read" {
					return sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectNone, "read failed")
				}
				return nil
			}
			w, _ := treeWorld(t, s, map[string]string{"workspace/outputs/a": "a", "workspace/outputs/b": "b", "workspace/outputs/c": "c", "workspace/outputs/d": "d", "workspace/outputs/e": "unadmitted"})
			var out io.Writer = io.Discard
			if kind == "writer" {
				out = exportFailWriter{}
			}
			done := make(chan error, 1)
			go func() { done <- exportOwner(w).ExportOutputs(ctx, out) }()
			if kind == "cancel" {
				for range 4 {
					select {
					case <-started:
					case <-time.After(5 * time.Second):
						t.Fatal("read not admitted")
					}
				}
				// Fence the client write slot before cancelling: all four requests
				// have been sent, rather than cancellation racing a frame write.
				if _, err := w.c.GetAttr(t.Context(), &sandboxfs.GetAttrRequest{Target: sandboxfs.Target{Kind: sandboxfs.TargetNode, Node: w.root}}); err != nil {
					t.Fatal(err)
				}
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("export succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("export did not join")
			}
			if s.active.Load() != 0 {
				t.Fatal("read still running")
			}
			if s.opens.Load() > 4 {
				t.Fatalf("admitted next batch: %d", s.opens.Load())
			}
			if kind == "release" {
				select {
				case <-w.c.Done():
				default:
					t.Fatal("unconfirmed Release left admission open")
				}
			} else if s.opens.Load() != s.releases.Load() {
				select {
				case <-w.c.Done():
				default:
					t.Fatalf("unconfirmed cleanup left admission open: open=%d release=%d", s.opens.Load(), s.releases.Load())
				}
			}
		})
	}
}

func TestExportOutputsKeepsOpenedIdentity(t *testing.T) {
	s := &treeService{}
	w, dir := treeWorld(t, s, map[string]string{"workspace/outputs/a": "old"})
	p := filepath.Join(dir, "workspace/outputs/a")
	s.opened = func(context.Context) {
		if err := os.Rename(p, p+".moved"); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(p, []byte("replacement"), 0600); err != nil {
			t.Error(err)
		}
	}
	var out bytes.Buffer
	if err := exportOwner(w).ExportOutputs(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	_, files := readExport(t, out.Bytes())
	if files["outputs/a"] != "old" {
		t.Fatal(files)
	}
}

type exportCountWriter struct {
	total   int64
	largest int
}

func (w *exportCountWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	w.largest = max(w.largest, len(p))
	return len(p), nil
}

func TestExportOutputsStreamsMaximumFile(t *testing.T) {
	s := &treeService{maxHandles: 1}
	w, dir := treeWorld(t, s, map[string]string{"workspace/outputs/large": ""})
	p := filepath.Join(dir, "workspace/outputs/large")
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, exportFileBytes); err != nil {
		t.Fatal(err)
	}
	out := new(exportCountWriter)
	if err := exportOwner(w).ExportOutputs(t.Context(), out); err != nil {
		t.Fatal(err)
	}
	if out.total != exportFileBytes+1536 || out.largest > int(w.caps.MaxReadBytes) {
		t.Fatalf("unbounded/incorrect stream: %+v", out)
	}
	if s.opens.Load() != 1 || s.releases.Load() != 1 {
		t.Fatalf("open=%d release=%d", s.opens.Load(), s.releases.Load())
	}
}

func TestExportOutputsSettlesCancelledAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{}, 4)
	s := &treeService{maxHandles: 4, opened: func(ctx context.Context) { entered <- struct{}{}; <-ctx.Done() }}
	w, _ := treeWorld(t, s, map[string]string{"workspace/outputs/a": "a", "workspace/outputs/b": "b", "workspace/outputs/c": "c", "workspace/outputs/d": "d"})
	done := make(chan error, 1)
	go func() { done <- exportOwner(w).ExportOutputs(ctx, io.Discard) }()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("open not admitted")
		}
	}
	if _, err := w.c.GetAttr(t.Context(), &sandboxfs.GetAttrRequest{Target: sandboxfs.Target{Kind: sandboxfs.TargetNode, Node: w.root}}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled export succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("acquisition not settled")
	}
	if s.opens.Load() != 4 || s.releases.Load() != 4 || s.held.Load() != 0 {
		t.Fatalf("open=%d released=%d held=%d", s.opens.Load(), s.releases.Load(), s.held.Load())
	}
}

type exportWriterFunc func([]byte) (int, error)

func (f exportWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestExportOutputsWriterFailureCancelsBlockedReads(t *testing.T) {
	entered := make(chan struct{}, 4)
	s := &treeService{maxHandles: 4, read: func(ctx context.Context) error { entered <- struct{}{}; <-ctx.Done(); return ctx.Err() }}
	w, _ := treeWorld(t, s, map[string]string{"workspace/outputs/a": "a", "workspace/outputs/b": "b", "workspace/outputs/c": "c", "workspace/outputs/d": "d"})
	writer := exportWriterFunc(func([]byte) (int, error) {
		for range 4 {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				return 0, io.ErrNoProgress
			}
		}
		// Keep the parent live, with every Read frame fully sent and each service
		// method blocked on cancellation when the local writer fails.
		if _, err := w.c.GetAttr(t.Context(), &sandboxfs.GetAttrRequest{Target: sandboxfs.Target{Kind: sandboxfs.TargetNode, Node: w.root}}); err != nil {
			return 0, err
		}
		return 0, io.ErrClosedPipe
	})
	done := make(chan error, 1)
	go func() { done <- exportOwner(w).ExportOutputs(t.Context(), writer) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("writer failure ignored")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("writer failure left peer reads running")
	}
	if s.active.Load() != 0 || s.opens.Load() != 4 || s.releases.Load() != 4 || s.held.Load() != 0 {
		t.Fatalf("active=%d open=%d release=%d held=%d", s.active.Load(), s.opens.Load(), s.releases.Load(), s.held.Load())
	}
}
