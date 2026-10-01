//go:build linux

package worldfs

import (
	"context"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// openFlags maps open(2) flags. ok is false for an invalid access mode.
func openFlags(flags uint32) (acc sandboxfs.AccessMode, of sandboxfs.OpenFlags, ok bool) {
	switch flags & syscall.O_ACCMODE {
	case syscall.O_RDONLY:
		acc = sandboxfs.AccessRead
	case syscall.O_WRONLY:
		acc = sandboxfs.AccessWrite
	case syscall.O_RDWR:
		acc = sandboxfs.AccessReadWrite
	default:
		return 0, 0, false
	}
	for _, m := range []struct {
		bit  uint32
		flag sandboxfs.OpenFlags
	}{{syscall.O_APPEND, sandboxfs.OpenAppend}, {syscall.O_TRUNC, sandboxfs.OpenTruncate}, {syscall.O_NOFOLLOW, sandboxfs.OpenNoFollow}} {
		if flags&m.bit != 0 {
			of |= m.flag
		}
	}
	switch {
	case flags&syscall.O_SYNC == syscall.O_SYNC:
		of |= sandboxfs.OpenSync
	case flags&syscall.O_DSYNC != 0:
		of |= sandboxfs.OpenDataSync
	}
	return acc, of, true
}

func (f *frontend) Open(_ <-chan struct{}, in *fuse.OpenIn, out *fuse.OpenOut) fuse.Status {
	n, st := f.node(in.NodeId)
	if !st.Ok() {
		return st
	}
	acc, of, ok := openFlags(in.Flags)
	if !ok {
		return fuse.EINVAL
	}
	h := &handle{node: n}
	if n.synthetic() {
		if acc.Writes() || of&sandboxfs.OpenTruncate != 0 {
			return fuse.EPERM
		}
	} else {
		r, err := call(f, f.ctx, (*sandboxfs.Client).Open, &sandboxfs.OpenRequest{Node: n.ref, Access: acc, Flags: of})
		if err != nil {
			return status(err)
		}
		h.server = r.Handle
	}
	*out = fuse.OpenOut{Fh: f.newHandle(h), OpenFlags: fuse.FOPEN_DIRECT_IO}
	return fuse.OK
}

func (f *frontend) Create(_ <-chan struct{}, in *fuse.CreateIn, name string, out *fuse.CreateOut) fuse.Status {
	p, st := f.parent(in.NodeId, name)
	if !st.Ok() {
		return st
	}
	acc, of, ok := openFlags(in.Flags)
	if !ok {
		return fuse.EINVAL
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).Create, &sandboxfs.CreateRequest{
		Parent: p.ref, Name: []byte(name), Mode: in.Mode & sandboxfs.ModePerm, Access: acc, Flags: of, Exclusive: in.Flags&syscall.O_EXCL != 0,
	})
	if err != nil {
		return status(err)
	}
	n := f.adopt(r.Entry, &out.EntryOut)
	out.OpenOut = fuse.OpenOut{Fh: f.newHandle(&handle{node: n, server: r.Handle}), OpenFlags: fuse.FOPEN_DIRECT_IO}
	return fuse.OK
}

func (f *frontend) Read(_ <-chan struct{}, in *fuse.ReadIn, _ []byte) (fuse.ReadResult, fuse.Status) {
	h, st := f.handle(in.Fh)
	if !st.Ok() {
		return nil, st
	}
	if h.server == 0 {
		return fuse.ReadResultData(nil), fuse.OK
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).Read, &sandboxfs.ReadRequest{Handle: h.server, Offset: in.Offset, Size: min(in.Size, f.caps.MaxReadBytes)})
	if err != nil {
		return nil, status(err)
	}
	return fuse.ReadResultData(r.Data), fuse.OK
}

// Write reports a short write when the service stopped after a prefix.
func (f *frontend) Write(_ <-chan struct{}, in *fuse.WriteIn, data []byte) (uint32, fuse.Status) {
	h, st := f.handle(in.Fh)
	if !st.Ok() {
		return 0, st
	}
	if h.server == 0 {
		return 0, fuse.EBADF
	}
	data = data[:min(uint32(len(data)), f.caps.MaxWriteBytes)]
	r, err := call(f, f.ctx, (*sandboxfs.Client).Write, &sandboxfs.WriteRequest{Handle: h.server, Offset: in.Offset, Data: data})
	if err != nil {
		return 0, status(err)
	}
	return r.Written, fuse.OK
}

func (f *frontend) Flush(_ <-chan struct{}, in *fuse.FlushIn) fuse.Status {
	h, st := f.handle(in.Fh)
	if !st.Ok() || h.server == 0 {
		return st
	}
	_, err := call(f, f.ctx, (*sandboxfs.Client).Flush, &sandboxfs.FlushRequest{Handle: h.server, Owner: sandboxfs.LockOwner(in.LockOwner)})
	return status(err)
}

func (f *frontend) Fsync(_ <-chan struct{}, in *fuse.FsyncIn) fuse.Status {
	h, st := f.handle(in.Fh)
	if !st.Ok() || h.server == 0 {
		return st
	}
	_, err := call(f, f.ctx, (*sandboxfs.Client).Fsync, &sandboxfs.FsyncRequest{Handle: h.server, DataOnly: in.FsyncFlags&1 != 0})
	return status(err)
}

func (f *frontend) Release(_ <-chan struct{}, in *fuse.ReleaseIn) {
	if h := f.dropHandle(in.Fh); h != nil && h.server != 0 && !f.closed.Load() {
		_, _ = call(f, f.ctx, (*sandboxfs.Client).Release, &sandboxfs.ReleaseRequest{Handle: h.server})
	}
}

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

// setLock acquires or releases a POSIX or flock lock. A waiting request ends with EINTR when the kernel interrupts it.
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
	ctx := f.ctx
	if wait {
		var stop context.CancelFunc
		ctx, stop = context.WithCancel(f.ctx)
		defer stop()
		go func() {
			select {
			case <-cancel:
				stop()
			case <-ctx.Done():
			}
		}()
	}
	_, err := call(f, ctx, (*sandboxfs.Client).SetLock, &sandboxfs.SetLockRequest{
		Handle: h.server, Kind: kind, Owner: sandboxfs.LockOwner(in.Owner), Lock: sandboxfs.Lock{Mode: mode, Start: in.Lk.Start, End: in.Lk.End}, Wait: wait,
	})
	if wait && ctx.Err() != nil && f.ctx.Err() == nil {
		return fuse.EINTR
	}
	return status(err)
}

// Lseek has no File request. ENOSYS makes the kernel seek itself; SEEK_DATA and SEEK_HOLE then treat the file as one data extent.
func (f *frontend) Lseek(<-chan struct{}, *fuse.LseekIn, *fuse.LseekOut) fuse.Status {
	return fuse.ENOSYS
}

// Fallocate has no File request. ENOSYS makes the kernel stop asking and answer EOPNOTSUPP itself.
func (f *frontend) Fallocate(<-chan struct{}, *fuse.FallocateIn) fuse.Status {
	return fuse.ENOSYS
}

// CopyFileRange has no File request. ENOSYS makes the kernel copy with Read and Write.
func (f *frontend) CopyFileRange(<-chan struct{}, *fuse.CopyFileRangeIn) (uint32, fuse.Status) {
	return 0, fuse.ENOSYS
}

// Ioctl has no File request: no world file takes ioctls.
func (f *frontend) Ioctl(<-chan struct{}, *fuse.IoctlIn, []byte, *fuse.IoctlOut, []byte) fuse.Status {
	return errno(syscall.ENOTTY)
}
