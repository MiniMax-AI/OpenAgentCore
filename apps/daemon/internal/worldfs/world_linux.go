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
		kick:      make(chan struct{}, 1),
		drainCtx:  drainCtx,
		stopDrain: stopDrain,
		drained:   make(chan struct{}),
		lost:      make(chan struct{}),
		served:    make(chan struct{}),
	}}
}

// Serve has the signature of [sessionview.World]. It connects, attaches the export, presents the mountpoints and starts serving dev. A World serves once.
func (w *World) Serve(dev *os.File, mount sessionview.WorldMount) (sessionview.WorldServer, sessionview.Presentation, error) {
	p, err := w.fs.serve(dev, mount)
	if err != nil {
		return nil, sessionview.Presentation{}, err
	}
	return w, p, nil
}

// Stop waits up to 10 seconds for serving to end, which happens once the view's mount namespace is gone. It then detaches, ending every request still waiting on the service after 5 more seconds, so it returns within 15 seconds. It unmounts nothing.
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

	connMu   sync.Mutex
	conn     *sandboxfs.Client
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
	releases []cleanup                    // handles the kernel released whose Release a failed stream never sent

	kick      chan struct{} // wakes the drainer
	drainCtx  context.Context
	stopDrain context.CancelFunc
	drained   chan struct{}

	started  atomic.Bool
	attached bool
	dead     atomic.Bool // the attachment is gone: no request reaches the service again
	closed   atomic.Bool // Stop began: kernel requests fail
	lostOnce sync.Once
	lost     chan struct{}
	lostErr  error
	served   chan struct{}
	stopOnce sync.Once
	stopErr  error
}

func (f *frontend) serve(dev *os.File, mount sessionview.WorldMount) (sessionview.Presentation, error) {
	if !f.started.CompareAndSwap(false, true) {
		return sessionview.Presentation{}, &Error{Kind: ErrConnect, Op: "serve", Err: errors.New("the world already serves a view")}
	}
	f.view = sandboxfs.Identity{UID: mount.UID, GID: mount.GID}
	now := time.Now()
	f.born = sandboxfs.Timestamp{Sec: now.Unix(), Nsec: uint32(now.Nanosecond())}
	p, opts, err := f.attach(mount.Mountpoints)
	if err == nil {
		err = f.start(dev, opts)
	}
	if err != nil {
		f.abort()
		return sessionview.Presentation{}, err
	}
	return p, nil
}

// attach connects, checks the service's declarations, attaches the export and presents the mountpoints.
func (f *frontend) attach(mps []sessionview.Mountpoint) (sessionview.Presentation, *fuse.MountOptions, error) {
	c, d, err := f.connect(f.ctx)
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
	a, err := c.Attach(f.ctx, &sandboxfs.AttachRequest{Export: f.export})
	if err != nil {
		// An Attach whose outcome is unknown may have attached: abort detaches it rather than attaching again.
		var fail *sandboxfs.Failure
		f.attached = !errors.As(err, &fail) || fail.Effect != sandboxwire.EffectNone
		return sessionview.Presentation{}, nil, &Error{Kind: ErrConnect, Op: "attach", Path: string(f.export), Err: err}
	}
	f.attached = true
	f.root = f.newInode(a.Root.Node)
	f.root.attr, f.root.held = a.Root.Attr, true
	p, err := f.present(mps)
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

// abort undoes a failed Serve.
func (f *frontend) abort() {
	if f.attached && !f.dead.Load() {
		ctx, cancel := context.WithTimeout(f.ctx, detachWait)
		_, _ = call(f, ctx, (*sandboxfs.Client).Detach, &sandboxfs.DetachRequest{})
		cancel()
	}
	f.shutdown()
}

// connect opens a stream and describes the service within ctx.
func (f *frontend) connect(ctx context.Context) (*sandboxfs.Client, *sandboxfs.DescribeResponse, error) {
	rw, err := f.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	c := sandboxfs.NewClient(rw)
	d, err := c.Describe(ctx, &sandboxfs.DescribeRequest{})
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	return c, d, nil
}

var errDead = errors.New("worldfs: the world is lost")

// client returns the stream's client. After the stream failed it redials within ctx and continues only with the same service instance. A failed redial is an [ErrConnect] error: the request was never sent.
func (f *frontend) client(ctx context.Context) (*sandboxfs.Client, error) {
	f.connMu.Lock()
	defer f.connMu.Unlock()
	if f.dead.Load() {
		return nil, errDead
	}
	if c := f.conn; c != nil {
		select {
		case <-c.Done():
		default:
			return c, nil
		}
	}
	c, d, err := f.connect(ctx)
	if err != nil {
		f.observe(err)
		return nil, &Error{Kind: ErrConnect, Op: "reconnect", Err: err}
	}
	if d.ServerInstanceID != f.instance {
		c.Close()
		f.lose(&Error{Kind: ErrInstanceChanged, Op: "reconnect"}, true)
		return nil, errDead
	}
	f.conn = c
	f.wake() // cleanup the failed stream never sent goes on the new one
	return c, nil
}

// call sends one request. A request the stream failed before sending cannot have taken effect, so it is sent once more on a new stream; nothing else is retried.
func call[Q, R any](f *frontend, ctx context.Context, op func(*sandboxfs.Client, context.Context, Q) (R, error), q Q) (R, error) {
	var r R
	var err error
	for range 2 {
		var c *sandboxfs.Client
		if c, err = f.client(ctx); err != nil {
			return r, err
		}
		if r, err = op(c, ctx, q); err == nil {
			return r, nil
		}
		f.observe(err)
		if !unsent(err) {
			break
		}
	}
	return r, err
}

// unsent reports whether err shows that the request never left the frontend: the redial failed, or the stream had failed before the request was written.
func unsent(err error) bool {
	var fail *sandboxfs.Failure
	return errors.Is(err, ErrConnect) || errors.As(err, &fail) && fail.Effect == sandboxwire.EffectNone && errors.Is(err, sandboxfs.ErrTransport)
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
	<-f.drained
	if !f.dead.Load() {
		if _, err := call(f, ctx, (*sandboxfs.Client).Detach, &sandboxfs.DetachRequest{}); err != nil {
			errs = append(errs, &Error{Kind: ErrConnect, Op: "detach", Err: err})
		}
	}
	f.shutdown()
	return errors.Join(errs...)
}

func (f *frontend) shutdown() {
	f.cancel()
	f.connMu.Lock()
	defer f.connMu.Unlock()
	if f.conn != nil {
		f.conn.Close()
	}
}
