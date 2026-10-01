//go:build linux

package fileservice

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"golang.org/x/sys/unix"
)

const (
	none     = sandboxwire.EffectNone
	possible = sandboxwire.EffectPossible
)

// lookup opens name in directory dir without following a symlink and
// acquires one reference on its node. Names never contain "/" and are never
// "." or "..", so a lookup resolves exactly one entry of dir, and a symlink,
// including a proc magic link, is returned as itself.
func (st *state) lookup(dir int, name []byte) (*node, sandboxfs.Entry, error) {
	fd, err := openat(dir, string(name), unix.O_PATH|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, sandboxfs.Entry{}, err
	}
	var sb unix.Stat_t
	if err := unix.Fstat(fd, &sb); err != nil {
		unix.Close(fd)
		return nil, sandboxfs.Entry{}, err
	}
	n, err := st.addNode(fd, &sb)
	if err != nil {
		return nil, sandboxfs.Entry{}, err
	}
	return n, sandboxfs.Entry{Node: n.ref, Attr: st.attr(&sb)}, nil
}

func (st *state) lookupIn(parent *node, name []byte) (n *node, e sandboxfs.Entry, err error) {
	err = use(parent.f, errStaleNode, func(dir int) error {
		n, e, err = st.lookup(dir, name)
		return err
	})
	return n, e, err
}

// withNode runs fn with the descriptor of the node ref names.
func (st *state) withNode(ref sandboxfs.NodeRef, fn func(n *node, fd int) error) error {
	n, err := st.node(ref)
	if err != nil {
		return err
	}
	return use(n.f, errStaleNode, func(fd int) error { return fn(n, fd) })
}

// dir returns the node ref names, which must be a directory. A symlink is
// never a directory, so no request resolves through one.
func (st *state) dir(ref sandboxfs.NodeRef) (*node, error) {
	n, err := st.node(ref)
	if err != nil {
		return nil, err
	}
	if n.typ != sandboxfs.ModeDirectory {
		return nil, failure(unix.ENOTDIR, none)
	}
	return n, nil
}

// withDir runs fn with the descriptor of the directory ref names.
func (st *state) withDir(ref sandboxfs.NodeRef, fn func(dir int) error) error {
	n, err := st.dir(ref)
	if err != nil {
		return err
	}
	return use(n.f, errStaleNode, fn)
}

func (s *Service) Lookup(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.LookupRequest) (*sandboxfs.LookupResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	parent, err := st.dir(r.Parent)
	if err != nil {
		return nil, err
	}
	_, e, err := st.lookupIn(parent, r.Name)
	if err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.LookupResponse{Entry: e}, nil
}

// Walk looks up each name in turn and stops after a symlink or at the first
// failure, which it reports after the entries already walked.
func (s *Service) Walk(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.WalkRequest) (*sandboxfs.WalkResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	cur, err := st.dir(r.Parent)
	if err != nil {
		return nil, err
	}
	resp := &sandboxfs.WalkResponse{}
	for _, name := range r.Names {
		n, e, err := st.lookupIn(cur, name)
		if err != nil {
			if len(resp.Entries) == 0 {
				return nil, failure(err, none)
			}
			resp.Failure = failure(err, none).(*sandboxfs.Failure)
			break
		}
		resp.Entries = append(resp.Entries, e)
		if n.typ == sandboxfs.ModeSymlink {
			break
		}
		cur = n
	}
	return resp, nil
}

// target resolves a node or handle Target to its descriptor holder. typ is
// the node's type, or zero for a handle.
func (st *state) target(t sandboxfs.Target) (f *os.File, typ uint32, stale func() error, err error) {
	if t.Kind == sandboxfs.TargetHandle {
		h, err := st.handle(t.Handle)
		if err != nil {
			return nil, 0, nil, err
		}
		return h.f, 0, errStaleHandle, nil
	}
	n, err := st.node(t.Node)
	if err != nil {
		return nil, 0, nil, err
	}
	return n.f, n.typ, errStaleNode, nil
}

func (s *Service) GetAttr(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.GetAttrRequest) (*sandboxfs.GetAttrResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	f, _, stale, err := st.target(r.Target)
	if err != nil {
		return nil, err
	}
	var sb unix.Stat_t
	if err := use(f, stale, func(fd int) error { return unix.Fstat(fd, &sb) }); err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.GetAttrResponse{Attr: st.attr(&sb)}, nil
}

// SetAttr applies the owner, then the mode, the size and the times. A
// failure after the first applied change carries EffectPossible.
func (s *Service) SetAttr(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.SetAttrRequest) (*sandboxfs.SetAttrResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	f, typ, stale, err := st.target(r.Target)
	if err != nil {
		return nil, err
	}
	isHandle := r.Target.Kind == sandboxfs.TargetHandle
	applied := false
	var sb unix.Stat_t
	err = use(f, stale, func(fd int) error {
		apply := func(err error) error {
			applied = applied || err == nil
			return err
		}
		if r.Set&(sandboxfs.AttrUID|sandboxfs.AttrGID) != 0 {
			uid, gid := -1, -1
			if r.Set&sandboxfs.AttrUID != 0 {
				uid = int(r.UID)
			}
			if r.Set&sandboxfs.AttrGID != 0 {
				gid = int(r.GID)
			}
			if err := apply(unix.Fchownat(fd, "", uid, gid, unix.AT_EMPTY_PATH)); err != nil {
				return err
			}
		}
		if r.Set&sandboxfs.AttrMode != 0 {
			var err error
			switch {
			case isHandle:
				err = unix.Fchmod(fd, r.Mode)
			case typ == sandboxfs.ModeSymlink:
				err = unix.EOPNOTSUPP
			default:
				err = s.viaProc(fd, func(dir int, name string) error { return unix.Fchmodat(dir, name, r.Mode, 0) })
			}
			if err := apply(err); err != nil {
				return err
			}
		}
		if r.Set&sandboxfs.AttrSize != 0 {
			if err := apply(s.truncate(fd, isHandle, typ, int64(r.Size))); err != nil {
				return err
			}
		}
		if r.Set&(sandboxfs.AttrAtime|sandboxfs.AttrMtime|sandboxfs.AttrAtimeNow|sandboxfs.AttrMtimeNow) != 0 {
			atime, aerr := timespec(r.Set, sandboxfs.AttrAtime, sandboxfs.AttrAtimeNow, r.Atime)
			mtime, merr := timespec(r.Set, sandboxfs.AttrMtime, sandboxfs.AttrMtimeNow, r.Mtime)
			if err := errors.Join(aerr, merr); err != nil {
				return err
			}
			err := s.viaProc(fd, func(dir int, name string) error {
				return unix.UtimesNanoAt(dir, name, []unix.Timespec{atime, mtime}, 0)
			})
			if err := apply(err); err != nil {
				return err
			}
		}
		return unix.Fstat(fd, &sb)
	})
	if err != nil {
		effect := none
		if applied {
			effect = possible
		}
		return nil, failure(err, effect)
	}
	return &sandboxfs.SetAttrResponse{Attr: st.attr(&sb)}, nil
}

func (s *Service) truncate(fd int, isHandle bool, typ uint32, size int64) error {
	switch {
	case isHandle:
		_, err := eintr(func() (struct{}, error) { return struct{}{}, unix.Ftruncate(fd, size) })
		return err
	case typ == sandboxfs.ModeDirectory:
		return unix.EISDIR
	case typ != sandboxfs.ModeRegular:
		return unix.EINVAL
	}
	w, err := s.reopen(fd, sandboxfs.ModeRegular, unix.O_WRONLY|unix.O_NOCTTY)
	if err != nil {
		return err
	}
	defer unix.Close(w)
	_, err = eintr(func() (struct{}, error) { return struct{}{}, unix.Ftruncate(w, size) })
	return err
}

func timespec(set, value, now sandboxfs.AttrMask, t sandboxfs.Timestamp) (unix.Timespec, error) {
	switch {
	case set&value != 0:
		return unix.TimeToTimespec(time.Unix(t.Sec, int64(t.Nsec)))
	case set&now != 0:
		return unix.Timespec{Nsec: unix.UTIME_NOW}, nil
	}
	return unix.Timespec{Nsec: unix.UTIME_OMIT}, nil
}

func (s *Service) Access(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.AccessRequest) (*sandboxfs.AccessResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	var mode uint32
	for bit, m := range map[sandboxfs.AccessMask]uint32{sandboxfs.MayRead: unix.R_OK, sandboxfs.MayWrite: unix.W_OK, sandboxfs.MayExecute: unix.X_OK} {
		if r.Mask&bit != 0 {
			mode |= m
		}
	}
	err = st.withNode(r.Node, func(_ *node, fd int) error {
		return s.viaProc(fd, func(dir int, name string) error { return unix.Faccessat(dir, name, mode, 0) })
	})
	if err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.AccessResponse{}, nil
}

// create runs make in the parent directory and then looks the new entry up.
// A failure after make succeeded carries EffectPossible.
func (st *state) create(parent sandboxfs.NodeRef, name []byte, make func(dir int) error) (sandboxfs.Entry, error) {
	var e sandboxfs.Entry
	made := false
	err := st.withDir(parent, func(dir int) (err error) {
		if err := make(dir); err != nil {
			return err
		}
		made = true
		_, e, err = st.lookup(dir, name)
		return err
	})
	if err != nil {
		effect := none
		if made {
			effect = possible
		}
		return e, failure(err, effect)
	}
	return e, nil
}

func (s *Service) Mkdir(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.MkdirRequest) (*sandboxfs.MkdirResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	e, err := st.create(r.Parent, r.Name, func(dir int) error { return unix.Mkdirat(dir, string(r.Name), r.Mode) })
	if err != nil {
		return nil, err
	}
	return &sandboxfs.MkdirResponse{Entry: e}, nil
}

func (s *Service) Symlink(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.SymlinkRequest) (*sandboxfs.SymlinkResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	e, err := st.create(r.Parent, r.Name, func(dir int) error { return unix.Symlinkat(string(r.Target), dir, string(r.Name)) })
	if err != nil {
		return nil, err
	}
	return &sandboxfs.SymlinkResponse{Entry: e}, nil
}

func (s *Service) Link(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.LinkRequest) (*sandboxfs.LinkResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	var e sandboxfs.Entry
	err = st.withNode(r.Node, func(_ *node, fd int) error {
		e, err = st.create(r.NewParent, r.NewName, func(dir int) error {
			return s.viaProc(fd, func(proc int, name string) error {
				return unix.Linkat(proc, name, dir, string(r.NewName), unix.AT_SYMLINK_FOLLOW)
			})
		})
		return err
	})
	if err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.LinkResponse{Entry: e}, nil
}

func (s *Service) Unlink(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.UnlinkRequest) (*sandboxfs.UnlinkResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	if err := st.withDir(r.Parent, func(dir int) error { return unix.Unlinkat(dir, string(r.Name), 0) }); err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.UnlinkResponse{}, nil
}

func (s *Service) Rmdir(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.RmdirRequest) (*sandboxfs.RmdirResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	if err := st.withDir(r.Parent, func(dir int) error { return unix.Unlinkat(dir, string(r.Name), unix.AT_REMOVEDIR) }); err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.RmdirResponse{}, nil
}

var renameFlags = map[sandboxfs.RenameMode]uint{
	sandboxfs.RenameReplace:   0,
	sandboxfs.RenameNoReplace: unix.RENAME_NOREPLACE,
	sandboxfs.RenameExchange:  unix.RENAME_EXCHANGE,
}

func (s *Service) Rename(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.RenameRequest) (*sandboxfs.RenameResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	err = st.withDir(r.Parent, func(dir int) error {
		return st.withDir(r.NewParent, func(newDir int) error {
			return unix.Renameat2(dir, string(r.Name), newDir, string(r.NewName), renameFlags[r.Mode])
		})
	})
	if err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.RenameResponse{}, nil
}

func (s *Service) Readlink(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.ReadlinkRequest) (*sandboxfs.ReadlinkResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, s.caps.MaxPathBytes+1)
	var n int
	err = st.withNode(r.Node, func(nd *node, fd int) (err error) {
		if nd.typ != sandboxfs.ModeSymlink {
			return unix.EINVAL // as readlink(2) answers for a non-symlink
		}
		n, err = unix.Readlinkat(fd, "", buf)
		return err
	})
	switch {
	case err != nil:
		return nil, failure(err, none)
	case n > int(s.caps.MaxPathBytes):
		return nil, errnoFailure(sandboxfs.ErrnoNameTooLong, "symlink target exceeds MaxPathBytes")
	}
	return &sandboxfs.ReadlinkResponse{Target: buf[:n]}, nil
}

func (s *Service) StatFS(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.StatFSRequest) (*sandboxfs.StatFSResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	var sf unix.Statfs_t
	if err := st.withNode(r.Node, func(_ *node, fd int) error { return unix.Fstatfs(fd, &sf) }); err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.StatFSResponse{
		Blocks: sf.Blocks, BlocksFree: sf.Bfree, BlocksAvailable: sf.Bavail, Files: sf.Files, FilesFree: sf.Ffree,
		BlockSize: uint32(sf.Bsize), FragmentSize: uint32(sf.Frsize), NameMax: uint32(sf.Namelen),
	}, nil
}

func (s *Service) Forget(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.ForgetRequest) (*sandboxfs.ForgetResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	if err := st.forget(r.Entries); err != nil {
		return nil, err
	}
	return &sandboxfs.ForgetResponse{}, nil
}
