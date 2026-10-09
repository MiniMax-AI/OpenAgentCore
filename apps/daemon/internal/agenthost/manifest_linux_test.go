//go:build linux

package agenthost

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/fileservicetest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

var manifestIdentity = agentcapabilities.Identity{EnvironmentID: "a1000000-0000-4000-8000-000000000001", SessionID: "a1000000-0000-4000-8000-000000000002"}

type manifestService struct {
	sandboxfs.Service
	delay                             time.Duration
	lookups, opens, creates, releases atomic.Int32
	afterLookup                       func() error
	readNotFound                      bool
	readError                         bool
}

func (s *manifestService) Lookup(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.LookupRequest) (*sandboxfs.LookupResponse, error) {
	time.Sleep(s.delay)
	s.lookups.Add(1)
	r, err := s.Service.Lookup(ctx, a, q)
	if err == nil && string(q.Name) == agentcapabilities.ManifestName && s.afterLookup != nil {
		err = s.afterLookup()
		s.afterLookup = nil
	}
	return r, err
}
func (s *manifestService) Open(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.OpenRequest) (*sandboxfs.OpenResponse, error) {
	time.Sleep(s.delay)
	r, e := s.Service.Open(ctx, a, q)
	if e == nil {
		s.opens.Add(1)
	}
	return r, e
}
func (s *manifestService) Create(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.CreateRequest) (*sandboxfs.CreateResponse, error) {
	r, err := s.Service.Create(ctx, a, q)
	if err == nil {
		s.creates.Add(1)
	}
	return r, err
}
func (s *manifestService) Read(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReadRequest) (*sandboxfs.ReadResponse, error) {
	time.Sleep(s.delay)
	if s.readNotFound {
		return nil, sandboxfs.NewErrnoFailure(sandboxfs.ErrnoNotFound, sandboxwire.EffectNone, "read refused")
	}
	if s.readError {
		return nil, sandboxfs.NewErrnoFailure(sandboxfs.ErrnoPermissionDenied, sandboxwire.EffectNone, "read refused")
	}
	return s.Service.Read(ctx, a, q)
}
func (s *manifestService) Release(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReleaseRequest) (*sandboxfs.ReleaseResponse, error) {
	time.Sleep(s.delay)
	r, e := s.Service.Release(ctx, a, q)
	if e == nil {
		s.releases.Add(1)
	}
	return r, e
}

func manifestWorld(t testing.TB, service *manifestService) (*world, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	snapshot, err := agentcapabilities.NewSnapshot(agentcapabilities.Input{}, manifestIdentity, func(string) ([]fs.DirEntry, error) { return nil, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := snapshot.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, agentcapabilities.ManifestName)
	if err = os.WriteFile(name, body, 0400); err != nil {
		t.Fatal(err)
	}
	server, err := fileservicetest.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	server.Intercept(func(s sandboxfs.Service) sandboxfs.Service { service.Service = s; return service })
	stream, err := server.Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	uncertain := false
	w, err := attachWorld(context.Background(), stream, &uncertain)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.c.Close() })
	return w, name, body
}

func TestManifestSnapshotRead(t *testing.T) {
	for _, name := range []string{"existing", "missing_completed", "missing_unfinished", "directory", "symlink", "writable", "oversize", "invalid", "foreign", "replaced", "unlinked", "read_error", "read_missing_unfinished"} {
		t.Run(name, func(t *testing.T) {
			service := &manifestService{}
			w, path, body := manifestWorld(t, service)
			completed := true
			wantErr := false
			wantLookups := int32(1)
			var err error
			switch name {
			case "missing_completed":
				err = os.Remove(path)
				wantErr = true
			case "missing_unfinished":
				err = os.Remove(path)
				completed = false
				wantLookups = 2
			case "directory":
				err = os.Remove(path)
				if err == nil {
					err = os.Mkdir(path, 0700)
				}
				wantErr = true
			case "symlink":
				err = os.Remove(path)
				if err == nil {
					err = os.Symlink("outside", path)
				}
				wantErr = true
			case "writable":
				err = os.Chmod(path, 0600)
				wantErr = true
			case "oversize":
				err = os.Chmod(path, 0600)
				if err == nil {
					err = os.Truncate(path, int64(agentcapabilities.MaxManifestBytes)+1)
				}
				if err == nil {
					err = os.Chmod(path, 0400)
				}
				wantErr = true
			case "invalid":
				err = os.Remove(path)
				if err == nil {
					err = os.WriteFile(path, []byte("invalid"), 0400)
				}
				wantErr = true
			case "foreign":
				body = bytes.ReplaceAll(body, []byte(manifestIdentity.SessionID), []byte("a1000000-0000-4000-8000-000000000099"))
				err = os.Remove(path)
				if err == nil {
					err = os.WriteFile(path, body, 0400)
				}
				wantErr = true
			case "replaced":
				service.afterLookup = func() error {
					if e := os.Rename(path, path+".old"); e != nil {
						return e
					}
					return os.WriteFile(path, []byte("replacement is not the referenced manifest"), 0400)
				}
			case "unlinked":
				service.afterLookup = func() error { return os.Remove(path) }
			case "read_missing_unfinished":
				service.readNotFound = true
				completed = false
				wantErr = true
			case "read_error":
				service.readError = true
				wantErr = true
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = (&environment{}).snapshot(context.Background(), w, w.root, agentcapabilities.Input{}, manifestIdentity, completed)
			if wantErr {
				if !errors.Is(err, agentcapabilities.ErrInvalid) {
					t.Fatalf("error=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := service.lookups.Load(); got != wantLookups {
				t.Fatalf("lookups=%d, want %d", got, wantLookups)
			}
			if name == "oversize" && service.opens.Load() != 0 {
				t.Fatal("oversized manifest opened before its size was rejected")
			}
			if name == "read_missing_unfinished" && service.creates.Load() != 0 {
				t.Fatal("read failure finalized an existing manifest")
			}
			if service.opens.Load()+service.creates.Load() != service.releases.Load() {
				t.Fatalf("open=%d create=%d release=%d", service.opens.Load(), service.creates.Load(), service.releases.Load())
			}
			refs := make([]sandboxfs.NodeRef, 0, len(w.refs))
			for ref := range w.refs {
				refs = append(refs, ref)
			}
			if err = w.forget(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(w.refs) != 0 {
				t.Fatal("operation retained refs")
			}
			for _, ref := range refs {
				_, err = w.c.GetAttr(context.Background(), &sandboxfs.GetAttrRequest{Target: sandboxfs.Target{Kind: sandboxfs.TargetNode, Node: ref}})
				var failure *sandboxfs.Failure
				if !errors.As(err, &failure) || failure.Code != sandboxfs.CodeStaleNode {
					t.Fatalf("forgotten node: %v", err)
				}
			}
			if name == "replaced" || name == "unlinked" {
				if _, err = (&environment{}).snapshot(context.Background(), w, w.root, agentcapabilities.Input{}, manifestIdentity, true); !errors.Is(err, agentcapabilities.ErrInvalid) {
					t.Fatalf("next operation reused old entry: %v", err)
				}
				if err = w.forget(context.Background()); err != nil {
					t.Fatal(err)
				}
			}

		})
	}
}

// The same benchmark runs before and after the change against the real File
// service and framed client. Delay models each manifest read RPC, not a network.
func BenchmarkManifestSnapshot(b *testing.B) {
	for _, delay := range []time.Duration{0, time.Millisecond, 20 * time.Millisecond, 100 * time.Millisecond} {
		b.Run(delay.String(), func(b *testing.B) {
			service := &manifestService{delay: delay}
			w, _, _ := manifestWorld(b, service)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := (&environment{}).snapshot(context.Background(), w, w.root, agentcapabilities.Input{}, manifestIdentity, true); err != nil {
					b.Fatal(err)
				}
				if err := w.forget(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(service.lookups.Load())/float64(b.N), "lookups/op")
		})
	}
}
