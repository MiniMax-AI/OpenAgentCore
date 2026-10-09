//go:build linux

package agenthost

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

type readFileService struct {
	sandboxfs.Service
	entry       sandboxfs.Entry
	data        []byte
	readFailure bool
	releases    atomic.Int32
}

func (s *readFileService) Lookup(context.Context, sandboxfs.Attachment, *sandboxfs.LookupRequest) (*sandboxfs.LookupResponse, error) {
	return &sandboxfs.LookupResponse{Entry: s.entry}, nil
}

func (s *readFileService) Open(context.Context, sandboxfs.Attachment, *sandboxfs.OpenRequest) (*sandboxfs.OpenResponse, error) {
	return &sandboxfs.OpenResponse{}, nil
}

func (s *readFileService) Read(_ context.Context, _ sandboxfs.Attachment, r *sandboxfs.ReadRequest) (*sandboxfs.ReadResponse, error) {
	if s.readFailure && r.Offset >= 4 {
		return nil, sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, sandboxwire.EffectNone, "read failed")
	}
	start := min(uint64(len(s.data)), r.Offset)
	end := min(uint64(len(s.data)), start+uint64(r.Size))
	return &sandboxfs.ReadResponse{Data: s.data[start:end]}, nil
}

func (s *readFileService) Release(context.Context, sandboxfs.Attachment, *sandboxfs.ReleaseRequest) (*sandboxfs.ReleaseResponse, error) {
	s.releases.Add(1)
	return &sandboxfs.ReleaseResponse{}, nil
}

func TestReadFileResults(t *testing.T) {
	for _, tc := range []struct {
		name        string
		data        string
		size        uint64
		limit       int64
		want        []byte
		wantErr     error
		readFailure bool
	}{
		{name: "empty", limit: 0, want: []byte{}},
		{name: "binary chunks", data: "a\x00b\xffc\xfe", size: 6, limit: 6, want: []byte("a\x00b\xffc\xfe")},
		{name: "oversize attribute", data: "abcdef", size: 6, limit: 5, wantErr: fs.ErrInvalid},
		{name: "shrank after lookup", data: "abcdef", size: 7, limit: 7, want: []byte("abcdef"), wantErr: fs.ErrInvalid},
		{name: "grew beyond limit", data: "abcdef", size: 4, limit: 5, want: []byte("abcd"), wantErr: errTooLarge},
		{name: "read failure", data: "abcdef", size: 6, limit: 6, want: []byte("abcd"), readFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &readFileService{data: []byte(tc.data), readFailure: tc.readFailure, entry: sandboxfs.Entry{
				Node: sandboxfs.NodeRef{ID: 2, Generation: 1},
				Attr: sandboxfs.Attr{Ino: 2, Mode: sandboxfs.ModeRegular | 0o600, Nlink: 1, Size: tc.size},
			}}
			cc, sc := net.Pipe()
			ctx, cancel := context.WithCancel(t.Context())
			attachment := sandboxfs.Attachment{ID: sandboxwire.NewID(), ServerInstanceID: sandboxwire.NewID(), Lease: ctx,
				Exports: []sandboxlink.ExportGrant{{ID: sandboxfs.WorldExport}}}
			finished := make(chan error, 1)
			go func() { finished <- sandboxfs.NewServer(svc).Serve(ctx, sc, attachment, 1) }()
			w := &world{c: sandboxfs.NewClient(cc), caps: sandboxfs.Capabilities{MaxReadBytes: 4},
				root: sandboxfs.NodeRef{ID: 1, Generation: 1}, handles: new(sandboxfs.HandleIDs), refs: map[sandboxfs.NodeRef]uint64{}}
			t.Cleanup(func() { w.c.Close(); cancel(); <-finished })

			data, attr, err := w.readFile(ctx, w.root, "file", tc.limit)
			if tc.readFailure {
				if !isErrno(err, sandboxfs.ErrnoIO) {
					t.Fatalf("read error = %v, want IO failure", err)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("read error = %v, want %v", err, tc.wantErr)
			}
			if !bytes.Equal(data, tc.want) || (data == nil) != (tc.want == nil) || attr != svc.entry.Attr {
				t.Fatalf("read = (%v, %+v), want (%v, %+v)", data, attr, tc.want, svc.entry.Attr)
			}
			wantRefs, wantReleases := uint64(1), int32(1)
			if tc.want == nil {
				wantReleases = 0
			}
			if err == nil && len(data) > 0 {
				data[0] = 'z'
				retained := bytes.Clone(data)
				next, _, err := w.readFile(ctx, w.root, "file", tc.limit)
				if err != nil || !bytes.Equal(next, tc.want) || !bytes.Equal(data, retained) {
					t.Fatalf("read results share storage: first %v, next %v, error %v", data, next, err)
				}
				wantRefs++
				wantReleases++
			}
			if got := svc.releases.Load(); got != wantReleases {
				t.Fatalf("released %d handles, want %d", got, wantReleases)
			}
			if len(w.refs) != 1 || w.refs[svc.entry.Node] != wantRefs {
				t.Fatalf("references = %v, want %v: %d", w.refs, svc.entry.Node, wantRefs)
			}
		})
	}
}
