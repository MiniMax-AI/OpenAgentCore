//go:build linux

package worldfs

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func (f *frontend) OpenDir(_ <-chan struct{}, in *fuse.OpenIn, out *fuse.OpenOut) fuse.Status {
	n, st := f.node(in.NodeId)
	if !st.Ok() {
		return st
	}
	h := &handle{node: n}
	if !n.synthetic() {
		id := f.ids.Next()
		if _, err := call(f, f.ctx, (*sandboxfs.Client).OpenDir, &sandboxfs.OpenDirRequest{Handle: id, Node: n.ref}); err != nil {
			f.settle(cleanup{handle: id, dir: true}, err)
			return status(err)
		}
		h.server = id
	}
	*out = fuse.OpenOut{Fh: f.newHandle(h)}
	return fuse.OK
}

// ReadDir lists a directory without "." and "..". A plain sandbox directory uses the service's cookies as offsets, and a synthetic directory lists its children at offsets 1 to k. A directory with presented children is a [listing].
func (f *frontend) ReadDir(_ <-chan struct{}, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	h, st := f.handle(in.Fh)
	if !st.Ok() {
		return st
	}
	n := h.node
	switch {
	case h.server == 0:
		for off := in.Offset; off < uint64(len(n.order)); off++ {
			c := n.fixed[n.order[off]]
			if !out.AddDirEntry(fuse.DirEntry{Mode: c.fileType(), Name: n.order[off], Ino: f.ino(c), Off: off + 1}) {
				break
			}
		}
		return fuse.OK
	case len(n.order) == 0:
		return f.readPlain(h, in, out)
	}
	return f.readPresented(h, in, out)
}

func (f *frontend) readPlain(h *handle, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	r, err := call(f, f.ctx, (*sandboxfs.Client).ReadDir, &sandboxfs.ReadDirRequest{Handle: h.server, Cookie: in.Offset, Limit: min(in.Size, f.caps.MaxReadDirBytes)})
	switch {
	case err != nil:
		return status(err)
	case len(r.Entries) == 0 && !r.End:
		return fuse.EIO
	}
	for _, e := range r.Entries {
		if !out.AddDirEntry(fuse.DirEntry{Mode: e.Type, Name: string(e.Name), Ino: e.Ino, Off: e.Cookie}) {
			break
		}
	}
	return fuse.OK
}

func (f *frontend) ino(n *inode) uint64 {
	if n.synthetic() {
		return f.attrOf(n).Ino
	}
	return n.attr.Ino
}

func (f *frontend) ReleaseDir(in *fuse.ReleaseIn) {
	if h := f.dropHandle(in.Fh); h != nil && h.server != 0 && !f.closed.Load() {
		f.release(cleanup{handle: h.server, dir: true})
	}
}

// FsyncDir fails with EINVAL, as fsync(2) does on a directory a file system cannot sync, when the service does not declare DirectoryFsync.
func (f *frontend) FsyncDir(_ <-chan struct{}, in *fuse.FsyncIn) fuse.Status {
	h, st := f.handle(in.Fh)
	switch {
	case !st.Ok() || h.server == 0:
		return st
	case !f.caps.DirectoryFsync:
		return fuse.EINVAL
	}
	_, err := call(f, f.ctx, (*sandboxfs.Client).Fsync, &sandboxfs.FsyncRequest{Handle: h.server, DataOnly: in.FsyncFlags&1 != 0})
	return status(err)
}
