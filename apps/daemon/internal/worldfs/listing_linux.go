//go:build linux

package worldfs

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// listing pages a sandbox directory that has presented children. Every sandbox entry is listed at its own position, a presented name with the presented node's type and inode number, and the presented names the sandbox does not list follow its last entry. An offset names the position after one listed entry. Offsets are assigned when an entry is first listed and kept until ReleaseDir, so a rewind or seekdir never invalidates one, and each READDIR sends at most one ReadDir.
type listing struct {
	at   []position          // at[o-1] is where offset o resumes
	offs map[position]uint64 // the offset of each position listed so far
	seen map[string]bool     // presented names the sandbox has listed
	pass *pass               // the enumeration since offset 0, while it skips nothing
}

// position is a place in a listing: after the sandbox entry whose cookie is cookie, or, once the sandbox listing ended, before the presented name order[from].
type position struct {
	cookie uint64
	tail   bool
	from   int
}

// pass is an enumeration from offset 0. It is complete when it reaches the end of the sandbox listing having resumed only at offsets it listed.
type pass struct {
	offs  map[uint64]bool // offsets listed in this pass
	names map[string]bool // presented names the sandbox listed in this pass
}

// readPresented lists from in.Offset. A pinned entry the sandbox lists with another type, or that a complete pass does not find, is reported as a topology change.
func (f *frontend) readPresented(h *handle, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, n := &h.list, h.node
	if l.offs == nil {
		l.offs, l.seen = map[position]uint64{}, map[string]bool{}
	}
	var p position
	switch {
	case in.Offset == 0:
		l.pass = &pass{offs: map[uint64]bool{}, names: map[string]bool{}}
	case in.Offset > uint64(len(l.at)):
		return fuse.EINVAL
	default:
		p = l.at[in.Offset-1]
		if l.pass != nil && !l.pass.offs[in.Offset] {
			l.pass = nil
		}
	}
	if !p.tail {
		r, err := call(f, f.ctx, (*sandboxfs.Client).ReadDir, &sandboxfs.ReadDirRequest{Handle: h.server, Cookie: p.cookie, Limit: min(in.Size, f.caps.MaxReadDirBytes)})
		switch {
		case err != nil:
			return status(err)
		case len(r.Entries) == 0 && !r.End:
			return fuse.EIO
		}
		for _, e := range r.Entries {
			name := string(e.Name)
			ent := fuse.DirEntry{Mode: e.Type, Name: name, Ino: e.Ino}
			c := n.fixed[name]
			if c != nil {
				if !c.synthetic() && e.Type != c.fileType() {
					f.lose(&Error{Kind: ErrTopologyChanged, Op: "readdir", Path: c.path}, false)
				}
				ent.Mode, ent.Ino = c.fileType(), f.ino(c)
			}
			if !l.add(out, ent, position{cookie: e.Cookie}) {
				return fuse.OK
			}
			if c != nil {
				l.seen[name] = true
				if l.pass != nil {
					l.pass.names[name] = true
				}
			}
		}
		if !r.End {
			return fuse.OK
		}
		if l.pass != nil {
			for _, name := range n.order {
				if c := n.fixed[name]; !c.synthetic() && !l.pass.names[name] {
					f.lose(&Error{Kind: ErrTopologyChanged, Op: "readdir", Path: c.path}, false)
				}
			}
		}
		p = position{tail: true}
	}
	listed := l.seen
	if l.pass != nil {
		listed = l.pass.names
	}
	for i := p.from; i < len(n.order); i++ {
		name := n.order[i]
		if listed[name] {
			continue
		}
		c := n.fixed[name]
		if !l.add(out, fuse.DirEntry{Mode: c.fileType(), Name: name, Ino: f.ino(c)}, position{tail: true, from: i + 1}) {
			break
		}
	}
	return fuse.OK
}

// add lists e, after which the listing resumes at p.
func (l *listing) add(out *fuse.DirEntryList, e fuse.DirEntry, p position) bool {
	off, ok := l.offs[p]
	if !ok {
		l.at = append(l.at, p)
		off = uint64(len(l.at))
		l.offs[p] = off
	}
	e.Off = off
	if !out.AddDirEntry(e) {
		return false
	}
	if l.pass != nil {
		l.pass.offs[off] = true
	}
	return true
}
