//go:build linux

package agenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// writeTreeService uses the real File service; hooks hold replies or inject
// typed failures around its operations.
type writeTreeService struct {
	sandboxfs.Service
	mu                                                 sync.Mutex
	limit                                              uint32
	handles                                            map[sandboxfs.HandleID]string
	peak, refused, creates, fileClosed, directorySyncs int
	partialWrite                                       bool
	beforeWrite                                        func(context.Context, sandboxfs.HandleID) error
	after                                              func(context.Context, string, sandboxfs.HandleID) error
}

func (s *writeTreeService) Describe(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.DescribeRequest) (*sandboxfs.DescribeResponse, error) {
	r, e := s.Service.Describe(ctx, a, q)
	if e == nil {
		r.Capabilities.MaxOpenHandles = s.limit
	}
	return r, e
}
func (s *writeTreeService) reserve(h sandboxfs.HandleID, kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.handles) >= int(s.limit) {
		s.refused++
		return sandboxfs.NewFailure(sandboxfs.CodeResourceExhausted, sandboxwire.EffectNone, "handle limit")
	}
	s.handles[h] = kind
	s.peak = max(s.peak, len(s.handles))
	return nil
}
func (s *writeTreeService) drop(h sandboxfs.HandleID) {
	s.mu.Lock()
	delete(s.handles, h)
	s.mu.Unlock()
}
func (s *writeTreeService) hook(ctx context.Context, kind string, h sandboxfs.HandleID) error {
	if s.after != nil {
		return s.after(ctx, kind, h)
	}
	return nil
}
func (s *writeTreeService) Create(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.CreateRequest) (*sandboxfs.CreateResponse, error) {
	if err := s.reserve(q.Handle, "file"); err != nil {
		return nil, err
	}
	r, err := s.Service.Create(ctx, a, q)
	if err != nil {
		s.drop(q.Handle)
		return r, err
	}
	s.mu.Lock()
	s.creates++
	s.mu.Unlock()
	return r, s.hook(ctx, "create", q.Handle)
}
func (s *writeTreeService) OpenDir(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.OpenDirRequest) (*sandboxfs.OpenDirResponse, error) {
	if err := s.reserve(q.Handle, "directory"); err != nil {
		return nil, err
	}
	r, err := s.Service.OpenDir(ctx, a, q)
	if err != nil {
		s.drop(q.Handle)
	}
	return r, err
}
func (s *writeTreeService) Write(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.WriteRequest) (*sandboxfs.WriteResponse, error) {
	if s.beforeWrite != nil {
		if err := s.beforeWrite(ctx, q.Handle); err != nil {
			return nil, err
		}
	}
	if s.partialWrite {
		copy := *q
		copy.Data = q.Data[:max(1, len(q.Data)/2)]
		r, err := s.Service.Write(ctx, a, &copy)
		if err == nil {
			r.Failure = sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectNone, "write stopped after confirmed prefix")
		}
		return r, err
	}
	r, err := s.Service.Write(ctx, a, q)
	if err == nil {
		err = s.hook(ctx, "write", q.Handle)
	}
	return r, err
}
func (s *writeTreeService) Fsync(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.FsyncRequest) (*sandboxfs.FsyncResponse, error) {
	s.mu.Lock()
	kind := s.handles[q.Handle]
	if kind == "directory" {
		s.directorySyncs++
	}
	s.mu.Unlock()
	r, err := s.Service.Fsync(ctx, a, q)
	if err == nil {
		err = s.hook(ctx, kind+"_sync", q.Handle)
	}
	return r, err
}
func (s *writeTreeService) Release(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReleaseRequest) (*sandboxfs.ReleaseResponse, error) {
	r, err := s.Service.Release(ctx, a, q)
	if err == nil {
		s.drop(q.Handle)
		s.mu.Lock()
		s.fileClosed++
		s.mu.Unlock()
		err = s.hook(ctx, "release", q.Handle)
	}
	return r, err
}
func (s *writeTreeService) ReleaseDir(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReleaseDirRequest) (*sandboxfs.ReleaseDirResponse, error) {
	r, err := s.Service.ReleaseDir(ctx, a, q)
	if err == nil {
		s.drop(q.Handle)
		err = s.hook(ctx, "directory_release", q.Handle)
	}
	return r, err
}
func writeWorld(t *testing.T, limit uint32, files map[string]string) (*world, string, *writeTreeService) {
	t.Helper()
	s := &writeTreeService{limit: limit, handles: map[sandboxfs.HandleID]string{}}
	w, dir := treeWorld(t, &treeService{intercept: func(real sandboxfs.Service) sandboxfs.Service { s.Service = real; return s }}, files)
	return w, dir, s
}
func TestWriteTreePhases(t *testing.T) {
	for _, limit := range []uint32{1, 2, 3, 4} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			w, dir, s := writeWorld(t, limit, nil)
			var files []agentbundle.File
			for i := range 8 {
				files = append(files, agentbundle.File{Path: fmt.Sprintf("nested/%02d", i), Data: []byte(fmt.Sprint(i)), Executable: i%2 == 0})
			}
			entered := make(chan struct{}, 8)
			gate := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(gate) })
			s.after = func(ctx context.Context, kind string, _ sandboxfs.HandleID) error {
				if kind == "file_sync" {
					entered <- struct{}{}
					select {
					case <-gate:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if kind == "directory_sync" {
					s.mu.Lock()
					n := s.fileClosed
					s.mu.Unlock()
					if n != len(files) {
						return fmt.Errorf("directory sync before file release: %d", n)
					}
				}
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- w.writeTree(t.Context(), w.root, "captured", files) }()
			for range limit {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("writers did not reach declared limit")
				}
			}
			once.Do(func() { close(gate) })
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("write did not join")
			}
			s.mu.Lock()
			held, peak, refused, closed, synced := len(s.handles), s.peak, s.refused, s.fileClosed, s.directorySyncs
			s.mu.Unlock()
			if held != 0 || peak != int(limit) || refused != 0 || closed != 8 || synced != 3 {
				t.Fatalf("held=%d peak=%d refused=%d closed=%d synced=%d", held, peak, refused, closed, synced)
			}
			for _, f := range files {
				p := filepath.Join(dir, "captured", f.Path)
				body, err := os.ReadFile(p)
				if err != nil || string(body) != string(f.Data) {
					t.Fatalf("file %s: %q %v", f.Path, body, err)
				}
				st, err := os.Stat(p)
				if err != nil || st.Mode().Perm()&0222 != 0 || (st.Mode().Perm()&0111 != 0) != f.Executable {
					t.Fatalf("mode %s: %v %v", p, st, err)
				}
			}
			if err := w.forget(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWriteTreeFailureStopsFinalization(t *testing.T) {
	for _, kind := range []string{"create", "write", "release", "directory_sync", "directory_release", "mixed", "partial_write"} {
		t.Run(kind, func(t *testing.T) {
			limit := uint32(1)
			if kind == "mixed" {
				limit = 2
			}
			w, dir, s := writeWorld(t, limit, map[string]string{"source/proof/SKILL.md": "---\nname: proof\ndescription: Test proof.\n---\nBody\n", "source/proof/a": "a", "source/proof/b": "b", "source/proof/c": "c"})
			s.partialWrite = kind == "partial_write"
			root, err := w.directory(t.Context(), w.root, agentcapabilities.Directory, true)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			calls := 0
			var firstHandle sandboxfs.HandleID
			both, firstReleased := make(chan struct{}), make(chan struct{})
			var bothOnce, releaseOnce sync.Once
			defer bothOnce.Do(func() { close(both) })
			defer releaseOnce.Do(func() { close(firstReleased) })
			if kind == "mixed" {
				s.beforeWrite = func(ctx context.Context, h sandboxfs.HandleID) error {
					mu.Lock()
					calls++
					n := calls
					if n == 1 {
						firstHandle = h
					}
					if n == 2 {
						bothOnce.Do(func() { close(both) })
					}
					mu.Unlock()
					select {
					case <-both:
					case <-ctx.Done():
						return ctx.Err()
					}
					if n == 1 {
						return sandboxfs.NewErrnoFailure(sandboxfs.ErrnoPermissionDenied, sandboxwire.EffectNone, "confirmed refusal")
					}
					return nil
				}
			}
			s.after = func(ctx context.Context, op string, h sandboxfs.HandleID) error {
				if kind == "mixed" {
					mu.Lock()
					first := firstHandle
					mu.Unlock()
					if op == "release" && h == first {
						releaseOnce.Do(func() { close(firstReleased) })
					}
					if op == "write" {
						select {
						case <-firstReleased:
						case <-ctx.Done():
							return ctx.Err()
						}
						return sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectPossible, "unconfirmed second write")
					}
				}
				s.mu.Lock()
				created, synced := s.creates, s.directorySyncs
				s.mu.Unlock()
				if op == kind && (kind != "release" || created != 0) && (kind != "directory_release" || synced != 0) {
					return sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectPossible, "unconfirmed "+kind)
				}
				return nil
			}
			err = w.finalize(t.Context(), root, agentcapabilities.Input{Directories: []string{"/source"}}, manifestIdentity)
			wantUncertain := kind != "partial_write"
			if err == nil || errors.Is(err, errUncertain) != wantUncertain || *w.uncertain != wantUncertain {
				t.Fatalf("failure classification: %v uncertain=%v want=%v", err, *w.uncertain, wantUncertain)
			}
			if _, err := os.Stat(filepath.Join(dir, agentcapabilities.Directory, agentcapabilities.ManifestName)); !os.IsNotExist(err) {
				t.Fatalf("published manifest after failure: %v", err)
			}
			s.mu.Lock()
			created, held, synced := s.creates, len(s.handles), s.directorySyncs
			s.mu.Unlock()
			if kind == "create" || kind == "write" || kind == "release" || kind == "partial_write" {
				if created != 1 || synced != 0 {
					t.Fatalf("new work after failure: created=%d synced=%d", created, synced)
				}
			}
			if kind == "mixed" && created != 2 {
				t.Fatalf("mixed errors did not remain one admitted batch: %d", created)
			}
			if kind == "partial_write" {
				body, readErr := os.ReadFile(filepath.Join(dir, agentcapabilities.Directory, "directories/0/proof/SKILL.md"))
				if readErr != nil || len(body) == 0 || len(body) >= len("---\nname: proof\ndescription: Test proof.\n---\nBody\n") {
					t.Fatalf("partial prefix: %q %v", body, readErr)
				}
			}
			if held != 0 {
				t.Fatalf("unreleased confirmed handles: %d", held)
			}
			if kind == "release" || kind == "directory_release" {
				if !w.ended() {
					t.Fatal("failed release did not close admission")
				}
			}
		})
	}
}

func TestWriteTreeCancelledCreate(t *testing.T) {
	w, _, s := writeWorld(t, 1, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	finished := make(chan struct{})
	s.after = func(ctx context.Context, kind string, _ sandboxfs.HandleID) error {
		if kind == "create" {
			close(started)
			<-ctx.Done()
			close(finished)
		}
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- w.writeTree(ctx, w.root, "captured", []agentbundle.File{{Path: "a", Data: []byte("a")}, {Path: "b", Data: []byte("b")}})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Create did not arrive")
	}
	if _, err := w.c.Describe(t.Context(), &sandboxfs.DescribeRequest{}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, errUncertain) || !*w.uncertain {
			t.Fatalf("cancel result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not settle")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("late Create handler did not finish")
	}
	s.mu.Lock()
	held, created, closed := len(s.handles), s.creates, s.fileClosed
	s.mu.Unlock()
	if held != 0 || created != 1 || closed != 1 {
		t.Fatalf("late handle/admission: held=%d created=%d closed=%d", held, created, closed)
	}
}

func TestPublishInitialFileReplacement(t *testing.T) {
	w, dir, s := writeWorld(t, 1, nil)
	for _, body := range []string{"first", "replacement"} {
		if err := w.publish(t.Context(), w.root, "initial.txt", 0600, []byte(body), true); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "initial.txt"))
		if err != nil || string(got) != body {
			t.Fatalf("published %q: %v", got, err)
		}
	}
	s.mu.Lock()
	held, refused := len(s.handles), s.refused
	s.mu.Unlock()
	if held != 0 || refused != 0 || *w.uncertain {
		t.Fatalf("publish ownership: held=%d refused=%d uncertain=%v", held, refused, *w.uncertain)
	}
	if err := w.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
}
