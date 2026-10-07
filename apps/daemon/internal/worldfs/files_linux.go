//go:build linux

package worldfs

import (
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
	}{{syscall.O_TRUNC, sandboxfs.OpenTruncate}, {syscall.O_NOFOLLOW, sandboxfs.OpenNoFollow}} {
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
		id := f.ids.Next()
		if _, err := call(f, f.ctx, (*sandboxfs.Client).Open, &sandboxfs.OpenRequest{Handle: id, Node: n.ref, Access: acc, Flags: of}); err != nil {
			f.settle(cleanup{handle: id}, err)
			return status(err)
		}
		h.server = id
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
	id := f.ids.Next()
	r, err := call(f, f.ctx, (*sandboxfs.Client).Create, &sandboxfs.CreateRequest{
		Handle: id, Parent: p.ref, Name: []byte(name), Mode: in.Mode & sandboxfs.ModePerm, Access: acc, Flags: of, Exclusive: in.Flags&syscall.O_EXCL != 0,
	})
	if err != nil {
		f.settle(cleanup{handle: id}, err)
		return status(err)
	}
	n := f.adopt(r.Entry, &out.EntryOut)
	out.OpenOut = fuse.OpenOut{Fh: f.newHandle(&handle{node: n, server: id}), OpenFlags: fuse.FOPEN_DIRECT_IO}
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

// Write appends when the write's flags hold O_APPEND, which fcntl(F_SETFL) may have set or cleared since the open. It reports a short write when the service stopped after a prefix.
func (f *frontend) Write(_ <-chan struct{}, in *fuse.WriteIn, data []byte) (uint32, fuse.Status) {
	h, st := f.handle(in.Fh)
	if !st.Ok() {
		return 0, st
	}
	if h.server == 0 {
		return 0, fuse.EBADF
	}
	data = data[:min(uint32(len(data)), f.caps.MaxWriteBytes)]
	r, err := call(f, f.ctx, (*sandboxfs.Client).Write, &sandboxfs.WriteRequest{Handle: h.server, Offset: in.Offset, Append: in.Flags&syscall.O_APPEND != 0, Data: data})
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

// Release releases a failed handle too, which drops any lock the service holds on it.
func (f *frontend) Release(_ <-chan struct{}, in *fuse.ReleaseIn) {
	if h := f.dropHandle(in.Fh); h != nil && h.server != 0 && !f.closed.Load() {
		f.release(cleanup{handle: h.server})
	}
}

// Ioctl has no File request: no world file takes ioctls.
func (f *frontend) Ioctl(<-chan struct{}, *fuse.IoctlIn, []byte, *fuse.IoctlOut, []byte) fuse.Status {
	return errno(syscall.ENOTTY)
}
