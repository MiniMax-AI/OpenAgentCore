//go:build linux

package fileservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

type fixture struct {
	t    *testing.T
	dir  string
	svc  *Service
	att  sandboxfs.Attachment
	c    *sandboxfs.Client
	root sandboxfs.NodeRef
}

// newFixture serves a temporary directory as the export world and attaches
// to it.
func newFixture(t *testing.T) *fixture {
	return attachRoot(t, t.TempDir())
}

// attachRoot serves dir as the export world and attaches to it.
func attachRoot(t *testing.T, dir string) *fixture {
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	f := &fixture{t: t, dir: dir, svc: svc, att: attachment(svc, sandboxlink.ExportGrant{ID: "world"})}
	f.c, _, _ = f.connect(f.svc, f.att)
	resp, err := f.c.Attach(context.Background(), &sandboxfs.AttachRequest{Export: "world"})
	if err != nil {
		t.Fatal(err)
	}
	f.root = resp.Root.Node
	return f
}

// connect opens a stream to svc for attachment a. It returns the client, the
// server end of the stream and the channel Serve's result arrives on.
func (f *fixture) connect(svc *Service, a sandboxfs.Attachment) (*sandboxfs.Client, net.Conn, chan error) {
	cc, sc := net.Pipe()
	served := make(chan error, 1)
	go func() { served <- sandboxfs.Serve(context.Background(), sc, svc, a) }()
	c := sandboxfs.NewClient(cc)
	f.t.Cleanup(func() { c.Close() })
	return c, sc, served
}

// attachment returns a fresh attachment to svc with grants.
func attachment(svc *Service, grants ...sandboxlink.ExportGrant) sandboxfs.Attachment {
	return sandboxfs.Attachment{ID: sandboxwire.NewID(), ServerInstanceID: svc.InstanceID(), Lease: context.Background(), Exports: grants}
}

func (f *fixture) lookup(parent sandboxfs.NodeRef, name string) sandboxfs.Entry {
	f.t.Helper()
	r, err := f.c.Lookup(context.Background(), &sandboxfs.LookupRequest{Parent: parent, Name: []byte(name)})
	if err != nil {
		f.t.Fatalf("lookup %q: %v", name, err)
	}
	return r.Entry
}

func (f *fixture) create(name string, access sandboxfs.AccessMode, flags sandboxfs.OpenFlags) (sandboxfs.Entry, sandboxfs.HandleID) {
	f.t.Helper()
	r, err := f.c.Create(context.Background(), &sandboxfs.CreateRequest{Parent: f.root, Name: []byte(name), Mode: 0o640, Access: access, Flags: flags, Exclusive: true})
	if err != nil {
		f.t.Fatalf("create %q: %v", name, err)
	}
	return r.Entry, r.Handle
}

func (f *fixture) write(h sandboxfs.HandleID, off uint64, data string) {
	f.t.Helper()
	r, err := f.c.Write(context.Background(), &sandboxfs.WriteRequest{Handle: h, Offset: off, Data: []byte(data)})
	if err != nil || r.Written != uint32(len(data)) || r.Failure != nil {
		f.t.Fatalf("write: %+v, %v", r, err)
	}
}

func (f *fixture) read(h sandboxfs.HandleID) string {
	f.t.Helper()
	r, err := f.c.Read(context.Background(), &sandboxfs.ReadRequest{Handle: h, Size: 4096})
	if err != nil {
		f.t.Fatalf("read: %v", err)
	}
	return string(r.Data)
}

// wantFailure checks err is a failure with code, errno and effect.
func wantFailure(t *testing.T, err error, code sandboxfs.ErrorCode, errno sandboxfs.Errno, effect sandboxwire.Effect) *sandboxfs.Failure {
	t.Helper()
	var f *sandboxfs.Failure
	if !errors.As(err, &f) || f.Code != code || f.Errno != errno || f.Effect != effect {
		t.Fatalf("got %v, want %s %s effect %d", err, code, errno, effect)
	}
	return f
}

func TestCreateWriteRead(t *testing.T) {
	f := newFixture(t)
	d, err := f.c.Describe(context.Background(), &sandboxfs.DescribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Identity != (sandboxfs.Identity{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}) || !slices.Equal(d.Exports, []sandboxlink.ExportID{"world"}) || d.Capabilities.POSIXLocks || !d.Capabilities.Flock {
		t.Fatalf("describe %+v", d)
	}

	e, h := f.create("notes", sandboxfs.AccessReadWrite, 0)
	f.write(h, 0, "hello world")
	f.write(h, 6, "there")
	if got := f.read(h); got != "hello there" {
		t.Fatalf("read %q", got)
	}
	info, err := os.Lstat(filepath.Join(f.dir, "notes"))
	if err != nil || info.Mode().Perm() != 0o640 || info.Size() != 11 {
		t.Fatalf("on disk %v, %v", info.Mode(), err)
	}
	a, err := f.c.GetAttr(context.Background(), &sandboxfs.GetAttrRequest{Target: sandboxfs.Target{Kind: sandboxfs.TargetNode, Node: e.Node}})
	if err != nil || a.Attr.Size != 11 || a.Attr.Mode != sandboxfs.ModeRegular|0o640 {
		t.Fatalf("getattr %+v, %v", a, err)
	}
	if _, err := f.c.Release(context.Background(), &sandboxfs.ReleaseRequest{Handle: h}); err != nil {
		t.Fatal(err)
	}
	_, err = f.c.Read(context.Background(), &sandboxfs.ReadRequest{Handle: h, Size: 1})
	wantFailure(t, err, sandboxfs.CodeStaleHandle, 0, sandboxwire.EffectNone)
}

func TestExclusiveCreateConflict(t *testing.T) {
	f := newFixture(t)
	f.create("x", sandboxfs.AccessWrite, 0)
	_, err := f.c.Create(context.Background(), &sandboxfs.CreateRequest{Parent: f.root, Name: []byte("x"), Mode: 0o600, Access: sandboxfs.AccessWrite, Exclusive: true})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoExists, sandboxwire.EffectNone)
	if _, err := f.c.Create(context.Background(), &sandboxfs.CreateRequest{Parent: f.root, Name: []byte("x"), Mode: 0o600, Access: sandboxfs.AccessWrite}); err != nil {
		t.Fatalf("non-exclusive create of an existing file: %v", err)
	}
}

func TestAtomicAppendFromTwoHandles(t *testing.T) {
	f := newFixture(t)
	_, h1 := f.create("log", sandboxfs.AccessWrite, sandboxfs.OpenAppend)
	open, err := f.c.Open(context.Background(), &sandboxfs.OpenRequest{Node: f.lookup(f.root, "log").Node, Access: sandboxfs.AccessWrite, Flags: sandboxfs.OpenAppend})
	if err != nil {
		t.Fatal(err)
	}
	const records = 200
	var wg sync.WaitGroup
	for i, h := range []sandboxfs.HandleID{h1, open.Handle} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range records {
				// Every write names offset zero; append ignores it.
				rec := fmt.Sprintf("%d:%03d:%s\n", i, j, strings.Repeat("x", 64))
				if r, err := f.c.Write(context.Background(), &sandboxfs.WriteRequest{Handle: h, Data: []byte(rec)}); err != nil || int(r.Written) != len(rec) {
					t.Errorf("append: %+v, %v", r, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(f.dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2*records {
		t.Fatalf("%d records, want %d", len(lines), 2*records)
	}
	for _, l := range lines {
		if len(l) != 6+64 || !strings.HasSuffix(l, strings.Repeat("x", 64)) {
			t.Fatalf("torn record %q", l)
		}
	}
}

func TestReadOpenHandleAfterUnlink(t *testing.T) {
	f := newFixture(t)
	_, h := f.create("gone", sandboxfs.AccessReadWrite, 0)
	f.write(h, 0, "still here")
	if _, err := f.c.Unlink(context.Background(), &sandboxfs.UnlinkRequest{Parent: f.root, Name: []byte("gone")}); err != nil {
		t.Fatal(err)
	}
	if got := f.read(h); got != "still here" {
		t.Fatalf("read %q", got)
	}
	a, err := f.c.GetAttr(context.Background(), &sandboxfs.GetAttrRequest{Target: sandboxfs.Target{Kind: sandboxfs.TargetHandle, Handle: h}})
	if err != nil || a.Attr.Nlink != 0 {
		t.Fatalf("getattr %+v, %v", a, err)
	}
}

func TestRenameModes(t *testing.T) {
	f := newFixture(t)
	d, _ := f.c.Describe(context.Background(), &sandboxfs.DescribeRequest{})
	if !d.Capabilities.RenameNoReplace || !d.Capabilities.RenameExchange {
		t.Skip("the kernel lacks renameat2 modes")
	}
	for name, content := range map[string]string{"a": "A", "b": "B"} {
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rename := func(mode sandboxfs.RenameMode) error {
		_, err := f.c.Rename(context.Background(), &sandboxfs.RenameRequest{Parent: f.root, Name: []byte("a"), NewParent: f.root, NewName: []byte("b"), Mode: mode})
		return err
	}
	content := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(f.dir, name))
		return string(b)
	}

	wantFailure(t, rename(sandboxfs.RenameNoReplace), sandboxfs.CodeErrno, sandboxfs.ErrnoExists, sandboxwire.EffectNone)
	if err := rename(sandboxfs.RenameExchange); err != nil || content("a") != "B" || content("b") != "A" {
		t.Fatalf("exchange: %v, a=%q b=%q", err, content("a"), content("b"))
	}
	if err := rename(sandboxfs.RenameReplace); err != nil || content("b") != "B" {
		t.Fatalf("replace: %v, b=%q", err, content("b"))
	}
	if _, err := os.Lstat(filepath.Join(f.dir, "a")); !os.IsNotExist(err) {
		t.Fatalf("a after replace: %v", err)
	}
	wantFailure(t, rename(sandboxfs.RenameNoReplace), sandboxfs.CodeErrno, sandboxfs.ErrnoNotFound, sandboxwire.EffectNone)
}

func TestReadDirPagesWithCookies(t *testing.T) {
	f := newFixture(t)
	var want []string
	for i := range 40 {
		name := fmt.Sprintf("file-%02d", i)
		want = append(want, name)
		if err := os.WriteFile(filepath.Join(f.dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := f.c.OpenDir(context.Background(), &sandboxfs.OpenDirRequest{Node: f.root})
	if err != nil {
		t.Fatal(err)
	}
	readAll := func(cookie uint64) (names []string, cookies []uint64) {
		for {
			r, err := f.c.ReadDir(context.Background(), &sandboxfs.ReadDirRequest{Handle: dir.Handle, Cookie: cookie, Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			if !r.End && len(r.Entries) == 0 {
				t.Fatal("empty page before the end")
			}
			for _, e := range r.Entries {
				names = append(names, string(e.Name))
				cookies = append(cookies, e.Cookie)
				cookie = e.Cookie
			}
			if r.End {
				return names, cookies
			}
		}
	}
	names, cookies := readAll(0)
	if got := slices.Sorted(slices.Values(names)); !slices.Equal(got, want) {
		t.Fatalf("names %v", got)
	}
	// Resuming at an earlier cookie continues after that entry.
	again, _ := readAll(cookies[9])
	if !slices.Equal(again, names[10:]) {
		t.Fatalf("resumed at cookie 10: %v, want %v", again, names[10:])
	}

	r, err := f.c.ReadDir(context.Background(), &sandboxfs.ReadDirRequest{Handle: dir.Handle, Limit: 1024, WithAttrs: true})
	if err != nil || len(r.Entries) == 0 || r.Entries[0].Entry == nil || r.Entries[0].Entry.Attr.Mode&sandboxfs.ModeType != sandboxfs.ModeRegular {
		t.Fatalf("readdir with attributes: %+v, %v", r, err)
	}
	for _, e := range r.Entries {
		if _, err := f.c.Forget(context.Background(), &sandboxfs.ForgetRequest{Entries: []sandboxfs.ForgetEntry{{Node: e.Entry.Node, Count: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWalkStopsAtSymlink(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.dir, "d", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub", filepath.Join(f.dir, "d", "link")); err != nil {
		t.Fatal(err)
	}
	walk := func(names ...string) *sandboxfs.WalkResponse {
		r := &sandboxfs.WalkRequest{Parent: f.root}
		for _, n := range names {
			r.Names = append(r.Names, []byte(n))
		}
		resp, err := f.c.Walk(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	r := walk("d", "link", "x")
	if len(r.Entries) != 2 || r.Failure != nil || r.Entries[1].Attr.Mode&sandboxfs.ModeType != sandboxfs.ModeSymlink {
		t.Fatalf("walk through symlink: %+v", r)
	}
	r = walk("d", "missing", "x")
	if len(r.Entries) != 1 || r.Failure == nil || r.Failure.Errno != sandboxfs.ErrnoNotFound {
		t.Fatalf("walk to a missing name: %+v", r)
	}
}

func TestRootEscapeIsRefused(t *testing.T) {
	f := newFixture(t)
	_, err := f.c.Lookup(context.Background(), &sandboxfs.LookupRequest{Parent: f.root, Name: []byte("..")})
	wantFailure(t, err, sandboxfs.CodeInvalidArgument, 0, sandboxwire.EffectNone)

	// A client that skips validation gets the same answer from the server.
	cc, sc := net.Pipe()
	go sandboxfs.Serve(context.Background(), sc, f.svc, f.att)
	defer cc.Close()
	var e sandboxwire.Encoder
	e.U64(f.root.ID)
	e.U64(f.root.Generation)
	e.Bytes([]byte(".."))
	if err := sandboxwire.WriteFrame(cc, sandboxwire.Frame{Type: uint16(sandboxfs.OpLookup), RequestID: 1, Payload: e.Payload()}); err != nil {
		t.Fatal(err)
	}
	resp, err := sandboxwire.ReadFrame(cc, sandboxwire.MaxPayload)
	if err != nil || resp.Type != sandboxwire.ResponseType(uint16(sandboxfs.OpLookup)) || !bytes.HasPrefix(resp.Payload, []byte{0, 2, 0, byte(sandboxfs.CodeInvalidArgument)}) {
		t.Fatalf("raw lookup of \"..\": %x, %v", resp.Payload, err)
	}

	// A symlink to "/" is a leaf: nothing resolves through it.
	if err := os.Symlink("/", filepath.Join(f.dir, "escape")); err != nil {
		t.Fatal(err)
	}
	link := f.lookup(f.root, "escape").Node
	_, err = f.c.Lookup(context.Background(), &sandboxfs.LookupRequest{Parent: link, Name: []byte("etc")})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoNotDirectory, sandboxwire.EffectNone)
	_, err = f.c.Open(context.Background(), &sandboxfs.OpenRequest{Node: link, Access: sandboxfs.AccessRead})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoSymlinkLoop, sandboxwire.EffectNone)
	_, err = f.c.OpenDir(context.Background(), &sandboxfs.OpenDirRequest{Node: link})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoNotDirectory, sandboxwire.EffectNone)
	_, err = f.c.Mkdir(context.Background(), &sandboxfs.MkdirRequest{Parent: link, Name: []byte("x"), Mode: 0o755})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoNotDirectory, sandboxwire.EffectNone)
}

func TestFlockInteroperatesWithNativeFlock(t *testing.T) {
	f := newFixture(t)
	_, h := f.create("lock", sandboxfs.AccessReadWrite, 0)
	native, err := os.Open(filepath.Join(f.dir, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	whole := sandboxfs.Lock{Mode: sandboxfs.LockWrite, End: math.MaxInt64}
	setLock := func(ctx context.Context, mode sandboxfs.LockMode, wait bool) error {
		l := whole
		l.Mode = mode
		_, err := f.c.SetLock(ctx, &sandboxfs.SetLockRequest{Handle: h, Kind: sandboxfs.LockFlock, Owner: 1, Lock: l, Wait: wait})
		return err
	}
	nativeTry := func() error { return syscall.Flock(int(native.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }

	if err := setLock(context.Background(), sandboxfs.LockWrite, false); err != nil {
		t.Fatal(err)
	}
	if err := nativeTry(); err != syscall.EWOULDBLOCK {
		t.Fatalf("native flock while the service holds it: %v", err)
	}
	if err := setLock(context.Background(), sandboxfs.LockUnlock, false); err != nil {
		t.Fatal(err)
	}
	if err := nativeTry(); err != nil {
		t.Fatalf("native flock after unlock: %v", err)
	}
	wantFailure(t, setLock(context.Background(), sandboxfs.LockWrite, false), sandboxfs.CodeErrno, sandboxfs.ErrnoAgain, sandboxwire.EffectNone)

	// A waiting lock the client abandons is cancelled on the server.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	wantFailure(t, setLock(ctx, sandboxfs.LockWrite, true), sandboxfs.CodeDeadlineExceeded, 0, sandboxwire.EffectPossible)
	syscall.Flock(int(native.Fd()), syscall.LOCK_UN)
	time.Sleep(200 * time.Millisecond)
	if err := nativeTry(); err != nil {
		t.Fatalf("native flock after the cancelled wait: %v", err)
	}

	_, err = f.c.SetLock(context.Background(), &sandboxfs.SetLockRequest{Handle: h, Kind: sandboxfs.LockPOSIX, Lock: whole})
	wantFailure(t, err, sandboxfs.CodeUnsupported, 0, sandboxwire.EffectNone)
}

func TestIncarnationChange(t *testing.T) {
	f := newFixture(t)
	_, h := f.create("f", sandboxfs.AccessRead, 0)
	next, err := New(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()

	// A stream bound to the old incarnation.
	old, _, _ := f.connect(next, f.att)
	_, err = old.Lookup(context.Background(), &sandboxfs.LookupRequest{Parent: f.root, Name: []byte("f")})
	wantFailure(t, err, sandboxfs.CodeInstanceChanged, 0, sandboxwire.EffectNone)

	// The same attachment rebound to the new incarnation holds nothing.
	a := f.att
	a.ServerInstanceID = next.InstanceID()
	c, _, _ := f.connect(next, a)
	_, err = c.Lookup(context.Background(), &sandboxfs.LookupRequest{Parent: f.root, Name: []byte("f")})
	wantFailure(t, err, sandboxfs.CodeStaleAttachment, 0, sandboxwire.EffectNone)
	if _, err := c.Attach(context.Background(), &sandboxfs.AttachRequest{Export: "world"}); err != nil {
		t.Fatal(err)
	}
	_, err = c.Lookup(context.Background(), &sandboxfs.LookupRequest{Parent: f.root, Name: []byte("f")})
	wantFailure(t, err, sandboxfs.CodeStaleNode, 0, sandboxwire.EffectNone)
	_, err = c.Read(context.Background(), &sandboxfs.ReadRequest{Handle: h, Size: 1})
	wantFailure(t, err, sandboxfs.CodeStaleHandle, 0, sandboxwire.EffectNone)
}

func TestAttachFollowsExportGrants(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	c, _, _ := f.connect(f.svc, attachment(f.svc, sandboxlink.ExportGrant{ID: "logs"}))
	_, err := c.Attach(ctx, &sandboxfs.AttachRequest{Export: "world"})
	wantFailure(t, err, sandboxfs.CodeUnauthorized, 0, sandboxwire.EffectNone)

	c, _, _ = f.connect(f.svc, attachment(f.svc, sandboxlink.ExportGrant{ID: "world", ReadOnly: true}))
	_, err = c.Attach(ctx, &sandboxfs.AttachRequest{Export: "world"})
	wantFailure(t, err, sandboxfs.CodeUnauthorized, 0, sandboxwire.EffectNone)
	r, err := c.Attach(ctx, &sandboxfs.AttachRequest{Export: "world", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Create(ctx, &sandboxfs.CreateRequest{Parent: r.Root.Node, Name: []byte("f"), Mode: 0o644, Access: sandboxfs.AccessWrite})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoReadOnlyFilesystem, sandboxwire.EffectNone)
}

func TestDescribeListsGrantedExports(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.connect(f.svc, attachment(f.svc, sandboxlink.ExportGrant{ID: "logs"}))
	d, err := c.Describe(context.Background(), &sandboxfs.DescribeRequest{})
	if err != nil || len(d.Exports) != 0 {
		t.Fatalf("describe without a world grant: %+v, %v", d, err)
	}
}

// The service never resolves through a proc magic link: oac-sandbox-io
// serves "/", where /proc/<pid>/cwd links to another directory.
func TestProcMagicLinkIsOpaque(t *testing.T) {
	f := attachRoot(t, "/")
	ctx := context.Background()
	w, err := f.c.Walk(ctx, &sandboxfs.WalkRequest{Parent: f.root, Names: [][]byte{[]byte("proc"), []byte(strconv.Itoa(os.Getpid())), []byte("cwd")}})
	if err != nil || len(w.Entries) != 3 || w.Entries[2].Attr.Mode&sandboxfs.ModeType != sandboxfs.ModeSymlink {
		t.Fatalf("walk to /proc/<pid>/cwd: %+v, %v", w, err)
	}
	cwd := w.Entries[2].Node
	_, err = f.c.Lookup(ctx, &sandboxfs.LookupRequest{Parent: cwd, Name: []byte("x")})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoNotDirectory, sandboxwire.EffectNone)
	_, err = f.c.OpenDir(ctx, &sandboxfs.OpenDirRequest{Node: cwd})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoNotDirectory, sandboxwire.EffectNone)
	_, err = f.c.Open(ctx, &sandboxfs.OpenRequest{Node: cwd, Access: sandboxfs.AccessRead})
	wantFailure(t, err, sandboxfs.CodeErrno, sandboxfs.ErrnoSymlinkLoop, sandboxwire.EffectNone)
}

// A bind mount and its source are one inode on two mounts, and stay two
// nodes. Bind mounts need a mount namespace, so the test reruns itself under
// unshare when unprivileged user namespaces are available.
func TestBindAliasesStayDistinct(t *testing.T) {
	if os.Getenv("OAC_TEST_IN_USERNS") == "" {
		unshare, err := exec.LookPath("unshare")
		if err != nil || exec.Command(unshare, "-Urm", "true").Run() != nil {
			t.Skip("unprivileged user and mount namespaces are unavailable")
		}
		cmd := exec.Command(unshare, "-Urm", os.Args[0], "-test.run=^TestBindAliasesStayDistinct$", "-test.v")
		cmd.Env = append(os.Environ(), "OAC_TEST_IN_USERNS=1")
		if out, err := cmd.CombinedOutput(); err != nil || !bytes.Contains(out, []byte("--- PASS")) {
			t.Fatalf("in a user namespace: %v\n%s", err, out)
		}
		return
	}
	dir := t.TempDir()
	src, alias := filepath.Join(dir, "src"), filepath.Join(dir, "alias")
	for _, d := range []string{src, alias} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mount(src, alias, "", syscall.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	defer syscall.Unmount(alias, syscall.MNT_DETACH)
	f := attachRoot(t, dir)
	a, b := f.lookup(f.root, "src"), f.lookup(f.root, "alias")
	if a.Node == b.Node || a.Attr.Ino != b.Attr.Ino {
		t.Fatalf("source %+v and bind alias %+v", a, b)
	}
}

// The fdinfo fallback reports the mount ID statx does.
func TestMountIDMechanismsAgree(t *testing.T) {
	f := newFixture(t)
	fd := int(f.svc.root.Fd())
	viaStatx, err := f.svc.mountID(fd)
	if err != nil {
		t.Fatal(err)
	}
	if viaFdinfo, err := f.svc.fdinfoMountID(fd); err != nil || viaFdinfo != viaStatx {
		t.Fatalf("fdinfo mount ID %d, %v; statx %d", viaFdinfo, err, viaStatx)
	}
}

func TestFailureAfterAChangeIsEffectPossible(t *testing.T) {
	wantFailure(t, failure(errStaleAttachment(), possible), sandboxfs.CodeStaleAttachment, 0, possible)
}

// wroteConn reports each completed write.
type wroteConn struct {
	net.Conn
	wrote chan struct{}
}

func (c wroteConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.wrote <- struct{}{}
	return n, err
}

func TestTransportLoss(t *testing.T) {
	f := newFixture(t)
	_, h := f.create("busy", sandboxfs.AccessRead, 0)
	native, err := os.Open(filepath.Join(f.dir, "busy"))
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if err := syscall.Flock(int(native.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	// A second stream of the same attachment reuses its handles.
	cc, sc := net.Pipe()
	served := make(chan error, 1)
	go func() { served <- sandboxfs.Serve(context.Background(), sc, f.svc, f.att) }()
	wrote := make(chan struct{}, 1)
	c := sandboxfs.NewClient(wroteConn{cc, wrote})
	defer c.Close()
	result := make(chan error, 1)
	go func() {
		_, err := c.SetLock(context.Background(), &sandboxfs.SetLockRequest{Handle: h, Kind: sandboxfs.LockFlock, Lock: sandboxfs.Lock{Mode: sandboxfs.LockWrite, End: math.MaxInt64}, Wait: true})
		result <- err
	}()
	<-wrote // the server has read the request
	sc.Close()

	fail := wantFailure(t, <-result, sandboxfs.CodeUnknown, 0, sandboxwire.EffectPossible)
	if !errors.Is(fail, sandboxfs.ErrTransport) {
		t.Fatalf("%v is not a transport failure", fail)
	}
	_, err = c.GetAttr(context.Background(), &sandboxfs.GetAttrRequest{Target: sandboxfs.Target{Kind: sandboxfs.TargetHandle, Handle: h}})
	wantFailure(t, err, sandboxfs.CodeUnknown, 0, sandboxwire.EffectNone)
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the transport closed")
	}
}
