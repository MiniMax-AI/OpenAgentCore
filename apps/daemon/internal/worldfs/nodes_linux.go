//go:build linux

package worldfs

import (
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// inode is a kernel node ID. Every ID is frontend-local: a server node gets one when first seen, and a synthetic node has no server node.
type inode struct {
	id    uint64
	ref   sandboxfs.NodeRef // zero for a synthetic node
	synth uint32            // a synthetic node's file type, else zero
	mount bool              // a synthetic mountpoint
	path  string            // a presented node's in-root path
	attr  sandboxfs.Attr    // a pinned node's attributes when it was pinned
	link  []byte            // a pinned symlink's target
	// fixed holds the presented children: synthetic nodes and the pinned entries on the way to a mountpoint. It never changes once Serve returns.
	fixed map[string]*inode
	order []string // fixed's names, sorted, in ReadDir order
	held  bool     // root, pinned or synthetic: kept for the view's lifetime

	lookups uint64 // the kernel's lookup count
	refs    uint64 // server references taken for kernel lookups
}

func (n *inode) synthetic() bool { return n.synth != 0 }

func (n *inode) fileType() uint32 {
	if n.synthetic() {
		return n.synth
	}
	return n.attr.Mode & sandboxfs.ModeType
}

// handle is an open file handle the kernel holds.
type handle struct {
	node   *inode
	server sandboxfs.HandleID // zero for a synthetic node

	failed atomic.Bool // the service may hold a lock on the handle that no request reported: every request but Release fails

	mu   sync.Mutex
	list listing // a presented directory's offsets
}

// newInode registers a node for ref, or a synthetic node when ref is zero. The caller holds mu, or Serve has not returned.
func (f *frontend) newInode(ref sandboxfs.NodeRef) *inode {
	f.lastID++
	n := &inode{id: f.lastID, ref: ref}
	f.nodes[n.id] = n
	if ref != (sandboxfs.NodeRef{}) {
		f.byRef[ref] = n
	}
	return n
}

// node returns the node the kernel names.
func (f *frontend) node(id uint64) (*inode, fuse.Status) {
	if f.dead.Load() || f.closed.Load() {
		return nil, fuse.EIO
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := f.nodes[id]; n != nil {
		return n, fuse.OK
	}
	return nil, fuse.Status(syscall.ESTALE)
}

// adopt records the lookup reference a response carried and answers the kernel with the entry.
func (f *frontend) adopt(e sandboxfs.Entry, out *fuse.EntryOut) *inode {
	f.mu.Lock()
	n := f.byRef[e.Node]
	if n == nil {
		n = f.newInode(e.Node)
	}
	n.lookups++
	n.refs++
	f.mu.Unlock()
	f.entry(n, e.Attr, out)
	return n
}

// lookedUp counts a kernel lookup answered without a server reference.
func (f *frontend) lookedUp(n *inode) {
	f.mu.Lock()
	n.lookups++
	f.mu.Unlock()
}

// entry fills an entry reply. Timeouts stay zero, so the kernel revalidates every name and attribute.
func (f *frontend) entry(n *inode, a sandboxfs.Attr, out *fuse.EntryOut) {
	*out = fuse.EntryOut{NodeId: n.id}
	f.fill(n, a, &out.Attr)
}

// attrOf returns a synthetic node's attributes: an empty read-only directory or file, root-owned.
func (f *frontend) attrOf(n *inode) sandboxfs.Attr {
	a := sandboxfs.Attr{Ino: 1<<63 | n.id, Mode: sandboxfs.ModeDirectory | 0o555, Nlink: 2, Blksize: 4096, Atime: f.born, Mtime: f.born, Ctime: f.born}
	if n.synth == sandboxfs.ModeRegular {
		a.Mode, a.Nlink = sandboxfs.ModeRegular|0o444, 1
	}
	return a
}

// fill converts attributes. Owners the service acts as appear as the view's identity; synthetic nodes stay root-owned.
func (f *frontend) fill(n *inode, a sandboxfs.Attr, out *fuse.Attr) {
	if !n.synthetic() {
		if a.UID == f.service.UID {
			a.UID = f.view.UID
		}
		if a.GID == f.service.GID {
			a.GID = f.view.GID
		}
	}
	*out = fuse.Attr{
		Ino: a.Ino, Size: a.Size, Blocks: a.Blocks,
		Atime: uint64(a.Atime.Sec), Mtime: uint64(a.Mtime.Sec), Ctime: uint64(a.Ctime.Sec),
		Atimensec: a.Atime.Nsec, Mtimensec: a.Mtime.Nsec, Ctimensec: a.Ctime.Nsec,
		Mode: a.Mode, Nlink: a.Nlink, Rdev: uint32(a.Rdev), Blksize: a.Blksize,
		Owner: fuse.Owner{Uid: a.UID, Gid: a.GID},
	}
}

// Forget releases kernel lookups. The server references they carried are queued for one Forget request.
func (f *frontend) Forget(id, count uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.nodes[id]
	if n == nil {
		return
	}
	n.lookups -= min(count, n.lookups)
	if r := min(count, n.refs); r > 0 {
		n.refs -= r
		f.unref(n.ref, r)
	}
	if n.lookups == 0 && !n.held {
		delete(f.nodes, id)
		delete(f.byRef, n.ref)
	}
}

// unref queues count server references of ref for a Forget request. The caller holds mu, or Serve has not returned.
func (f *frontend) unref(ref sandboxfs.NodeRef, count uint64) {
	f.forgets[ref] += count
	f.wake()
}

func (f *frontend) newHandle(h *handle) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastFh++
	f.handles[f.lastFh] = h
	return f.lastFh
}

func (f *frontend) handle(fh uint64) (*handle, fuse.Status) {
	if f.dead.Load() || f.closed.Load() {
		return nil, fuse.EIO
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch h := f.handles[fh]; {
	case h == nil:
		return nil, fuse.EBADF
	case h.failed.Load():
		return nil, fuse.EIO
	default:
		return h, fuse.OK
	}
}

func (f *frontend) dropHandle(fh uint64) *handle {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := f.handles[fh]
	delete(f.handles, fh)
	return h
}
