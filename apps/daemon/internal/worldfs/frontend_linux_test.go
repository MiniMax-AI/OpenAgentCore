//go:build linux

package worldfs

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/fileservicetest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

// These tests drive the frontend's FUSE methods directly, without a mount, so they need no privileges.

// counts records what the service ran. It refuses Releases while refuse is positive, and when held is set it closes held once a waiting lock request arrives and ends that request Cancelled with EffectPossible once it is cancelled. When cut is set, the next Open runs and then breaks every stream, so its response is lost.
type counts struct {
	srv      *fileservicetest.Server
	refuse   atomic.Int32
	opened   atomic.Int32
	released atomic.Int32
	cut      atomic.Bool
	held     chan struct{}

	mu    sync.Mutex
	tries []time.Time // when each Release arrived
}

type counting struct {
	sandboxfs.Service
	*counts
}

func (c counting) Release(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.ReleaseRequest) (*sandboxfs.ReleaseResponse, error) {
	c.mu.Lock()
	c.tries = append(c.tries, time.Now())
	c.mu.Unlock()
	if c.refuse.Add(-1) >= 0 {
		return nil, sandboxfs.NewFailure(sandboxfs.CodeResourceExhausted, sandboxwire.EffectNone, "too many requests in flight")
	}
	r, err := c.Service.Release(ctx, a, q)
	if err == nil {
		c.released.Add(1)
	}
	return r, err
}

func (c counting) Open(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.OpenRequest) (*sandboxfs.OpenResponse, error) {
	r, err := c.Service.Open(ctx, a, q)
	if err == nil {
		c.opened.Add(1)
	}
	if c.cut.CompareAndSwap(true, false) {
		c.srv.Break()
	}
	return r, err
}

func (c counting) SetLock(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.SetLockRequest) (*sandboxfs.SetLockResponse, error) {
	if c.held == nil || !q.Wait {
		return c.Service.SetLock(ctx, a, q)
	}
	close(c.held)
	<-ctx.Done()
	return nil, sandboxfs.NewFailure(sandboxfs.CodeCancelled, sandboxwire.EffectPossible, "cancelled after it may have locked")
}

// newServer serves a directory holding the empty file f and returns the file's path.
func newServer(t *testing.T) (*fileservicetest.Server, *counts, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := fileservicetest.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	c := &counts{srv: srv}
	srv.Intercept(func(s sandboxfs.Service) sandboxfs.Service { return counting{s, c} })
	return srv, c, path
}

// attached returns a frontend attached through dial as Serve leaves it, but neither mounted nor draining.
func attached(t *testing.T, dial Dial) *frontend {
	t.Helper()
	f := New(fileservicetest.Export, dial).fs
	if _, _, err := f.attach(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.shutdown)
	return f
}

func draining(t *testing.T, f *frontend) {
	go f.drain()
	t.Cleanup(func() {
		f.stopDrain()
		<-f.drained
	})
}

func open(t *testing.T, f *frontend, name string) uint64 {
	t.Helper()
	var e fuse.EntryOut
	if st := f.Lookup(nil, &fuse.InHeader{NodeId: f.root.id}, name, &e); !st.Ok() {
		t.Fatalf("Lookup %s: %v", name, st)
	}
	var o fuse.OpenOut
	if st := f.Open(nil, &fuse.OpenIn{InHeader: fuse.InHeader{NodeId: e.NodeId}, Flags: syscall.O_RDWR}, &o); !st.Ok() {
		t.Fatalf("Open %s: %v", name, st)
	}
	return o.Fh
}

// flock sends a whole-file flock request, as the kernel does.
func flock(f *frontend, cancel <-chan struct{}, fh, owner uint64, typ uint32, wait bool) fuse.Status {
	in := &fuse.LkIn{Fh: fh, Owner: owner, LkFlags: fuse.FUSE_LK_FLOCK, Lk: fuse.FileLock{End: math.MaxInt64, Typ: typ}}
	if wait {
		return f.SetLkw(cancel, in)
	}
	return f.SetLk(cancel, in)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within 2s", what)
		}
	}
}

// A read lock requested while a failed conversion awaits its recovery unlock survives that unlock.
func TestLockRecoveryKeepsLaterLock(t *testing.T) {
	srv, _, path := newServer(t)
	f := attached(t, srv.Dial)
	fh, other := open(t, f, "f"), open(t, f, "f")
	if flock(f, nil, fh, 1, syscall.F_RDLCK, false) != fuse.OK || flock(f, nil, other, 2, syscall.F_RDLCK, false) != fuse.OK {
		t.Fatal("read locks failed")
	}
	later := make(chan fuse.Status, 1)
	f.seams.undo = func() {
		go func() { later <- flock(f, nil, fh, 1, syscall.F_RDLCK, false) }()
		select {
		case st := <-later:
			later <- st // it ran before the recovery
		case <-time.After(200 * time.Millisecond):
		}
	}
	// The other handle's read lock makes the conversion fail, and the failed conversion drops fh's read lock.
	if st := flock(f, nil, fh, 1, syscall.F_WRLCK, false); st == fuse.OK {
		t.Fatal("conversion succeeded beside another read lock")
	}
	if st := <-later; st != fuse.OK {
		t.Fatalf("later read lock: %v", st)
	}
	if flock(f, nil, other, 2, syscall.F_UNLCK, false) != fuse.OK {
		t.Fatal("unlock failed")
	}
	native, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if err := unix.Flock(int(native.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != unix.EWOULDBLOCK {
		t.Fatalf("native exclusive lock: %v, want EWOULDBLOCK while the later read lock holds", err)
	}
}

// A Release the service refuses for now is sent again until it runs.
func TestRefusedReleaseIsRetried(t *testing.T) {
	srv, c, _ := newServer(t)
	c.refuse.Store(1)
	f := attached(t, srv.Dial)
	fh := open(t, f, "f")
	draining(t, f)
	f.Release(nil, &fuse.ReleaseIn{Fh: fh})
	eventually(t, "the refused Release", func() bool { return c.released.Load() == 1 })
}

// A Release the service refuses right after the drainer redialed to send it waits for the backoff before it goes again.
func TestRefusalAfterOwnRedialWaits(t *testing.T) {
	srv, c, _ := newServer(t)
	var down atomic.Bool
	f := attached(t, func(ctx context.Context) (io.ReadWriteCloser, error) {
		if down.Load() {
			return nil, errors.New("unreachable")
		}
		return srv.Dial(ctx)
	})
	fh := open(t, f, "f")
	down.Store(true)
	srv.Break()
	<-f.conn.Done()
	f.Release(nil, &fuse.ReleaseIn{Fh: fh}) // never sent: queued
	c.refuse.Store(2)
	down.Store(false)
	draining(t, f)
	eventually(t, "the refused Release", func() bool { return c.released.Load() == 1 })
	c.mu.Lock()
	defer c.mu.Unlock()
	if gap := c.tries[1].Sub(c.tries[0]); gap < retryMin {
		t.Errorf("sent again after %v, want at least %v", gap, retryMin)
	}
}

// A Release queued just after another request redialed, once the drainer has taken that redial's wakeup, is still sent.
func TestReleaseQueuedAfterRedial(t *testing.T) {
	srv, c, _ := newServer(t)
	var down atomic.Bool
	f := attached(t, func(ctx context.Context) (io.ReadWriteCloser, error) {
		if down.Load() {
			return nil, errors.New("unreachable")
		}
		return srv.Dial(ctx)
	})
	fh := open(t, f, "f")
	down.Store(true)
	srv.Break()
	<-f.conn.Done()
	f.seams.queue = func() {
		down.Store(false)
		if _, err := f.client(context.Background(), nil); err != nil {
			t.Errorf("redial: %v", err)
		}
		<-f.kick
	}
	f.Release(nil, &fuse.ReleaseIn{Fh: fh})
	draining(t, f)
	eventually(t, "the queued Release", func() bool { return c.released.Load() == 1 })
}

// An Open whose response is lost while the service is unreachable fails with EIO at once, and its handle ID is released on the next stream once the service is reachable, after the service finished the Open, so the service holds no handle.
func TestLostOpenIsReleased(t *testing.T) {
	srv, c, _ := newServer(t)
	var down atomic.Bool
	reachable := make(chan struct{})
	f := attached(t, func(ctx context.Context) (io.ReadWriteCloser, error) {
		if down.Load() {
			select {
			case <-reachable:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return srv.Dial(ctx)
	})
	draining(t, f)
	var e fuse.EntryOut
	if st := f.Lookup(nil, &fuse.InHeader{NodeId: f.root.id}, "f", &e); !st.Ok() {
		t.Fatalf("Lookup: %v", st)
	}
	down.Store(true)
	c.cut.Store(true)
	got := make(chan fuse.Status, 1)
	go func() {
		var o fuse.OpenOut
		got <- f.Open(nil, &fuse.OpenIn{InHeader: fuse.InHeader{NodeId: e.NodeId}, Flags: syscall.O_RDWR}, &o)
	}()
	wantStatus(t, got, fuse.EIO)
	close(reachable)
	eventually(t, "the release of the lost handle", func() bool { return c.released.Load() == 1 })
	if c.opened.Load() != 1 {
		t.Fatalf("%d opened, want 1", c.opened.Load())
	}
}

// lateAttach closes attaching when Attach arrives and attaches only once the request was cancelled, as a handler that outlives its stream does.
type lateAttach struct {
	sandboxfs.Service
	attaching chan struct{}
}

func (l lateAttach) Attach(ctx context.Context, a sandboxfs.Attachment, q *sandboxfs.AttachRequest) (*sandboxfs.AttachResponse, error) {
	close(l.attaching)
	<-ctx.Done()
	if _, err := l.Service.Attach(context.WithoutCancel(ctx), a, q); err != nil {
		return nil, err
	}
	return nil, ctx.Err()
}

// A Serve whose context ends while Attach is in doubt detaches on a new stream, which the service serves after the late Attach took effect, so the attachment holds nothing and the failure is not ErrAttachmentDirty.
func TestUncertainAttachIsDetached(t *testing.T) {
	srv, err := fileservicetest.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	attaching := make(chan struct{})
	srv.Intercept(func(s sandboxfs.Service) sandboxfs.Service { return lateAttach{s, attaching} })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-attaching
		cancel()
	}()
	if _, _, err := New(fileservicetest.Export, srv.Dial).Serve(ctx, nil, sessionview.WorldMount{}); !errors.Is(err, context.Canceled) || errors.Is(err, ErrAttachmentDirty) {
		t.Fatalf("Serve = %v, want the cancellation without ErrAttachmentDirty", err)
	}
	rw, err := srv.Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := sandboxfs.NewClient(rw)
	defer c.Close()
	var fail *sandboxfs.Failure
	if _, err := c.Detach(context.Background(), &sandboxfs.DetachRequest{}); !errors.As(err, &fail) || fail.Code != sandboxfs.CodeStaleAttachment {
		t.Fatalf("Detach after Serve: %v, want StaleAttachment", err)
	}
}

// jammed passes reads through and, while jam is set, holds every write and Close until free closes, as a transport whose outgoing traffic is stuck does.
type jammed struct {
	io.ReadWriteCloser
	jam  *atomic.Bool
	free <-chan struct{}
}

func (j jammed) Write(p []byte) (int, error) {
	if j.jam.Load() {
		<-j.free
	}
	return j.ReadWriteCloser.Write(p)
}

func (j jammed) Close() error {
	if j.jam.Load() {
		<-j.free
	}
	return j.ReadWriteCloser.Close()
}

// A Serve whose context ends while Attach is in doubt and the transport holds every write returns ErrAttachmentDirty within detachWait, teardown included.
func TestAbortBoundedWhenTransportBlocks(t *testing.T) {
	srv, err := fileservicetest.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	attaching := make(chan struct{})
	srv.Intercept(func(s sandboxfs.Service) sandboxfs.Service { return lateAttach{s, attaching} })
	var jam atomic.Bool
	free := make(chan struct{})
	t.Cleanup(func() { close(free) })
	w := New(fileservicetest.Export, func(ctx context.Context) (io.ReadWriteCloser, error) {
		rw, err := srv.Dial(ctx)
		if err != nil {
			return nil, err
		}
		return jammed{rw, &jam, free}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-attaching
		// A request completed after Attach shows that Attach's write has finished, so the cancellation finds Attach in doubt rather than mid-write.
		if _, err := w.fs.conn.Describe(context.Background(), &sandboxfs.DescribeRequest{}); err != nil {
			t.Errorf("Describe beside Attach: %v", err)
		}
		jam.Store(true)
		cancel()
	}()
	served := make(chan error, 1)
	go func() {
		_, _, err := w.Serve(ctx, nil, sessionview.WorldMount{})
		served <- err
	}()
	select {
	case err := <-served:
		if !errors.Is(err, ErrAttachmentDirty) {
			t.Fatalf("Serve = %v, want ErrAttachmentDirty", err)
		}
	case <-time.After(detachWait + 2*time.Second):
		t.Fatal("Serve still cleaning up 2s after the detach bound")
	}
}

// stalled returns a frontend whose stream has failed and whose service is unreachable, with fh open, and a channel closed once a redial waits.
func stalled(t *testing.T) (f *frontend, fh uint64, dialing chan struct{}) {
	srv, _, _ := newServer(t)
	var stall atomic.Bool
	var once sync.Once
	dialing = make(chan struct{})
	f = attached(t, func(ctx context.Context) (io.ReadWriteCloser, error) {
		if stall.Load() {
			once.Do(func() { close(dialing) })
		}
		return srv.Dial(ctx)
	})
	fh = open(t, f, "f")
	stall.Store(true)
	srv.Stall()
	<-f.conn.Done()
	return f, fh, dialing
}

func wantStatus(t *testing.T, got <-chan fuse.Status, want fuse.Status) {
	t.Helper()
	select {
	case st := <-got:
		if st != want {
			t.Fatalf("status %v, want %v", st, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no status within 2s, want %v", want)
	}
}

// A waiting lock blocked on redialing an unreachable service returns EINTR once interrupted.
func TestLockInterruptedWhileRedialing(t *testing.T) {
	f, fh, dialing := stalled(t)
	cancel := make(chan struct{})
	got := make(chan fuse.Status, 1)
	go func() { got <- flock(f, cancel, fh, 1, syscall.F_WRLCK, true) }()
	<-dialing
	close(cancel)
	wantStatus(t, got, fuse.EINTR)
}

// An interrupted waiting lock that was never sent returns at once, although a non-blocking request of its owner holds the turn while it waits for the stream.
func TestUnsentLockInterruptedBehindTurn(t *testing.T) {
	f, fh, dialing := stalled(t)
	go f.client(f.ctx, nil) // another request redials and holds the stream
	<-dialing
	h, _ := f.handle(fh)
	o := h.order(1)
	registered := func(n uint64) func() bool {
		return func() bool {
			o.mu.Lock()
			defer o.mu.Unlock()
			return o.acquired == n
		}
	}
	cancel := make(chan struct{})
	got := make(chan fuse.Status, 1)
	go func() { got <- flock(f, cancel, fh, 1, syscall.F_WRLCK, true) }()
	eventually(t, "the waiting lock's registration", registered(1))
	go flock(f, nil, fh, 1, syscall.F_RDLCK, false)
	eventually(t, "the non-blocking lock's turn", registered(2))
	close(cancel)
	wantStatus(t, got, fuse.EINTR)
}

// A waiting lock cancelled after it may have locked, once another acquisition of its owner has completed, cannot be undone safely: it returns EIO.
func TestUnsafeLockRecoveryFails(t *testing.T) {
	srv, c, _ := newServer(t)
	c.held = make(chan struct{})
	f := attached(t, srv.Dial)
	fh := open(t, f, "f")
	cancel := make(chan struct{})
	got := make(chan fuse.Status, 1)
	go func() { got <- flock(f, cancel, fh, 1, syscall.F_WRLCK, true) }()
	<-c.held
	if st := flock(f, nil, fh, 1, syscall.F_RDLCK, false); st != fuse.OK {
		t.Fatalf("read lock: %v", st)
	}
	close(cancel)
	wantStatus(t, got, fuse.EIO)
}

// Serve gives up when Start's context ends, even while Describe gets no answer, and Stop then returns at once.
func TestServeEndsWithItsContext(t *testing.T) {
	w := New(fileservicetest.Export, func(context.Context) (io.ReadWriteCloser, error) {
		c, s := net.Pipe()
		go io.Copy(io.Discard, s) // reads requests and never answers
		t.Cleanup(func() { s.Close() })
		return c, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	served := make(chan error, 1)
	go func() {
		_, _, err := w.Serve(ctx, nil, sessionview.WorldMount{})
		served <- err
	}()
	select {
	case err := <-served:
		if !errors.Is(err, ErrConnect) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Serve = %v, want ErrConnect with the deadline", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve still waiting 2s after its context ended")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- w.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked after a failed Serve")
	}
}
