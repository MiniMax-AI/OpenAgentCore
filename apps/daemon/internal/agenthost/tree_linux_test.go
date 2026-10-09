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
	opens, releases, active, peak atomic.Int32
	read                          func(context.Context) error
	opened                        func(context.Context)
	listed                        func(*sandboxfs.ReadDirResponse)
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
	return s.Service.Read(ctx, a, q)
}
func (s *treeService) ReadDir(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReadDirRequest) (*sandboxfs.ReadDirResponse, error) {
	r, err := s.Service.ReadDir(ctx, a, q)
	if err == nil && s.listed != nil {
		s.listed(r)
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
	server.Intercept(func(real sandboxfs.Service) sandboxfs.Service { s.Service = real; return s })
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

func TestReadTreeConcurrent(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		limit int
	}{
		{"success", 4}, {"read_error", 4}, {"parent_cancel", 4}, {"open_cancel", 4},
		{"success", 1}, {"success", 2}, {"success", 3},
	} {
		mode, limit := tc.mode, tc.limit
		t.Run(fmt.Sprintf("%s/handles_%d", mode, limit), func(t *testing.T) {
			s := &treeService{readDone: make(chan struct{}, 8), maxHandles: uint32(limit)}
			files := map[string]string{}
			for i := range 8 {
				files[fmt.Sprintf("dir/%02d", i)] = fmt.Sprint(i)
			}
			w, _ := treeWorld(t, s, files)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := make(chan struct{}, 8)
			gate := make(chan struct{})
			gateClosed := false
			defer func() {
				if !gateClosed {
					close(gate)
				}
			}()
			if mode == "open_cancel" {
				s.opened = func(ctx context.Context) { entered <- struct{}{}; <-ctx.Done() }
			} else {
				var first atomic.Bool
				s.read = func(ctx context.Context) error {
					entered <- struct{}{}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-gate:
					}
					if mode == "read_error" && first.CompareAndSwap(false, true) {
						return sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectNone, "read refused")
					}
					return nil
				}
			}
			type result struct {
				files []agentbundle.File
				err   error
			}
			done := make(chan result, 1)
			go func() { f, e := w.readTree(ctx, w.root, true); done <- result{f, e} }()
			for range limit {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("declared number of reads did not start")
				}
			}
			if s.opens.Load() != int32(limit) {
				t.Fatalf("opened %d files before releasing bound", s.opens.Load())
			}
			if mode == "parent_cancel" || mode == "open_cancel" {
				// A completed call fences the preceding request writes: this
				// case exercises cancellation after dispatch, not a torn frame.
				if _, err := w.c.Describe(t.Context(), &sandboxfs.DescribeRequest{}); err != nil {
					t.Fatal(err)
				}
				cancel()
			} else {
				close(gate)
				gateClosed = true
			}
			var r result
			select {
			case r = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("read did not join")
			}
			if mode == "success" {
				if r.err != nil || len(r.files) != len(files) {
					t.Fatalf("tree result: %v %v", r.files, r.err)
				}
				names := make([]string, 0, len(files))
				for name := range files {
					names = append(names, name)
				}
				slices.Sort(names)
				for i, f := range r.files {
					if f.Path != names[i] || string(f.Data) != files[f.Path] || !f.Executable {
						t.Fatalf("incorrect ordered file: %+v", f)
					}
				}
			} else if r.err == nil || r.files != nil {
				t.Fatalf("failure returned tree: %v %v", r.files, r.err)
			}
			if mode != "open_cancel" && s.peak.Load() != int32(limit) {
				t.Fatalf("read concurrency %d, want %d", s.peak.Load(), limit)
			}
			if mode != "open_cancel" {
				n := 8
				if mode == "parent_cancel" {
					n = limit
				}
				for range n {
					select {
					case <-s.readDone:
					case <-time.After(5 * time.Second):
						t.Fatal("File read handler did not finish")
					}
				}
			}
			if s.held.Load() != 0 || s.refused.Load() != 0 {
				t.Fatalf("handle bound exceeded or leaked: held=%d refused=%d", s.held.Load(), s.refused.Load())
			}
			treeReleased(t, w, s)
		})
	}
}

func TestReadTreeValidation(t *testing.T) {
	for _, mode := range []string{"writable", "symlink", "size_sum", "entry_count", "grow", "shrink", "readback_tamper", "release_error"} {
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
				s.listed = func(r *sandboxfs.ReadDirResponse) {
					for i := range r.Entries {
						r.Entries[i].Entry.Attr.Size = uint64(agentbundle.MaxExpandedBytes)
					}
				}
			case "entry_count":
				for i := range agentbundle.MaxFiles {
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), nil, 0400); err != nil {
						t.Fatal(err)
					}
				}
			case "grow", "shrink":
				s.listed = func(r *sandboxfs.ReadDirResponse) {
					for i := range r.Entries {
						if string(r.Entries[i].Name) == "a" {
							if mode == "grow" {
								r.Entries[i].Entry.Attr.Size--
							} else {
								r.Entries[i].Entry.Attr.Size++
							}
						}
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
				t.Fatalf("opened files before validating metadata: %d", s.opens.Load())
			}
			treeReleased(t, w, s)
		})
	}
}
