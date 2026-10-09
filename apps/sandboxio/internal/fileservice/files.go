//go:build linux

package fileservice

import (
	"context"
	"math"
	"os"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"golang.org/x/sys/unix"
)

// openFlags converts an access mode and open flags. The service never opens
// a controlling terminal and never follows a symlink it opens.
func openFlags(access sandboxfs.AccessMode, flags sandboxfs.OpenFlags) int {
	f := map[sandboxfs.AccessMode]int{sandboxfs.AccessRead: unix.O_RDONLY, sandboxfs.AccessWrite: unix.O_WRONLY, sandboxfs.AccessReadWrite: unix.O_RDWR}[access]
	f |= unix.O_NOCTTY
	for bit, o := range map[sandboxfs.OpenFlags]int{
		sandboxfs.OpenTruncate: unix.O_TRUNC, sandboxfs.OpenNoFollow: unix.O_NOFOLLOW,
		sandboxfs.OpenSync: unix.O_SYNC, sandboxfs.OpenDataSync: unix.O_DSYNC,
	} {
		if flags&bit != 0 {
			f |= o
		}
	}
	return f
}

// openable reports why a node of type typ cannot be opened as a file.
func openable(typ uint32) error {
	switch typ {
	case sandboxfs.ModeRegular:
		return nil
	case sandboxfs.ModeDirectory:
		return unix.EISDIR
	case sandboxfs.ModeSymlink:
		return unix.ELOOP
	}
	return unsupported("opening a special file")
}

// reopen opens the object of fd, a descriptor the service opened itself,
// through its /proc/self/fd name, after checking that the object has type
// typ. That name resolves to the object fd holds, so reopening never reaches
// a symlink's target.
func (s *Service) reopen(fd int, typ uint32, flags int) (fd2 int, err error) {
	var sb unix.Stat_t
	if err := unix.Fstat(fd, &sb); err != nil {
		return -1, err
	}
	switch got := sb.Mode & sandboxfs.ModeType; {
	case got == typ:
	case typ == sandboxfs.ModeDirectory:
		return -1, unix.ENOTDIR
	default:
		return -1, openable(got)
	}
	err = s.viaProc(fd, func(dir int, name string) (err error) {
		fd2, err = openat(dir, name, flags&^unix.O_NOFOLLOW, 0)
		return err
	})
	return fd2, err
}

func (s *Service) Open(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.OpenRequest) (*sandboxfs.OpenResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	n, err := st.node(r.Node)
	if err != nil {
		return nil, err
	}
	if err := openable(n.typ); err != nil {
		return nil, failure(err, none)
	}
	if err := st.reserve(r.Handle); err != nil {
		return nil, err
	}
	var fd int
	err = use(n.f, errStaleNode, func(pfd int) (err error) {
		fd, err = s.reopen(pfd, sandboxfs.ModeRegular, openFlags(r.Access, r.Flags))
		return err
	})
	if err != nil {
		st.unreserve(r.Handle)
		return nil, failure(err, none)
	}
	if err := st.publish(r.Handle, &handle{f: os.NewFile(uintptr(fd), "")}); err != nil {
		return nil, failure(err, possible)
	}
	return &sandboxfs.OpenResponse{}, nil
}

// Create creates the entry with O_EXCL first, so it never opens a special
// file or follows a symlink. Without Exclusive an existing regular file is
// opened instead.
func (s *Service) Create(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.CreateRequest) (*sandboxfs.CreateResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	parent, err := st.dir(r.Parent)
	if err != nil {
		return nil, err
	}
	if err := st.reserve(r.Handle); err != nil {
		return nil, err
	}
	flags := openFlags(r.Access, r.Flags) | unix.O_NOFOLLOW
	effect := none
	var fd, pathFD int
	var sb unix.Stat_t
	err = use(parent.f, errStaleNode, func(dir int) error {
		var err error
		for range 3 {
			fd, err = openat(dir, string(r.Name), flags|unix.O_CREAT|unix.O_EXCL, r.Mode)
			if err != unix.EEXIST || r.Exclusive {
				break
			}
			if fd, err = s.openExisting(dir, r.Name, flags); err != unix.ENOENT {
				break
			}
		}
		if err != nil {
			return err
		}
		effect = possible
		if pathFD, err = s.reopen(fd, sandboxfs.ModeRegular, unix.O_PATH); err == nil {
			if err = unix.Fstat(pathFD, &sb); err != nil {
				unix.Close(pathFD)
			}
		}
		if err != nil {
			unix.Close(fd)
		}
		return err
	})
	if err != nil {
		st.unreserve(r.Handle)
		return nil, failure(err, effect)
	}
	n, err := st.addNode(pathFD, &sb)
	if err != nil {
		st.unreserve(r.Handle)
		unix.Close(fd)
		return nil, failure(err, possible)
	}
	if err := st.publish(r.Handle, &handle{f: os.NewFile(uintptr(fd), "")}); err != nil {
		return nil, failure(err, possible)
	}
	return &sandboxfs.CreateResponse{Entry: sandboxfs.Entry{Node: n.ref, Attr: st.attr(&sb)}}, nil
}

// openExisting opens an existing regular file without following a symlink.
func (s *Service) openExisting(dir int, name []byte, flags int) (int, error) {
	pfd, err := openat(dir, string(name), unix.O_PATH|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(pfd)
	return s.reopen(pfd, sandboxfs.ModeRegular, flags)
}

// fileHandle returns a handle that must not be a directory handle.
func (st *state) fileHandle(id sandboxfs.HandleID, dirErr error) (*handle, error) {
	h, err := st.handle(id)
	if err != nil {
		return nil, err
	}
	if h.dir != nil {
		return nil, failure(dirErr, none)
	}
	return h, nil
}

func (s *Service) Read(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.ReadRequest) (*sandboxfs.ReadResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	h, err := st.fileHandle(r.Handle, unix.EISDIR)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, r.Size)
	total := 0
	err = h.use(func(fd int) error {
		for total < len(buf) {
			n, err := eintr(func() (int, error) { return unix.Pread(fd, buf[total:], int64(r.Offset)+int64(total)) })
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			total += n
		}
		return nil
	})
	if err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.ReadResponse{Data: buf[:total]}, nil
}

// Write writes at the offset, or appends atomically with one write on the
// descriptor with O_APPEND set. It reports the written prefix and the error
// that stopped it; a short append is reported as it is, never continued by
// another append.
func (s *Service) Write(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.WriteRequest) (*sandboxfs.WriteResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	h, err := st.fileHandle(r.Handle, unix.EISDIR)
	if err != nil {
		return nil, err
	}
	if h.tree {
		return nil, failure(unix.EROFS, none)
	}
	if !r.Append && r.Offset > math.MaxInt64-uint64(len(r.Data)) {
		return nil, sandboxfs.NewFailure(sandboxfs.CodeInvalidArgument, none, "write ends beyond 2^63-1")
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	total := 0
	err = h.use(func(fd int) error {
		if err := setAppend(fd, r.Append); err != nil {
			return err
		}
		if r.Append {
			n, err := eintr(func() (int, error) { return unix.Write(fd, r.Data) })
			total = max(n, 0)
			return err
		}
		for total < len(r.Data) {
			n, err := eintr(func() (int, error) { return unix.Pwrite(fd, r.Data[total:], int64(r.Offset)+int64(total)) })
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			total += n
		}
		return nil
	})
	switch {
	case err == nil:
		return &sandboxfs.WriteResponse{Written: uint32(total)}, nil
	case total == 0:
		return nil, failure(err, none)
	}
	return &sandboxfs.WriteResponse{Written: uint32(total), Failure: failure(err, none).(*sandboxfs.Failure)}, nil
}

// setAppend sets or clears O_APPEND on fd. An append uses the descriptor's
// flag rather than pwritev2's RWF_APPEND: overlayfs on some kernels drops
// RWF_APPEND and writes at the offset, but every file system honors the flag.
func setAppend(fd int, on bool) error {
	fl, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || (fl&unix.O_APPEND != 0) == on {
		return err
	}
	_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFL, fl^unix.O_APPEND)
	return err
}

// Flush closes a duplicate of the handle's descriptor, which is what a close
// of one descriptor does to the file.
func (s *Service) Flush(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.FlushRequest) (*sandboxfs.FlushResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	h, err := st.fileHandle(r.Handle, unix.EBADF)
	if err != nil {
		return nil, err
	}
	if h.tree {
		return &sandboxfs.FlushResponse{}, nil
	}
	effect := none
	err = h.use(func(fd int) error {
		dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			return err
		}
		effect = possible
		return unix.Close(dup)
	})
	if err != nil {
		return nil, failure(err, effect)
	}
	return &sandboxfs.FlushResponse{}, nil
}

func (s *Service) Fsync(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.FsyncRequest) (*sandboxfs.FsyncResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	h, err := st.handle(r.Handle)
	if err != nil {
		return nil, err
	}
	if h.tree {
		return nil, unsupported("tree result operation")
	}
	sync := unix.Fsync
	if r.DataOnly {
		sync = unix.Fdatasync
	}
	effect := none
	err = h.use(func(fd int) error {
		effect = possible
		_, err := eintr(func() (struct{}, error) { return struct{}{}, sync(fd) })
		return err
	})
	if err != nil {
		return nil, failure(err, effect)
	}
	return &sandboxfs.FsyncResponse{}, nil
}

func (s *Service) Release(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.ReleaseRequest) (*sandboxfs.ReleaseResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	if err := st.release(r.Handle, false); err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.ReleaseResponse{}, nil
}

func (s *Service) OpenDir(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.OpenDirRequest) (*sandboxfs.OpenDirResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	n, err := st.dir(r.Node)
	if err != nil {
		return nil, err
	}
	if err := st.reserve(r.Handle); err != nil {
		return nil, err
	}
	var fd int
	var sb unix.Stat_t
	err = use(n.f, errStaleNode, func(pfd int) (err error) {
		if err = unix.Fstat(pfd, &sb); err == nil {
			fd, err = s.reopen(pfd, sandboxfs.ModeDirectory, unix.O_RDONLY|unix.O_DIRECTORY)
		}
		return err
	})
	if err != nil {
		st.unreserve(r.Handle)
		return nil, failure(err, none)
	}
	if err := st.publish(r.Handle, &handle{f: os.NewFile(uintptr(fd), ""), dir: &cursor{dev: uint64(sb.Dev)}}); err != nil {
		return nil, err
	}
	return &sandboxfs.OpenDirResponse{}, nil
}

func (s *Service) ReadDir(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.ReadDirRequest) (*sandboxfs.ReadDirResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	h, err := st.handle(r.Handle)
	if err != nil {
		return nil, err
	}
	if h.tree {
		return nil, failure(unix.EBADF, none)
	}
	if h.dir == nil {
		return nil, failure(unix.ENOTDIR, none)
	}
	var resp *sandboxfs.ReadDirResponse
	err = h.use(func(fd int) (err error) {
		resp, err = h.dir.read(st, fd, r)
		return err
	})
	if err != nil {
		return nil, failure(err, none)
	}
	return resp, nil
}

func (s *Service) ReleaseDir(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.ReleaseDirRequest) (*sandboxfs.ReleaseDirResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	if err := st.release(r.Handle, true); err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.ReleaseDirResponse{}, nil
}

// GetLock queries POSIX locks, which this service does not declare.
func (s *Service) GetLock(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.GetLockRequest) (*sandboxfs.GetLockResponse, error) {
	if _, err := s.enter(a, r); err != nil {
		return nil, err
	}
	return nil, unsupported("POSIX locks")
}

// SetLock takes flock locks on the handle's open file description, the same
// lock native flock(2) takes, so both exclude each other. A waiting request
// polls, holding the handle's lock state only during each attempt, so other
// requests on the handle proceed and cancelling the context ends the wait.
func (s *Service) SetLock(ctx context.Context, a sandboxfs.Attachment, r *sandboxfs.SetLockRequest) (*sandboxfs.SetLockResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	h, err := st.handle(r.Handle)
	if err != nil {
		return nil, err
	}
	if h.tree {
		return nil, unsupported("tree result operation")
	}
	effect := none
	for delay := time.Millisecond; ; delay = min(2*delay, 50*time.Millisecond) {
		if err := ctx.Err(); err != nil {
			return nil, contextFailure(err, effect)
		}
		dropped, err := h.tryLock(r.Lock.Mode)
		if dropped {
			effect = possible
		}
		switch {
		case err == nil:
			return &sandboxfs.SetLockResponse{}, nil
		case err != unix.EWOULDBLOCK || !r.Wait:
			return nil, failure(err, effect)
		}
		wait := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			wait.Stop()
		case <-wait.C:
		}
	}
}

var flockOps = map[sandboxfs.LockMode]int{sandboxfs.LockRead: unix.LOCK_SH, sandboxfs.LockWrite: unix.LOCK_EX, sandboxfs.LockUnlock: unix.LOCK_UN}

// tryLock makes one non-blocking flock attempt. Converting a held lock drops
// it before taking the new one, so a failed conversion leaves the handle
// unlocked and reports dropped.
func (h *handle) tryLock(mode sandboxfs.LockMode) (dropped bool, err error) {
	h.lockMu.Lock()
	defer h.lockMu.Unlock()
	converting := h.flock != 0 && h.flock != mode && mode != sandboxfs.LockUnlock
	err = h.use(func(fd int) error { return unix.Flock(fd, flockOps[mode]|unix.LOCK_NB) })
	switch {
	case err == nil && mode == sandboxfs.LockUnlock:
		h.flock = 0
	case err == nil:
		h.flock = mode
	case converting:
		h.flock = 0
		return true, err
	}
	return false, err
}

func contextFailure(err error, effect sandboxwire.Effect) error {
	code := sandboxfs.CodeCancelled
	if err == context.DeadlineExceeded {
		code = sandboxfs.CodeDeadlineExceeded
	}
	return sandboxfs.NewFailure(code, effect, err.Error())
}
