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
		r, err := call(f, f.ctx, (*sandboxfs.Client).OpenDir, &sandboxfs.OpenDirRequest{Node: n.ref})
		if err != nil {
			return status(err)
		}
		h.server = r.Handle
	}
	*out = fuse.OpenOut{Fh: f.newHandle(h)}
	return fuse.OK
}

// ReadDir lists a directory without "." and "..". A plain sandbox directory uses the service's cookies as offsets. A directory with presented children lists those first, at offsets 1 to k, and then the sandbox entries it does not hide, at offsets k+1 onwards, remembering each one's cookie.
func (f *frontend) ReadDir(_ <-chan struct{}, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	h, st := f.handle(in.Fh)
	if !st.Ok() {
		return st
	}
	n := h.node
	k := uint64(len(n.order))
	if h.server != 0 && k == 0 {
		return f.readPlain(h, in, out)
	}
	off := in.Offset
	for ; off < k; off++ {
		c := n.fixed[n.order[off]]
		if !out.AddDirEntry(fuse.DirEntry{Mode: c.fileType(), Name: n.order[off], Ino: f.ino(c), Off: off + 1}) {
			return fuse.OK
		}
	}
	if h.server == 0 {
		return fuse.OK
	}
	return f.readPresented(h, off-k, in.Offset < k, in, out)
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

// readPresented lists sandbox entries from the j-th one not hidden. A hidden pinned entry whose type changed is reported as a topology change.
func (f *frontend) readPresented(h *handle, j uint64, listed bool, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	if j > uint64(len(h.cookies)) {
		return fuse.EINVAL
	}
	h.cookies = h.cookies[:j]
	var cookie uint64
	if j > 0 {
		cookie = h.cookies[j-1]
	}
	n, k := h.node, uint64(len(h.node.order))
	for {
		r, err := call(f, f.ctx, (*sandboxfs.Client).ReadDir, &sandboxfs.ReadDirRequest{Handle: h.server, Cookie: cookie, Limit: min(in.Size, f.caps.MaxReadDirBytes)})
		switch {
		case err != nil:
			return status(err)
		case len(r.Entries) == 0 && !r.End:
			return fuse.EIO
		}
		for _, e := range r.Entries {
			if c := n.fixed[string(e.Name)]; c != nil {
				if !c.synthetic() && e.Type != c.fileType() {
					f.lose(&Error{Kind: ErrTopologyChanged, Op: "readdir", Path: c.path}, false)
				}
				cookie = e.Cookie
				continue
			}
			if !out.AddDirEntry(fuse.DirEntry{Mode: e.Type, Name: string(e.Name), Ino: e.Ino, Off: k + uint64(len(h.cookies)) + 1}) {
				return fuse.OK
			}
			h.cookies = append(h.cookies, e.Cookie)
			cookie, listed = e.Cookie, true
		}
		if r.End || listed {
			return fuse.OK
		}
	}
}

func (f *frontend) ino(n *inode) uint64 {
	if n.synthetic() {
		return f.attrOf(n).Ino
	}
	return n.attr.Ino
}

func (f *frontend) ReleaseDir(in *fuse.ReleaseIn) {
	if h := f.dropHandle(in.Fh); h != nil && h.server != 0 && !f.closed.Load() {
		_, _ = call(f, f.ctx, (*sandboxfs.Client).ReleaseDir, &sandboxfs.ReleaseDirRequest{Handle: h.server})
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

// ReadDirPlus is never negotiated.
func (f *frontend) ReadDirPlus(<-chan struct{}, *fuse.ReadIn, *fuse.DirEntryList) fuse.Status {
	return fuse.ENOSYS
}
