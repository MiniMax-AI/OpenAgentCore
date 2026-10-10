//go:build linux

package worldfs

import (
	"fmt"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

func (f *frontend) String() string { return "oac-world" }

func (f *frontend) Lookup(_ <-chan struct{}, in *fuse.InHeader, name string, out *fuse.EntryOut) fuse.Status {
	p, st := f.node(in.NodeId)
	if !st.Ok() {
		return st
	}
	if c := p.fixed[name]; c != nil {
		if c.synthetic() {
			f.lookedUp(c)
			f.entry(c, f.attrOf(c), out)
			return fuse.OK
		}
		return f.lookupPinned(p, name, c, out)
	}
	if p.synthetic() {
		return fuse.ENOENT
	}
	if pf, ok := f.lookupScoped(in.Pid, p, name); ok {
		if pf.err != nil {
			return status(pf.err)
		}
		f.adopt(pf.entry, out)
		return fuse.OK
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).Lookup, &sandboxfs.LookupRequest{Parent: p.ref, Name: []byte(name)})
	if err != nil {
		return status(err)
	}
	f.adopt(r.Entry, out)
	return fuse.OK
}

// lookupPinned answers a pinned name with the pinned node. When the sandbox replaced or removed it, the view keeps the pinned node, since a new node ID would detach the mounts beneath it, and the world reports the change.
func (f *frontend) lookupPinned(p *inode, name string, c *inode, out *fuse.EntryOut) fuse.Status {
	r, err := call(f, f.ctx, (*sandboxfs.Client).Lookup, &sandboxfs.LookupRequest{Parent: p.ref, Name: []byte(name)})
	switch {
	case err == nil && r.Entry.Node == c.ref:
		f.adopt(r.Entry, out)
		return fuse.OK
	case err == nil:
		f.mu.Lock()
		f.unref(r.Entry.Node, 1)
		f.mu.Unlock()
	case !isErrno(err, sandboxfs.ErrnoNotFound):
		return status(err)
	}
	f.lose(fmt.Errorf("%w: lookup %s", ErrTopologyChanged, c.path), false)
	f.lookedUp(c)
	f.entry(c, c.attr, out)
	return fuse.OK
}

func (f *frontend) GetAttr(_ <-chan struct{}, in *fuse.GetAttrIn, out *fuse.AttrOut) fuse.Status {
	n, st := f.node(in.NodeId)
	if !st.Ok() {
		return st
	}
	switch {
	case n.synthetic():
		f.fill(n, f.attrOf(n), &out.Attr)
		return fuse.OK
	case n.link != nil:
		f.fill(n, n.attr, &out.Attr)
		return fuse.OK
	}
	t, st := f.target(n, in.Flags()&fuse.FUSE_GETATTR_FH != 0, in.Fh())
	if !st.Ok() {
		return st
	}
	if t.Kind == sandboxfs.TargetNode {
		if a, ok := f.attrScoped(in.Pid, n); ok {
			f.fill(n, a, &out.Attr)
			return fuse.OK
		}
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).GetAttr, &sandboxfs.GetAttrRequest{Target: t})
	if err != nil {
		return status(err)
	}
	f.fill(n, r.Attr, &out.Attr)
	return fuse.OK
}

// target addresses the open handle when the kernel names one, and the node otherwise. A failed handle fails the request.
func (f *frontend) target(n *inode, useFh bool, fh uint64) (sandboxfs.Target, fuse.Status) {
	if useFh {
		h, st := f.handle(fh)
		switch {
		case st == fuse.EIO:
			return sandboxfs.Target{}, st
		case st.Ok() && h.server != 0:
			return sandboxfs.Target{Kind: sandboxfs.TargetHandle, Handle: h.server}, fuse.OK
		}
	}
	return sandboxfs.Target{Kind: sandboxfs.TargetNode, Node: n.ref}, fuse.OK
}

func (f *frontend) SetAttr(_ <-chan struct{}, in *fuse.SetAttrIn, out *fuse.AttrOut) fuse.Status {
	n, st := f.node(in.NodeId)
	if !st.Ok() {
		return st
	}
	if n.synthetic() || n.link != nil {
		return fuse.EPERM
	}
	t, st := f.target(n, in.Valid&fuse.FATTR_FH != 0, in.Fh)
	if !st.Ok() {
		return st
	}
	q := &sandboxfs.SetAttrRequest{Target: t}
	if in.Valid&fuse.FATTR_MODE != 0 {
		q.Set |= sandboxfs.AttrMode
		q.Mode = in.Mode & sandboxfs.ModePerm
	}
	if in.Valid&fuse.FATTR_UID != 0 {
		q.Set |= sandboxfs.AttrUID
		q.UID = in.Uid
		if q.UID == f.view.UID {
			q.UID = f.service.UID
		}
	}
	if in.Valid&fuse.FATTR_GID != 0 {
		q.Set |= sandboxfs.AttrGID
		q.GID = in.Gid
		if q.GID == f.view.GID {
			q.GID = f.service.GID
		}
	}
	if in.Valid&fuse.FATTR_SIZE != 0 {
		q.Set |= sandboxfs.AttrSize
		q.Size = in.Size
	}
	// The kernel marks a time set to now with both bits.
	switch {
	case in.Valid&fuse.FATTR_ATIME_NOW != 0:
		q.Set |= sandboxfs.AttrAtimeNow
	case in.Valid&fuse.FATTR_ATIME != 0:
		q.Set |= sandboxfs.AttrAtime
		q.Atime = sandboxfs.Timestamp{Sec: int64(in.Atime), Nsec: in.Atimensec}
	}
	switch {
	case in.Valid&fuse.FATTR_MTIME_NOW != 0:
		q.Set |= sandboxfs.AttrMtimeNow
	case in.Valid&fuse.FATTR_MTIME != 0:
		q.Set |= sandboxfs.AttrMtime
		q.Mtime = sandboxfs.Timestamp{Sec: int64(in.Mtime), Nsec: in.Mtimensec}
	}
	switch {
	case q.Set&sandboxfs.AttrMode != 0 && !f.caps.SetMode,
		q.Set&(sandboxfs.AttrUID|sandboxfs.AttrGID) != 0 && !f.caps.SetOwner,
		q.Set&(sandboxfs.AttrAtime|sandboxfs.AttrMtime|sandboxfs.AttrAtimeNow|sandboxfs.AttrMtimeNow) != 0 && !f.caps.SetTimes:
		return fuse.EPERM
	case q.Set == 0:
		// Nothing the File protocol sets, such as a ctime-only update: report the current attributes.
		r, err := call(f, f.ctx, (*sandboxfs.Client).GetAttr, &sandboxfs.GetAttrRequest{Target: q.Target})
		if err != nil {
			return status(err)
		}
		f.fill(n, r.Attr, &out.Attr)
		return fuse.OK
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).SetAttr, q)
	if err != nil {
		return status(err)
	}
	f.fill(n, r.Attr, &out.Attr)
	return fuse.OK
}

func (f *frontend) Access(_ <-chan struct{}, in *fuse.AccessIn) fuse.Status {
	n, st := f.node(in.NodeId)
	if !st.Ok() {
		return st
	}
	mask := sandboxfs.AccessMask(in.Mask & 7)
	if n.synthetic() {
		if mask&sandboxfs.MayWrite != 0 || mask&sandboxfs.MayExecute != 0 && n.synth != sandboxfs.ModeDirectory {
			return fuse.EACCES
		}
		return fuse.OK
	}
	_, err := call(f, f.ctx, (*sandboxfs.Client).Access, &sandboxfs.AccessRequest{Node: n.ref, Mask: mask})
	return status(err)
}

func (f *frontend) Readlink(_ <-chan struct{}, in *fuse.InHeader) ([]byte, fuse.Status) {
	n, st := f.node(in.NodeId)
	switch {
	case !st.Ok():
		return nil, st
	case n.link != nil:
		return n.link, fuse.OK
	case n.synthetic():
		return nil, fuse.EINVAL
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).Readlink, &sandboxfs.ReadlinkRequest{Node: n.ref})
	if err != nil {
		return nil, status(err)
	}
	return r.Target, fuse.OK
}

// parent returns the directory a name is created in or removed from. Presented names and synthetic directories never change.
func (f *frontend) parent(id uint64, name string) (*inode, fuse.Status) {
	p, st := f.node(id)
	if !st.Ok() {
		return nil, st
	}
	if p.synthetic() || p.fixed[name] != nil {
		return nil, fuse.EPERM
	}
	return p, fuse.OK
}

func (f *frontend) Mkdir(_ <-chan struct{}, in *fuse.MkdirIn, name string, out *fuse.EntryOut) fuse.Status {
	p, st := f.parent(in.NodeId, name)
	if !st.Ok() {
		return st
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).Mkdir, &sandboxfs.MkdirRequest{Parent: p.ref, Name: []byte(name), Mode: in.Mode & sandboxfs.ModePerm})
	if err != nil {
		return status(err)
	}
	f.adopt(r.Entry, out)
	return fuse.OK
}

// Mknod has no File request. The kernel creates regular files with Create.
func (f *frontend) Mknod(<-chan struct{}, *fuse.MknodIn, string, *fuse.EntryOut) fuse.Status {
	return errno(syscall.EOPNOTSUPP)
}

func (f *frontend) Unlink(_ <-chan struct{}, in *fuse.InHeader, name string) fuse.Status {
	p, st := f.parent(in.NodeId, name)
	if !st.Ok() {
		return st
	}
	_, err := call(f, f.ctx, (*sandboxfs.Client).Unlink, &sandboxfs.UnlinkRequest{Parent: p.ref, Name: []byte(name)})
	return status(err)
}

func (f *frontend) Rmdir(_ <-chan struct{}, in *fuse.InHeader, name string) fuse.Status {
	p, st := f.parent(in.NodeId, name)
	if !st.Ok() {
		return st
	}
	_, err := call(f, f.ctx, (*sandboxfs.Client).Rmdir, &sandboxfs.RmdirRequest{Parent: p.ref, Name: []byte(name)})
	return status(err)
}

func (f *frontend) Rename(_ <-chan struct{}, in *fuse.RenameIn, oldName, newName string) fuse.Status {
	p, st := f.parent(in.NodeId, oldName)
	if !st.Ok() {
		return st
	}
	np, st := f.parent(in.Newdir, newName)
	if !st.Ok() {
		return st
	}
	var mode sandboxfs.RenameMode
	switch {
	case in.Flags == 0:
		mode = sandboxfs.RenameReplace
	case in.Flags == unix.RENAME_NOREPLACE && f.caps.RenameNoReplace:
		mode = sandboxfs.RenameNoReplace
	case in.Flags == unix.RENAME_EXCHANGE && f.caps.RenameExchange:
		mode = sandboxfs.RenameExchange
	default:
		return fuse.EINVAL
	}
	_, err := call(f, f.ctx, (*sandboxfs.Client).Rename, &sandboxfs.RenameRequest{Parent: p.ref, Name: []byte(oldName), NewParent: np.ref, NewName: []byte(newName), Mode: mode})
	return status(err)
}

func (f *frontend) Link(_ <-chan struct{}, in *fuse.LinkIn, name string, out *fuse.EntryOut) fuse.Status {
	n, st := f.node(in.Oldnodeid)
	if !st.Ok() {
		return st
	}
	np, st := f.parent(in.NodeId, name)
	if !st.Ok() {
		return st
	}
	if n.synthetic() || !f.caps.HardLinks {
		return fuse.EPERM
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).Link, &sandboxfs.LinkRequest{Node: n.ref, NewParent: np.ref, NewName: []byte(name)})
	if err != nil {
		return status(err)
	}
	f.adopt(r.Entry, out)
	return fuse.OK
}

func (f *frontend) Symlink(_ <-chan struct{}, in *fuse.InHeader, target, name string, out *fuse.EntryOut) fuse.Status {
	p, st := f.parent(in.NodeId, name)
	if !st.Ok() {
		return st
	}
	if !f.caps.Symlinks {
		return fuse.EPERM
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).Symlink, &sandboxfs.SymlinkRequest{Parent: p.ref, Name: []byte(name), Target: []byte(target)})
	if err != nil {
		return status(err)
	}
	f.adopt(r.Entry, out)
	return fuse.OK
}

func (f *frontend) StatFs(_ <-chan struct{}, in *fuse.InHeader, out *fuse.StatfsOut) fuse.Status {
	n, st := f.node(in.NodeId)
	if !st.Ok() {
		return st
	}
	ref := n.ref
	if n.synthetic() {
		ref = f.root.ref
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).StatFS, &sandboxfs.StatFSRequest{Node: ref})
	if err != nil {
		return status(err)
	}
	*out = fuse.StatfsOut{
		Blocks: r.Blocks, Bfree: r.BlocksFree, Bavail: r.BlocksAvailable, Files: r.Files, Ffree: r.FilesFree,
		Bsize: r.BlockSize, NameLen: r.NameMax, Frsize: r.FragmentSize,
	}
	return fuse.OK
}
