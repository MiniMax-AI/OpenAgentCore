//go:build linux

package fileservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"golang.org/x/sys/unix"
)

func treeRequest(f *fixture) sandboxfs.OpenTreeRequest {
	return sandboxfs.OpenTreeRequest{Handle: f.ids.Next(), Node: f.root, MaxEntries: 1000, MaxDataBytes: 20 << 20, RequireReadOnlyFiles: true}
}

func treeBudget(t *testing.T, s *Service, count int, size uint64) {
	t.Helper()
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	if s.treeCount != count || s.treeBytes != size {
		t.Fatalf("tree budget = %d/%d, want %d/%d", s.treeCount, s.treeBytes, count, size)
	}
}

// Reading through the public client exercises chunk boundaries and the
// ordinary handle path instead of accessing the memfd directly.
type treeTestReader struct {
	f   *fixture
	h   sandboxfs.HandleID
	off uint64
}

func (r *treeTestReader) Read(p []byte) (int, error) {
	resp, err := r.f.c.Read(context.Background(), &sandboxfs.ReadRequest{Handle: r.h, Offset: r.off, Size: uint32(min(len(p), sandboxwire.MaxChunk))})
	if err != nil {
		return 0, err
	}
	n := copy(p, resp.Data)
	r.off += uint64(n)
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func decodeTree(t *testing.T, f *fixture, req sandboxfs.OpenTreeRequest, resp *sandboxfs.OpenTreeResponse, visit func(sandboxfs.TreeRecord, io.Reader)) {
	t.Helper()
	d, err := sandboxfs.NewTreeDecoder(&treeTestReader{f: f, h: req.Handle}, req, f.svc.caps, resp.Size)
	if err != nil {
		t.Fatal(err)
	}
	for {
		record, body, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		visit(record, body)
	}
}

func TestOpenTreeLargeFileAndResultOperations(t *testing.T) {
	f := newFixture(t)
	data := bytes.Repeat([]byte("large tree body\n"), 4096)
	file, err := os.OpenFile(filepath.Join(f.dir, "large"), os.O_CREATE|os.O_WRONLY, 0400)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.New()
	remaining := 20 << 20
	for remaining > 0 {
		chunk := data[:min(remaining, len(data))]
		if _, err := file.Write(chunk); err != nil {
			t.Fatal(err)
		}
		expected.Write(chunk)
		remaining -= len(chunk)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	req := treeRequest(f)
	resp, err := f.c.OpenTree(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := sandboxfs.TreeSizeBound(req, f.svc.caps)
	if err != nil {
		t.Fatal(err)
	}
	treeBudget(t, f.svc, 1, bound)
	got := sha256.New()
	count := 0
	decodeTree(t, f, req, resp, func(record sandboxfs.TreeRecord, body io.Reader) {
		count++
		if record.Attr.Mode&sandboxfs.ModeType == sandboxfs.ModeRegular {
			if string(record.Name) != "large" || record.Attr.Size != 20<<20 {
				t.Fatalf("record: %+v", record)
			}
			if _, err := io.Copy(got, body); err != nil {
				t.Fatal(err)
			}
		}
	})
	if count != 2 || !bytes.Equal(got.Sum(nil), expected.Sum(nil)) {
		t.Fatal("large tree body differed")
	}
	target := sandboxfs.Target{Kind: sandboxfs.TargetHandle, Handle: req.Handle}
	attr, err := f.c.GetAttr(context.Background(), &sandboxfs.GetAttrRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if attr.Attr.Size != resp.Size || attr.Attr.Mode != sandboxfs.ModeRegular|0400 {
		t.Fatalf("result attr: %+v", attr.Attr)
	}
	st := f.svc.atts[f.att.ID]
	h, err := st.handle(req.Handle)
	if err != nil {
		t.Fatal(err)
	}
	seals, err := unix.FcntlInt(h.f.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || seals != unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL {
		t.Fatalf("seals=%x: %v", seals, err)
	}
	_, err = f.c.Write(context.Background(), &sandboxfs.WriteRequest{Handle: req.Handle, Data: []byte("x")})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoReadOnlyFilesystem, none)
	_, err = f.c.SetAttr(context.Background(), &sandboxfs.SetAttrRequest{Target: target, Set: sandboxfs.AttrMode, Mode: 0600})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoReadOnlyFilesystem, none)
	_, err = f.c.Fsync(context.Background(), &sandboxfs.FsyncRequest{Handle: req.Handle})
	wantFailure(t, err, sandboxfs.CodeUnsupported, 0, none)
	_, err = f.c.ReadDir(context.Background(), &sandboxfs.ReadDirRequest{Handle: req.Handle, Limit: 4096})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoBadDescriptor, none)
	_, err = f.c.ReleaseDir(context.Background(), &sandboxfs.ReleaseDirRequest{Handle: req.Handle})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoBadDescriptor, none)
	if _, err = f.c.Flush(context.Background(), &sandboxfs.FlushRequest{Handle: req.Handle}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.c.Release(context.Background(), &sandboxfs.ReleaseRequest{Handle: req.Handle}); err != nil {
		t.Fatal(err)
	}
	treeBudget(t, f.svc, 0, 0)
	t.Logf("20 MiB body: sealed result backing=%d bytes; reserved bound=%d bytes; release budget=0", resp.Size, bound)
}

func TestOpenTreeDeepDirectoryKeepsLinearRecords(t *testing.T) {
	f := newFixture(t)
	dir, err := unix.Open(f.dir, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	name := string(bytes.Repeat([]byte("d"), 200))
	const depth = 200
	for range depth {
		if err := unix.Mkdirat(dir, name, 0700); err != nil {
			t.Fatal(err)
		}
		next, err := unix.Openat(dir, name, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		unix.Close(dir)
		dir = next
	}
	fd, err := unix.Openat(dir, "last", unix.O_CREAT|unix.O_WRONLY|unix.O_CLOEXEC, 0400)
	unix.Close(dir)
	if err != nil {
		t.Fatal(err)
	}
	unix.Close(fd)
	req := treeRequest(f)
	resp, err := f.c.OpenTree(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	index := uint32(0)
	decodeTree(t, f, req, resp, func(record sandboxfs.TreeRecord, body io.Reader) {
		if index > 0 && record.Parent != index-1 {
			t.Fatalf("parent %d at %d", record.Parent, index)
		}
		if _, err := io.Copy(io.Discard, body); err != nil {
			t.Fatal(err)
		}
		index++
	})
	if index != depth+2 || resp.Size > uint64(index)*400 {
		t.Fatalf("count=%d encoded=%d", index, resp.Size)
	}
}

func TestOpenTreeRejectsInvalidTreesAndReleasesBudget(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(string) error
		entries uint32
		data    uint64
		want    sandboxfs.Errno
	}{
		{"writable", func(p string) error { return os.WriteFile(p, nil, 0600) }, 10, 100, sandboxfs.ErrnoInvalidArgument},
		{"symlink", func(p string) error { return os.Symlink("/etc/passwd", p) }, 10, 100, sandboxfs.ErrnoInvalidArgument},
		{"fifo", func(p string) error { return unix.Mkfifo(p, 0400) }, 10, 100, sandboxfs.ErrnoInvalidArgument},
		{"data-limit", func(p string) error { return os.WriteFile(p, []byte("ab"), 0400) }, 10, 1, sandboxfs.ErrnoOverflow},
		{"entry-limit", func(p string) error { return os.WriteFile(p, nil, 0400) }, 1, 100, sandboxfs.ErrnoOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if err := tc.setup(filepath.Join(f.dir, "entry")); err != nil {
				t.Fatal(err)
			}
			req := treeRequest(f)
			req.MaxEntries = tc.entries
			req.MaxDataBytes = tc.data
			_, err := f.c.OpenTree(context.Background(), &req)
			wantFailure(t, err, sandboxfs.CodeErrno, tc.want, none)
			treeBudget(t, f.svc, 0, 0)
			if f.handles() != 0 {
				t.Fatal("failed acquisition left handle")
			}
		})
	}
}

func TestOpenTreeBudgetAcrossAttachments(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprint(large), func(t *testing.T) {
			f := newFixture(t)
			data := uint64(20 << 20)
			limit := 4
			if large {
				data = 32 << 20
				limit = 3
			}
			var atts []sandboxfs.Attachment
			var handles []sandboxfs.HandleID
			var total uint64
			for i := 0; i < limit+1; i++ {
				att := attachment(f.svc, sandboxlink.ExportGrant{ID: sandboxfs.WorldExport})
				root, err := f.svc.Attach(context.Background(), att, &sandboxfs.AttachRequest{Export: sandboxfs.WorldExport})
				if err != nil {
					t.Fatal(err)
				}
				req := treeRequest(f)
				req.Node = root.Root.Node
				req.MaxDataBytes = data
				req.MaxEntries = f.svc.caps.MaxTreeEntries
				_, err = f.svc.OpenTree(context.Background(), att, &req)
				if i == limit {
					wantFailure(t, err, sandboxfs.CodeResourceExhausted, 0, none)
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				bound, _ := sandboxfs.TreeSizeBound(req, f.svc.caps)
				total += bound
				treeBudget(t, f.svc, i+1, total)
				duplicate := req
				duplicate.Handle = f.ids.Next()
				_, err = f.svc.OpenTree(context.Background(), att, &duplicate)
				wantFailure(t, err, sandboxfs.CodeResourceExhausted, 0, none)
				atts = append(atts, att)
				handles = append(handles, req.Handle)
			}
			for i, att := range atts {
				if _, err := f.svc.Release(context.Background(), att, &sandboxfs.ReleaseRequest{Handle: handles[i]}); err != nil {
					t.Fatal(err)
				}
			}
			treeBudget(t, f.svc, 0, 0)
		})
	}
}

func TestLostOpenTreeReplyIsReleased(t *testing.T) {
	f := newFixture(t)
	cc, sc := net.Pipe()
	go f.srv.Serve(context.Background(), dropConn{sc, sandboxwire.ResponseType(uint16(sandboxfs.OpOpenTree))}, f.att, binds.Add(1))
	c := sandboxfs.NewClient(cc)
	defer c.Close()
	req := treeRequest(f)
	_, err := c.OpenTree(context.Background(), &req)
	fail := wantFailure(t, err, sandboxfs.CodeUnknown, 0, possible)
	if !errors.Is(fail, sandboxfs.ErrTransport) {
		t.Fatal("expected lost response")
	}
	bound, _ := sandboxfs.TreeSizeBound(req, f.svc.caps)
	treeBudget(t, f.svc, 1, bound)
	resumed, _, _ := f.connect(f.srv, f.att)
	if _, err := resumed.Release(context.Background(), &sandboxfs.ReleaseRequest{Handle: req.Handle}); err != nil {
		t.Fatal(err)
	}
	treeBudget(t, f.svc, 0, 0)
}

// gatedTreeContext pauses the first admitted check without modifying product
// code. Closing the attachment must signal done and join this acquisition.
type gatedTreeContext struct {
	context.Context
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (c *gatedTreeContext) Err() error {
	c.once.Do(func() { close(c.entered); <-c.resume })
	return c.Context.Err()
}

func TestOpenTreeCloseJoinsPendingAcquisition(t *testing.T) {
	for _, serviceClose := range []bool{false, true} {
		t.Run(fmt.Sprint(serviceClose), func(t *testing.T) {
			f := newFixture(t)
			st := f.svc.atts[f.att.ID]
			ctx := &gatedTreeContext{Context: context.Background(), entered: make(chan struct{}), resume: make(chan struct{})}
			resume := sync.OnceFunc(func() { close(ctx.resume) })
			defer resume()
			req := treeRequest(f)
			result := make(chan error, 1)
			go func() { _, err := f.svc.OpenTree(ctx, f.att, &req); result <- err }()
			<-ctx.entered
			bound, _ := sandboxfs.TreeSizeBound(req, f.svc.caps)
			treeBudget(t, f.svc, 1, bound)
			closed := make(chan error, 1)
			go func() {
				if serviceClose {
					closed <- f.svc.Close()
				} else {
					_, err := f.svc.Detach(context.Background(), f.att, &sandboxfs.DetachRequest{})
					closed <- err
				}
			}()
			<-st.done
			select {
			case err := <-closed:
				t.Fatalf("close returned before pending acquisition joined: %v", err)
			default:
			}
			treeBudget(t, f.svc, 1, bound)
			if err := use(f.svc.proc, errStaleAttachment, func(int) error { return nil }); err != nil {
				t.Fatal("proc closed under acquisition")
			}
			resume()
			wantFailure(t, <-result, sandboxfs.CodeStaleAttachment, 0, none)
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("close did not join")
			}
			treeBudget(t, f.svc, 0, 0)
		})
	}
}

func TestOpenTreeCloseWaitsForDescriptorRead(t *testing.T) {
	for _, operation := range []string{"release", "detach", "service"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			req := treeRequest(f)
			if _, err := f.svc.OpenTree(context.Background(), f.att, &req); err != nil {
				t.Fatal(err)
			}
			st := f.svc.atts[f.att.ID]
			h, err := st.handle(req.Handle)
			if err != nil {
				t.Fatal(err)
			}
			entered, resume := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(resume) })
			defer unblock()
			reading := make(chan error, 1)
			go func() {
				reading <- h.use(func(fd int) error {
					close(entered)
					<-resume
					buf := make([]byte, 12)
					_, err := unix.Pread(fd, buf, 0)
					return err
				})
			}()
			<-entered
			closed := make(chan error, 1)
			go func() {
				switch operation {
				case "release":
					_, err := f.svc.Release(context.Background(), f.att, &sandboxfs.ReleaseRequest{Handle: req.Handle})
					closed <- err
				case "detach":
					_, err := f.svc.Detach(context.Background(), f.att, &sandboxfs.DetachRequest{})
					closed <- err
				default:
					closed <- f.svc.Close()
				}
			}()
			waitTreeClose(t, h)
			bound, _ := sandboxfs.TreeSizeBound(req, f.svc.caps)
			treeBudget(t, f.svc, 1, bound)
			select {
			case err := <-closed:
				t.Fatalf("close returned during descriptor use: %v", err)
			default:
			}
			unblock()
			if err := <-reading; err != nil {
				t.Fatal(err)
			}
			if err := <-closed; err != nil {
				t.Fatal(err)
			}
			treeBudget(t, f.svc, 0, 0)
		})
	}
}

func TestOpenTreeCancellationLeavesNoResources(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := treeRequest(f)
	_, err := f.svc.OpenTree(ctx, f.att, &req)
	wantFailure(t, err, sandboxfs.CodeCancelled, 0, none)
	treeBudget(t, f.svc, 0, 0)
	if f.handles() != 0 {
		t.Fatal("cancelled acquisition left handle")
	}
}

func TestOpenTreeHeldRootSurvivesRenameAndReplacement(t *testing.T) {
	f := newFixture(t)
	old := filepath.Join(f.dir, "root")
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "original"), []byte("original body"), 0400); err != nil {
		t.Fatal(err)
	}
	root := f.lookup(f.root, "root").Node
	if err := os.Rename(old, old+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "replacement"), []byte("replacement body"), 0400); err != nil {
		t.Fatal(err)
	}
	req := treeRequest(f)
	req.Node = root
	resp, err := f.c.OpenTree(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	decodeTree(t, f, req, resp, func(record sandboxfs.TreeRecord, body io.Reader) {
		if record.Attr.Mode&sandboxfs.ModeType == sandboxfs.ModeRegular {
			data, err := io.ReadAll(body)
			if err != nil {
				t.Fatal(err)
			}
			if string(record.Name) != "original" || string(data) != "original body" {
				t.Fatalf("read replacement: %q %q", record.Name, data)
			}
			files++
		}
	})
	if files != 1 {
		t.Fatalf("files=%d", files)
	}
}

// Separate capture and encoding deterministically schedules filesystem
// mutations between them, without timing assumptions or production hooks.
func captureTreeForTest(t *testing.T, f *fixture, req *sandboxfs.OpenTreeRequest) *treeCapture {
	t.Helper()
	capture := &treeCapture{st: f.svc.atts[f.att.ID], ctx: context.Background(), req: req}
	t.Cleanup(capture.close)
	fd, err := unix.Open(f.dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := capture.retain(fd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := capture.walk(root, 0); err != nil {
		t.Fatal(err)
	}
	return capture
}

func TestOpenTreeCapturedFileIdentityAndSizeChanges(t *testing.T) {
	for _, operation := range []string{"rename", "unlink", "replacement", "shrink", "grow"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			path := filepath.Join(f.dir, "entry")
			if err := os.WriteFile(path, []byte("original"), 0400); err != nil {
				t.Fatal(err)
			}
			req := treeRequest(f)
			capture := captureTreeForTest(t, f, &req)
			switch operation {
			case "rename":
				if err := os.Rename(path, path+"-moved"); err != nil {
					t.Fatal(err)
				}
			case "unlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := os.Rename(path, path+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("replaced"), 0400); err != nil {
					t.Fatal(err)
				}
			case "shrink", "grow":
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				data := []byte("x")
				if operation == "grow" {
					data = []byte("larger than original")
				}
				if err := os.WriteFile(path, data, 0400); err != nil {
					t.Fatal(err)
				}
			}
			var encoded bytes.Buffer
			err := capture.encode(&encoded)
			if operation == "shrink" || operation == "grow" {
				wantFailure(t, treeFailure(err), sandboxfs.CodeErrno, sandboxfs.ErrnoInvalidArgument, none)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoder, err := sandboxfs.NewTreeDecoder(bytes.NewReader(encoded.Bytes()), req, f.svc.caps, uint64(encoded.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := decoder.Next(); err != nil {
				t.Fatal(err)
			}
			record, body, err := decoder.Next()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(body)
			if err != nil {
				t.Fatal(err)
			}
			if string(record.Name) != "entry" || string(data) != "original" {
				t.Fatalf("changed object: %q %q", record.Name, data)
			}
			if _, _, err := decoder.Next(); err != io.EOF {
				t.Fatalf("end=%v", err)
			}
		})
	}
}

func TestOpenTreeEnforcesProcessPermissions(t *testing.T) {
	// Root's native DAC override is intentional; check identical permissions
	// against a normal OS read, including that override if this test runs as root.
	f := newFixture(t)
	path := filepath.Join(f.dir, "entry")
	if err := os.WriteFile(path, []byte("private"), 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0600)
	direct, readErr := os.ReadFile(path)
	req := treeRequest(f)
	resp, err := f.c.OpenTree(context.Background(), &req)
	if readErr != nil {
		if !errors.Is(readErr, os.ErrPermission) {
			t.Fatal(readErr)
		}
		wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoPermissionDenied, none)
		treeBudget(t, f.svc, 0, 0)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	decodeTree(t, f, req, resp, func(record sandboxfs.TreeRecord, body io.Reader) {
		data, err := io.ReadAll(body)
		if err != nil {
			t.Fatal(err)
		}
		if record.Attr.Mode&sandboxfs.ModeType == sandboxfs.ModeRegular && !bytes.Equal(data, direct) {
			t.Fatal("OS read differs")
		}
	})
}

// A pending close writer prevents new descriptor readers from entering.
func waitTreeClose(t *testing.T, h *handle) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.useMu.TryRLock() {
		h.useMu.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("close not entered")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestOpenTreeRepeatedReleaseJoinsClosingHandle(t *testing.T) {
	for _, next := range []string{"release", "detach"} {
		t.Run(next, func(t *testing.T) {
			f := newFixture(t)
			q := treeRequest(f)
			if _, err := f.svc.OpenTree(t.Context(), f.att, &q); err != nil {
				t.Fatal(err)
			}
			st := f.svc.atts[f.att.ID]
			h, err := st.handle(q.Handle)
			if err != nil {
				t.Fatal(err)
			}
			entered, resume := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(resume) })
			defer unblock()
			reading := make(chan error, 1)
			go func() {
				reading <- h.use(func(fd int) error {
					close(entered)
					<-resume
					var b [1]byte
					_, err := unix.Pread(fd, b[:], 0)
					return err
				})
			}()
			<-entered
			first := make(chan error, 1)
			go func() {
				_, err := f.svc.Release(t.Context(), f.att, &sandboxfs.ReleaseRequest{Handle: q.Handle})
				first <- err
			}()
			waitTreeClose(t, h)
			second := make(chan error, 1)
			secondStarted := make(chan struct{})
			go func() {
				close(secondStarted)
				if next == "release" {
					_, err := f.svc.Release(t.Context(), f.att, &sandboxfs.ReleaseRequest{Handle: q.Handle})
					second <- err
				} else {
					_, err := f.svc.Detach(t.Context(), f.att, &sandboxfs.DetachRequest{})
					second <- err
				}
			}()
			<-secondStarted
			select {
			case err := <-second:
				t.Fatalf("second %s returned before backing released: %v", next, err)
			case <-time.After(20 * time.Millisecond):
			}
			bound, _ := sandboxfs.TreeSizeBound(q, f.svc.caps)
			treeBudget(t, f.svc, 1, bound)
			unblock()
			if err := <-reading; err != nil {
				t.Fatal(err)
			}
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			if err := <-second; err != nil {
				if next != "release" {
					t.Fatal(err)
				}
				wantFailure(t, err, sandboxfs.CodeStaleHandle, 0, none)
			}
			treeBudget(t, f.svc, 0, 0)
			if next == "release" {
				_, err := f.svc.Release(t.Context(), f.att, &sandboxfs.ReleaseRequest{Handle: q.Handle})
				wantFailure(t, err, sandboxfs.CodeStaleHandle, 0, none)
			}
		})
	}
}
