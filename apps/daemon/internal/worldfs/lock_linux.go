//go:build linux

package worldfs

import (
	"context"
	"errors"
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

// lockOrder orders one lock owner's requests on one handle, so that a recovery unlock never removes a lock another request reported. A non-blocking request holds the turn from before it is sent until its recovery is done. A waiting request takes the turn only to register and, once answered, to recover, so interrupts and unlocks get through while it waits.
type lockOrder struct {
	turn     chan struct{}
	waiting  int    // waiting requests in flight, guarded by turn
	acquired uint64 // acquisitions sent, guarded by turn
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

// setLock acquires or releases a POSIX or flock lock and reports what the service did, even when the kernel interrupted the request. A request that may have changed the lock but failed is undone with an unlock of the same owner and range, unless another request of the owner may have locked since; then, or when its outcome cannot be learned, the handle fails.
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
	if mode != sandboxfs.LockUnlock {
		o.acquired++
	}
	acquired := o.acquired
	if wait {
		o.waiting++
		o.give()
	}
	_, err := callUntil(f, f.ctx, cancel, (*sandboxfs.Client).SetLock, q)
	if wait {
		o.take(nil)
		o.waiting--
	}
	defer o.give()
	if err == nil {
		return fuse.OK
	}
	if !noEffect(err) {
		if f.seams.undo != nil {
			f.seams.undo()
		}
		alone := o.waiting == 0 && o.acquired == acquired
		f.undoLock(h, q, answered(err) && alone)
	}
	var fail *sandboxfs.Failure
	if errors.Is(err, errInterrupted) || errors.As(err, &fail) && fail.Code == sandboxfs.CodeCancelled || interrupted(cancel) && !answered(err) {
		return fuse.EINTR
	}
	return status(err)
}

// undoLock unlocks the owner's range after a lock request that may have changed it failed. The unlock is safe only when the service runs it after that request and no other request of the owner may have locked in between, which safe says; otherwise, or when the unlock fails, the handle fails instead.
func (f *frontend) undoLock(h *handle, q *sandboxfs.SetLockRequest, safe bool) {
	if safe {
		unlock := *q
		unlock.Lock.Mode, unlock.Wait = sandboxfs.LockUnlock, false
		if _, err := call(f, f.ctx, (*sandboxfs.Client).SetLock, &unlock); err == nil {
			return
		}
	}
	h.failed.Store(true)
}

// answered reports whether a failure came in the service's response, so a later request on the handle runs after the failed one. After a stream or context failure the request may still be running.
func answered(err error) bool {
	return !errors.Is(err, sandboxfs.ErrTransport) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
