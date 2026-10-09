//go:build linux

package agenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/fileservicetest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

type treeService struct {
	sandboxfs.Service
	intercept                     func(sandboxfs.Service) sandboxfs.Service
	opens, releases, active, peak atomic.Int32
	read                          func(context.Context) error
	opened                        func(context.Context)
	shortRead                     bool
	caps                          func(*sandboxfs.Capabilities)
	readDone                      chan struct{}
	releaseError                  bool
	maxHandles                    uint32
	held, refused                 atomic.Int32
}

func (s *treeService) Describe(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.DescribeRequest) (*sandboxfs.DescribeResponse, error) {
	r, err := s.Service.Describe(ctx, a, q)
	if err == nil && s.maxHandles != 0 {
		r.Capabilities.MaxOpenHandles = s.maxHandles
	}
	if err == nil && s.caps != nil {
		s.caps(&r.Capabilities)
	}
	return r, err
}
func (s *treeService) Open(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.OpenRequest) (*sandboxfs.OpenResponse, error) {
	if s.maxHandles != 0 && s.held.Add(1) > int32(s.maxHandles) {
		s.held.Add(-1)
		s.refused.Add(1)
		return nil, sandboxfs.NewFailure(sandboxfs.CodeResourceExhausted, sandboxwire.EffectNone, "declared handle limit reached")
	}
	r, err := s.Service.Open(ctx, a, q)
	if err != nil && s.maxHandles != 0 {
		s.held.Add(-1)
	}
	if err == nil {
		s.opens.Add(1)
		if s.opened != nil {
			s.opened(ctx)
		}
	}
	return r, err
}
func (s *treeService) Read(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReadRequest) (*sandboxfs.ReadResponse, error) {
	n := s.active.Add(1)
	defer func() {
		s.active.Add(-1)
		if s.readDone != nil {
			s.readDone <- struct{}{}
		}
	}()
	for old := s.peak.Load(); n > old; old = s.peak.Load() {
		if s.peak.CompareAndSwap(old, n) {
			break
		}
	}
	if s.read != nil {
		if err := s.read(ctx); err != nil {
			return nil, err
		}
	}
	r, err := s.Service.Read(ctx, a, q)
	if err == nil && s.shortRead && len(r.Data) != 0 {
		r.Data = r.Data[:len(r.Data)-1]
	}
	return r, err
}
func (s *treeService) OpenTree(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.OpenTreeRequest) (*sandboxfs.OpenTreeResponse, error) {
	if s.maxHandles != 0 && s.held.Add(1) > int32(s.maxHandles) {
		s.held.Add(-1)
		s.refused.Add(1)
		return nil, sandboxfs.NewFailure(sandboxfs.CodeResourceExhausted, sandboxwire.EffectNone, "declared handle limit reached")
	}
	r, err := s.Service.OpenTree(ctx, a, q)
	if err != nil && s.maxHandles != 0 {
		s.held.Add(-1)
	}
	if err == nil {
		s.opens.Add(1)
		if s.opened != nil {
			s.opened(ctx)
		}
	}
	return r, err
}
func (s *treeService) Release(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReleaseRequest) (*sandboxfs.ReleaseResponse, error) {
	if s.releaseError {
		return nil, sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectNone, "release refused")
	}
	r, err := s.Service.Release(ctx, a, q)
	if err == nil {
		s.releases.Add(1)
		if s.maxHandles != 0 {
			s.held.Add(-1)
		}
	}
	return r, err
}
func treeWorld(t *testing.T, s *treeService, files map[string]string) (*world, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0500); err != nil {
			t.Fatal(err)
		}
	}
	server, err := fileservicetest.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	server.Intercept(func(real sandboxfs.Service) sandboxfs.Service {
		s.Service = real
		if s.intercept != nil {
			return s.intercept(s)
		}
		return s
	})
	stream, err := server.Dial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	uncertain := false
	w, err := attachWorld(t.Context(), stream, &uncertain)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.c.Close() })
	return w, dir
}
func treeReleased(t *testing.T, w *world, s *treeService) {
	t.Helper()
	if s.active.Load() != 0 || s.opens.Load() != s.releases.Load() {
		t.Fatalf("unfinished reads: active=%d open=%d release=%d", s.active.Load(), s.opens.Load(), s.releases.Load())
	}
	nodes := make([]sandboxfs.NodeRef, 0, len(w.refs))
	for node := range w.refs {
		nodes = append(nodes, node)
	}
	if err := w.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		_, err := w.c.GetAttr(t.Context(), &sandboxfs.GetAttrRequest{Target: sandboxfs.Target{Kind: sandboxfs.TargetNode, Node: node}})
		var f *sandboxfs.Failure
		if !errors.As(err, &f) || f.Code != sandboxfs.CodeStaleNode {
			t.Fatalf("forgotten node remained live: %v", err)
		}
	}
}

func TestReadTreeResultOwnership(t *testing.T) {
	for _, mode := range []string{"success", "read_error", "parent_cancel", "acquire_cancel", "short_read"} {
		t.Run(mode, func(t *testing.T) {
			s := &treeService{maxHandles: 1, readDone: make(chan struct{}, 1)}
			contents := map[string]string{"a": "one", "dir/b": "two", "dir/nested/c": "three", "z": "four"}
			w, _ := treeWorld(t, s, contents)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := make(chan struct{}, 1)
			if mode == "acquire_cancel" {
				s.opened = func(ctx context.Context) { entered <- struct{}{}; <-ctx.Done() }
			}
			if mode == "parent_cancel" {
				s.read = func(ctx context.Context) error { entered <- struct{}{}; <-ctx.Done(); return ctx.Err() }
			}
			if mode == "read_error" {
				s.read = func(context.Context) error {
					return sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectNone, "read refused")
				}
			}
			s.shortRead = mode == "short_read"
			type result struct {
				files []agentbundle.File
				err   error
			}
			done := make(chan result, 1)
			go func() { f, e := w.readTree(ctx, w.root, true); done <- result{f, e} }()
			if mode == "acquire_cancel" || mode == "parent_cancel" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("operation did not enter")
				}
				if _, err := w.c.Describe(t.Context(), &sandboxfs.DescribeRequest{}); err != nil {
					t.Fatal(err)
				}
				cancel()
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("read did not settle")
			}
			if mode == "success" {
				if got.err != nil || len(got.files) != len(contents) {
					t.Fatalf("read tree: %v %v", got.files, got.err)
				}
				names := []string{"a", "dir/b", "dir/nested/c", "z"}
				for i, f := range got.files {
					if f.Path != names[i] || string(f.Data) != contents[f.Path] || !f.Executable {
						t.Fatalf("file: %+v", f)
					}
				}
			} else if got.err == nil || got.files != nil {
				t.Fatalf("failed read returned files: %v %v", got.files, got.err)
			}
			if mode != "acquire_cancel" {
				select {
				case <-s.readDone:
				case <-time.After(5 * time.Second):
					t.Fatal("read handler not finished")
				}
			}
			if s.opens.Load() != 1 || s.held.Load() != 0 || s.refused.Load() != 0 {
				t.Fatalf("handle ownership: opens=%d held=%d refused=%d", s.opens.Load(), s.held.Load(), s.refused.Load())
			}
			treeReleased(t, w, s)
		})
	}
}

func TestReadTreeValidation(t *testing.T) {
	for _, mode := range []string{"writable", "symlink", "size_sum", "entry_count", "readback_tamper", "release_error"} {
		t.Run(mode, func(t *testing.T) {
			s := &treeService{}
			w, dir := treeWorld(t, s, map[string]string{"a": "original", "b": "second"})
			switch mode {
			case "writable":
				if err := os.Chmod(filepath.Join(dir, "a"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("a", filepath.Join(dir, "c")); err != nil {
					t.Fatal(err)
				}
			case "size_sum":
				for _, name := range []string{"a", "b"} {
					p := filepath.Join(dir, name)
					if err := os.Chmod(p, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Truncate(p, agentbundle.MaxExpandedBytes); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(p, 0400); err != nil {
						t.Fatal(err)
					}
				}
			case "entry_count":
				for i := range agentbundle.MaxFiles {
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), nil, 0400); err != nil {
						t.Fatal(err)
					}
				}
			case "readback_tamper":
				if _, err := w.readTree(t.Context(), w.root, true); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(dir, "a"), 0600); err != nil {
					t.Fatal(err)
				}
			case "release_error":
				s.releaseError = true
			}
			files, err := w.readTree(t.Context(), w.root, true)
			if err == nil || files != nil {
				t.Fatalf("invalid tree accepted: %v %v", files, err)
			}
			if mode == "release_error" {
				if !w.ended() {
					t.Fatal("failed release left attachment reusable")
				}
				if _, err := w.c.Describe(t.Context(), &sandboxfs.DescribeRequest{}); !errors.Is(err, sandboxfs.ErrTransport) {
					t.Fatalf("failed attachment admitted a call: %v", err)
				}
				return
			}
			if slices.Contains([]string{"writable", "symlink", "size_sum", "entry_count"}, mode) && s.opens.Load() != 0 {
				t.Fatalf("published invalid result: %d", s.opens.Load())
			}
			treeReleased(t, w, s)
		})
	}
}

func TestReadTreeCapabilitiesRequired(t *testing.T) {
	for _, mode := range []string{"unsupported", "entries", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			server, err := fileservicetest.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			s := &treeService{caps: func(c *sandboxfs.Capabilities) {
				switch mode {
				case "unsupported":
					c.MaxTreeEntries = 0
					c.MaxTreeDataBytes = 0
				case "entries":
					c.MaxTreeEntries = agentbundle.MaxFiles - 1
				case "bytes":
					c.MaxTreeDataBytes = agentbundle.MaxExpandedBytes - 1
				}
			}}
			server.Intercept(func(real sandboxfs.Service) sandboxfs.Service { s.Service = real; return s })
			stream, err := server.Dial(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			uncertain := false
			w, err := attachWorld(t.Context(), stream, &uncertain)
			var failure *sandboxfs.Failure
			if w != nil || !errors.As(err, &failure) || failure.Code != sandboxfs.CodeUnsupported {
				t.Fatalf("unsupported trees: world=%v err=%v", w, err)
			}
		})
	}
}

func TestReadTreeLargeFileAndFreshRead(t *testing.T) {
	s := &treeService{maxHandles: 1}
	w, dir := treeWorld(t, s, nil)
	name := filepath.Join(dir, "large")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0400)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 64<<10)
	for i := range chunk {
		chunk[i] = byte(i % 251)
	}
	for range agentbundle.MaxExpandedBytes / len(chunk) {
		if _, err = f.Write(chunk); err != nil {
			f.Close()
			t.Fatal(err)
		}
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := w.readTree(t.Context(), w.root, true)
	if err != nil || len(got) != 1 || len(got[0].Data) != agentbundle.MaxExpandedBytes {
		t.Fatalf("large tree: files=%d err=%v", len(got), err)
	}
	if got[0].Path != "large" || got[0].Executable {
		t.Fatal("large file attributes changed")
	}
	for i, b := range got[0].Data {
		if b != chunk[i%len(chunk)] {
			t.Fatalf("byte %d changed", i)
		}
	}
	if err = os.Chmod(name, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, []byte("replacement"), 0400); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(name, 0400); err != nil {
		t.Fatal(err)
	}
	next, err := w.readTree(t.Context(), w.root, true)
	if err != nil || len(next) != 1 || string(next[0].Data) != "replacement" {
		t.Fatalf("fresh read: %v %v", next, err)
	}
	if len(got[0].Data) != agentbundle.MaxExpandedBytes || got[0].Data[0] != 0 {
		t.Fatal("later read mutated caller-owned bytes")
	}
	treeReleased(t, w, s)
}
