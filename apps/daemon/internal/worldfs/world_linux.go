//go:build linux

package worldfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

const (
	// stopWait bounds how long Stop waits for serving to end.
	stopWait = 10 * time.Second
	// detachWait bounds Detach, with the redial it may need, when Stop or a failed Serve ends the attachment.
	detachWait = 5 * time.Second
	// retryMin and retryMax bound the backoff before the drainer sends again what it could not send.
	retryMin = 20 * time.Millisecond
	retryMax = 2 * time.Second
	// forgetBatch is the most entries one Forget request carries.
	forgetBatch = 4096
)

// World serves one Session's world to one view.
type World struct{ fs *frontend }

// New returns a world that attaches export through the streams dial opens.
func New(export sandboxlink.ExportID, dial Dial) *World {
	ctx, cancel := context.WithCancel(context.Background())
	drainCtx, stopDrain := context.WithCancel(ctx)
	return &World{fs: &frontend{
		export:    export,
		dial:      dial,
		ctx:       ctx,
		cancel:    cancel,
		nodes:     map[uint64]*inode{},
		byRef:     map[sandboxfs.NodeRef]*inode{},
		handles:   map[uint64]*handle{},
		forgets:   map[sandboxfs.NodeRef]uint64{},
		connTurn:  make(chan struct{}, 1),
		kick:      make(chan struct{}, 1),
		drainCtx:  drainCtx,
		stopDrain: stopDrain,
		drained:   make(chan struct{}),
		lost:      make(chan struct{}),
		served:    make(chan struct{}),
	}}
}

// Serve has the signature of [sessionview.World]. Within ctx it connects, attaches the export and presents the mountpoints; it then starts serving dev. A World serves once.
//
// A Serve that fails after the export may have been attached detaches it on a new stream, and returns within 5 more seconds even when ctx has ended or the transport blocks. When that Detach cannot be sent or answered in that time, Serve returns [ErrAttachmentDirty] and the owner of the Link attachment must end it.
func (w *World) Serve(ctx context.Context, dev *os.File, mount sessionview.WorldMount) (sessionview.WorldServer, sessionview.Presentation, error) {
	p, err := w.fs.serve(ctx, dev, mount)
	if err != nil {
		return nil, sessionview.Presentation{}, err
	}
	return w, p, nil
}

// Stop waits up to 10 seconds for serving to end, which happens once the view's mount namespace is gone. It then detaches, ending every request still waiting on the service after 5 more seconds, so it returns within 15 seconds even when the transport blocks. It unmounts nothing. After a Serve that failed, or without one, it returns at once.
func (w *World) Stop() error {
	w.fs.stopOnce.Do(func() { w.fs.stopErr = w.fs.stop() })
	return w.fs.stopErr
}

// Lost is closed once the view no longer shows the world faithfully: the service incarnation changed, the attachment ended, or the sandbox changed a pinned entry. The view must then be rebuilt.
func (w *World) Lost() <-chan struct{} { return w.fs.lost }

// Err reports why Lost closed, or nil while it is open.
func (w *World) Err() error {
	select {
	case <-w.fs.lost:
		return w.fs.lostErr
	default:
		return nil
	}
}

// frontend is the FUSE file system. go-fuse calls it from its reader goroutines.
type frontend struct {
	export sandboxlink.ExportID
	dial   Dial
	ctx    context.Context
	cancel context.CancelFunc

	connTurn chan struct{} // held to use or replace conn; a channel so that waiting for it can be interrupted
	conn     *sandboxfs.Client
	ids      sandboxfs.HandleIDs // the attachment's handle IDs, never reused
	gen      atomic.Uint64       // counts redials
	instance sandboxwire.ID
	service  sandboxfs.Identity // the identity the service acts as
	view     sandboxfs.Identity // the identity the view's processes run as
	caps     sandboxfs.Capabilities

	mu       sync.Mutex
	nodes    map[uint64]*inode
	byRef    map[sandboxfs.NodeRef]*inode
	lastID   uint64
	handles  map[uint64]*handle
	lastFh   uint64
	root     *inode
	born     sandboxfs.Timestamp
	forgets  map[sandboxfs.NodeRef]uint64 // references the kernel released, not yet sent
	releases []cleanup                    // releases for the drainer: of handles the kernel closed whose Release went unanswered, and of acquisitions in doubt

	kick      chan struct{} // wakes the drainer
	drainCtx  context.Context
	stopDrain context.CancelFunc
	drained   chan struct{}

	started  atomic.Bool
	serving  atomic.Bool // Serve succeeded
	attached bool        // Attach succeeded
	maybe    bool        // Attach failed after it may have taken effect
	dead     atomic.Bool // the attachment is gone: no request reaches the service again
	closed   atomic.Bool // Stop began: kernel requests fail
	lostOnce sync.Once
	lost     chan struct{}
	lostErr  error
	served   chan struct{}
	stopOnce sync.Once
	stopErr  error

	// seams let tests run other requests at the points where order matters. They are nil outside tests.
	seams struct {
		undo  func() // after a lock request failed, before its recovery
		queue func() // after a cleanup failed, before it is queued
	}
}

func (f *frontend) serve(ctx context.Context, dev *os.File, mount sessionview.WorldMount) (sessionview.Presentation, error) {
	if !f.started.CompareAndSwap(false, true) {
		return sessionview.Presentation{}, &Error{Kind: ErrConnect, Op: "serve", Err: errors.New("the world already serves a view")}
	}
	f.view = sandboxfs.Identity{UID: mount.UID, GID: mount.GID}
	now := time.Now()
	f.born = sandboxfs.Timestamp{Sec: now.Unix(), Nsec: uint32(now.Nanosecond())}
	p, opts, err := f.attach(ctx, mount.Mountpoints)
	if err == nil {
		err = f.start(dev, opts)
	}
	if err != nil {
		return sessionview.Presentation{}, f.abort(ctx, err)
	}
	f.serving.Store(true)
	return p, nil
}

// attach connects, checks the service's declarations, attaches the export and presents the mountpoints.
func (f *frontend) attach(ctx context.Context, mps []sessionview.Mountpoint) (sessionview.Presentation, *fuse.MountOptions, error) {
	c, d, err := f.connect(ctx)
	if err != nil {
		return sessionview.Presentation{}, nil, &Error{Kind: ErrConnect, Op: "describe", Err: err}
	}
	f.conn, f.instance, f.service, f.caps = c, d.ServerInstanceID, d.Identity, d.Capabilities
	maxIO := min(f.caps.MaxReadBytes, f.caps.MaxWriteBytes) &^ uint32(os.Getpagesize()-1)
	switch {
	case f.caps.PathProfile != sandboxfs.PathProfileLinuxBytes || f.caps.CacheProfile != sandboxfs.CacheProfileUncached:
		return sessionview.Presentation{}, nil, &Error{Kind: ErrIncompatible, Op: "describe", Err: fmt.Errorf("path profile %d, cache profile %d", f.caps.PathProfile, f.caps.CacheProfile)}
	case f.caps.ReadOnly:
		return sessionview.Presentation{}, nil, &Error{Kind: ErrIncompatible, Op: "describe", Err: errors.New("the export is read-only")}
	case maxIO == 0 || f.caps.MaxWalkComponents == 0 || f.caps.MaxReadDirBytes == 0:
		return sessionview.Presentation{}, nil, &Error{Kind: ErrIncompatible, Op: "describe", Err: errors.New("read, write, walk or directory limit too small")}
	}
	a, err := c.Attach(ctx, &sandboxfs.AttachRequest{Export: f.export})
	if err != nil {
		var fail *sandboxfs.Failure
		f.maybe = !errors.As(err, &fail) || fail.Effect != sandboxwire.EffectNone
		return sessionview.Presentation{}, nil, &Error{Kind: ErrConnect, Op: "attach", Path: string(f.export), Err: err}
	}
	f.attached = true
	f.root = f.newInode(a.Root.Node)
	f.root.attr, f.root.held = a.Root.Attr, true
	p, err := f.present(ctx, mps)
	if err != nil {
		return sessionview.Presentation{}, nil, err
	}
	return p, &fuse.MountOptions{
		MaxWrite:    int(maxIO),
		EnableLocks: true,
		// Only what the mapping implements: no read-ahead, page cache or open-less modes, no READDIRPLUS, passthrough or id-mapped mounts.
		DisabledCapabilities: fuse.CAP_ASYNC_READ | fuse.CAP_FILE_OPS | fuse.CAP_AUTO_INVAL_DATA | fuse.CAP_READDIRPLUS |
			fuse.CAP_NO_OPEN_SUPPORT | fuse.CAP_PASSTHROUGH | fuse.CAP_ALLOW_IDMAP,
		ExtraCapabilities: fuse.CAP_ATOMIC_O_TRUNC,
	}, nil
}

// start serves dev on a duplicate descriptor, which go-fuse owns and closes when serving ends.
func (f *frontend) start(dev *os.File, opts *fuse.MountOptions) error {
	fd, err := unix.FcntlInt(dev.Fd(), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return &Error{Kind: ErrConnect, Op: "dup", Err: err}
	}
	srv, err := fuse.NewServer(f, fmt.Sprintf("/dev/fd/%d", fd), opts)
	if err != nil {
		return &Error{Kind: ErrConnect, Op: "init", Err: err}
	}
	go f.drain()
	go func() {
		srv.Serve()
		close(f.served)
	}()
	return nil
}

// abort undoes a failed Serve and returns its error. When the export may be attached, it drops the stream and detaches on a new one: the service serves that stream only after every request of the earlier ones has finished, so Detach releases whatever Attach, or a request the failure abandoned, left. Detach is settled when it succeeds or the world is lost, since a lost attachment or incarnation holds nothing; anything else, including no answer within detachWait, is [ErrAttachmentDirty].
func (f *frontend) abort(ctx context.Context, err error) error {
	close(f.served)
	close(f.drained)
	defer f.shutdown()
	if !f.attached && !f.maybe || f.dead.Load() {
		return err
	}
	f.drop()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detachWait)
	defer cancel()
	if derr := f.detach(ctx); derr != nil && !f.dead.Load() {
		return &Error{Kind: ErrAttachmentDirty, Op: "detach", Err: errors.Join(err, derr)}
	}
	return err
}

// detach sends Detach and waits for it until ctx ends. The request runs on its own goroutine, because a transport that blocks can hold a write, and the stream close a cancellation starts, past ctx.
func (f *frontend) detach(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		_, err := call(f, ctx, (*sandboxfs.Client).Detach, &sandboxfs.DetachRequest{})
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		select {
		case err := <-done:
			return err
		default:
			return &Error{Kind: ErrConnect, Op: "detach", Err: ctx.Err()}
		}
	}
}

// observe marks the world lost when err shows that the service incarnation or the attachment is gone: a File failure that says so, or a Link failure that is not retryable, such as LeaseExpired or StaleGeneration on a redial.
func (f *frontend) observe(err error) {
	var fail *sandboxfs.Failure
	if errors.As(err, &fail) {
		switch fail.Code {
		case sandboxfs.CodeInstanceChanged:
			f.lose(&Error{Kind: ErrInstanceChanged, Err: err}, true)
		case sandboxfs.CodeStaleAttachment:
			f.lose(&Error{Kind: ErrAttachmentLost, Err: err}, true)
		}
		return
	}
	code, ok := linkCode(err)
	switch {
	case !ok || code.Retryable():
	case code == sandboxlink.InstanceChanged:
		f.lose(&Error{Kind: ErrInstanceChanged, Err: err}, true)
	default:
		f.lose(&Error{Kind: ErrAttachmentLost, Err: err}, true)
	}
}

func linkCode(err error) (sandboxlink.Code, bool) {
	var e *sandboxlink.Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	var c sandboxlink.Code
	return c, errors.As(err, &c)
}

// lose reports why the view must be rebuilt. dead also fails every later request.
func (f *frontend) lose(err *Error, dead bool) {
	if dead {
		f.dead.Store(true)
	}
	f.lostOnce.Do(func() {
		f.lostErr = err
		close(f.lost)
	})
}

func (f *frontend) stop() error {
	if !f.serving.Load() {
		f.shutdown()
		return nil
	}
	var errs []error
	select {
	case <-f.served:
	case <-time.After(stopWait):
		errs = append(errs, &Error{Kind: ErrConnect, Op: "stop", Err: errors.New("the view's mount still exists")})
	}
	f.closed.Store(true)
	ctx, cancel := context.WithTimeout(f.ctx, detachWait)
	defer cancel()
	// At the deadline every request still on f.ctx ends too, such as a redial that holds the stream.
	context.AfterFunc(ctx, f.cancel)
	// Detach drops every reference and handle the attachment holds, so nothing queued needs sending.
	f.stopDrain()
	select {
	case <-f.drained:
	case <-ctx.Done():
	}
	if !f.dead.Load() {
		if err := f.detach(ctx); err != nil {
			errs = append(errs, &Error{Kind: ErrConnect, Op: "detach", Err: err})
		}
	}
	f.shutdown()
	return errors.Join(errs...)
}

// shutdown ends every request and redial on f.ctx, and closes the stream once no redial holds it, without waiting for the transport. client returns no stream after it.
func (f *frontend) shutdown() {
	f.cancel()
	go f.drop()
}

// drop closes the stream without waiting for the transport, so the next request redials.
func (f *frontend) drop() {
	f.connTurn <- struct{}{}
	c := f.conn
	f.conn = nil
	<-f.connTurn
	if c != nil {
		go c.Close()
	}
}
