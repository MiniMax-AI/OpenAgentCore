//go:build linux

package worldfs

import (
	"context"
	"errors"
	"sync"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func lockMode(typ uint32) (sandboxfs.LockMode, bool) {
	switch typ {
	case syscall.F_RDLCK:
		return sandboxfs.LockRead, true
	case syscall.F_WRLCK:
		return sandboxfs.LockWrite, true
	case syscall.F_UNLCK:
		return sandboxfs.LockUnlock, true
	}
	return 0, false
}

// GetLk tests a POSIX lock. Without the service's POSIX locks, the kernel's lock calls fail with ENOLCK rather than locking only within the view.
func (f *frontend) GetLk(_ <-chan struct{}, in *fuse.LkIn, out *fuse.LkOut) fuse.Status {
	h, st := f.handle(in.Fh)
	if !st.Ok() {
		return st
	}
	mode, ok := lockMode(in.Lk.Typ)
	switch {
	case !ok || mode == sandboxfs.LockUnlock:
		return fuse.EINVAL
	case h.server == 0 || !f.caps.POSIXLocks:
		return errno(syscall.ENOLCK)
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).GetLock, &sandboxfs.GetLockRequest{
		Handle: h.server, Owner: sandboxfs.LockOwner(in.Owner), Lock: sandboxfs.Lock{Mode: mode, Start: in.Lk.Start, End: in.Lk.End},
	})
	if err != nil {
		return status(err)
	}
	if r.Conflict == nil {
		out.Lk = in.Lk
		out.Lk.Typ = syscall.F_UNLCK
		return fuse.OK
	}
	typ := uint32(syscall.F_RDLCK)
	if r.Conflict.Mode == sandboxfs.LockWrite {
		typ = syscall.F_WRLCK
	}
	out.Lk = fuse.FileLock{Start: r.Conflict.Start, End: r.Conflict.End, Typ: typ}
	return fuse.OK
}

func (f *frontend) SetLk(cancel <-chan struct{}, in *fuse.LkIn) fuse.Status {
	return f.setLock(cancel, in, false)
}

func (f *frontend) SetLkw(cancel <-chan struct{}, in *fuse.LkIn) fuse.Status {
	return f.setLock(cancel, in, true)
}

// lockOrder orders one lock owner's requests on one handle, so that a recovery unlock never removes a lock another request reported. A non-blocking request holds the turn from before it is sent until its recovery is done. A waiting request holds it only to register and, when it must recover, to recover: it waits without it, so interrupts and unlocks get through, and an outcome that needs no recovery returns without it.
type lockOrder struct {
	turn chan struct{}

	mu       sync.Mutex
	acquired uint64 // acquisitions registered
	waiting  int    // waiting requests registered and not yet answered
}

// place is where a request registered.
type place struct {
	acquired uint64 // acquisitions registered up to and including the request
	busy     bool   // a waiting request was out
}

func (o *lockOrder) take(interrupt <-chan struct{}) bool {
	select {
	case o.turn <- struct{}{}:
		return true
	case <-interrupt:
		return false
	}
}

func (o *lockOrder) give() { <-o.turn }

// register records a request. The caller holds the turn.
func (o *lockOrder) register(acquire, wait bool) place {
	o.mu.Lock()
	defer o.mu.Unlock()
	p := place{busy: o.waiting > 0}
	if acquire {
		o.acquired++
	}
	if wait {
		o.waiting++
	}
	p.acquired = o.acquired
	return p
}

// answered records that a waiting request ended.
func (o *lockOrder) answered() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.waiting--
}

// alone reports whether no other request of the owner may have locked after the request at p was sent: none registered since, and none was waiting when it registered. The caller holds the turn.
func (o *lockOrder) alone(p place) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return !p.busy && o.acquired == p.acquired
}

func (h *handle) order(owner sandboxfs.LockOwner) *lockOrder {
	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	o := h.locks[owner]
	if o == nil {
		if h.locks == nil {
			h.locks = map[sandboxfs.LockOwner]*lockOrder{}
		}
		o = &lockOrder{turn: make(chan struct{}, 1)}
		h.locks[owner] = o
	}
	return o
}

// setLock acquires or releases a POSIX or flock lock and reports what the service did, even when the kernel interrupted the request. A request that may have changed the lock but failed is undone with an unlock of the same owner and range, unless another request of the owner may have locked since; then, or when its outcome cannot be learned or the unlock fails, the handle fails and the request returns EIO.
func (f *frontend) setLock(cancel <-chan struct{}, in *fuse.LkIn, wait bool) fuse.Status {
	h, st := f.handle(in.Fh)
	if !st.Ok() {
		return st
	}
	mode, ok := lockMode(in.Lk.Typ)
	if !ok {
		return fuse.EINVAL
	}
	kind, supported := sandboxfs.LockPOSIX, f.caps.POSIXLocks
	if in.LkFlags&fuse.FUSE_LK_FLOCK != 0 {
		kind, supported = sandboxfs.LockFlock, f.caps.Flock
	}
	if h.server == 0 || !supported {
		return errno(syscall.ENOLCK)
	}
	q := &sandboxfs.SetLockRequest{
		Handle: h.server, Kind: kind, Owner: sandboxfs.LockOwner(in.Owner), Lock: sandboxfs.Lock{Mode: mode, Start: in.Lk.Start, End: in.Lk.End}, Wait: wait,
	}
	o := h.order(q.Owner)
	if !o.take(cancel) {
		return fuse.EINTR
	}
	p := o.register(mode != sandboxfs.LockUnlock, wait)
	held := !wait
	if wait {
		o.give()
	}
	_, err := callUntil(f, f.ctx, cancel, (*sandboxfs.Client).SetLock, q)
	if wait {
		o.answered()
	}
	recovered := true
	if err != nil && !noEffect(err) {
		if !held {
			o.take(nil)
			held = true
		}
		if f.seams.undo != nil {
			f.seams.undo()
		}
		recovered = f.undoLock(h, q, answered(err) && o.alone(p))
	}
	if held {
		o.give()
	}
	var fail *sandboxfs.Failure
	switch {
	case err == nil:
		return fuse.OK
	case !recovered:
		return fuse.EIO
	case errors.Is(err, errInterrupted) || errors.As(err, &fail) && fail.Code == sandboxfs.CodeCancelled || interrupted(cancel) && !answered(err):
		return fuse.EINTR
	}
	return status(err)
}

// undoLock unlocks the owner's range after a lock request that may have changed it failed, and reports whether it did. The unlock is safe only when the service runs it after that request and no other request of the owner may have locked in between, which safe says; otherwise, or when the unlock fails, the handle fails instead.
func (f *frontend) undoLock(h *handle, q *sandboxfs.SetLockRequest, safe bool) bool {
	if safe {
		unlock := *q
		unlock.Lock.Mode, unlock.Wait = sandboxfs.LockUnlock, false
		if _, err := call(f, f.ctx, (*sandboxfs.Client).SetLock, &unlock); err == nil {
			return true
		}
	}
	h.failed.Store(true)
	return false
}

// answered reports whether a failure came in the service's response, so a later request on the handle runs after the failed one. After a stream or context failure the request may still be running.
func answered(err error) bool {
	return !errors.Is(err, sandboxfs.ErrTransport) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
