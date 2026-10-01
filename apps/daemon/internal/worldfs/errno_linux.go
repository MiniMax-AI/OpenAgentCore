//go:build linux

package worldfs

import (
	"errors"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// errnos maps every sandboxfs errno to its Linux errno.
var errnos = map[sandboxfs.Errno]syscall.Errno{
	sandboxfs.ErrnoPermissionDenied:      syscall.EACCES,
	sandboxfs.ErrnoOperationNotPermitted: syscall.EPERM,
	sandboxfs.ErrnoNotFound:              syscall.ENOENT,
	sandboxfs.ErrnoExists:                syscall.EEXIST,
	sandboxfs.ErrnoNotDirectory:          syscall.ENOTDIR,
	sandboxfs.ErrnoIsDirectory:           syscall.EISDIR,
	sandboxfs.ErrnoDirectoryNotEmpty:     syscall.ENOTEMPTY,
	sandboxfs.ErrnoInvalidArgument:       syscall.EINVAL,
	sandboxfs.ErrnoBadDescriptor:         syscall.EBADF,
	sandboxfs.ErrnoTooManyOpenFiles:      syscall.EMFILE,
	sandboxfs.ErrnoNoSpace:               syscall.ENOSPC,
	sandboxfs.ErrnoQuotaExceeded:         syscall.EDQUOT,
	sandboxfs.ErrnoReadOnlyFilesystem:    syscall.EROFS,
	sandboxfs.ErrnoCrossDevice:           syscall.EXDEV,
	sandboxfs.ErrnoNameTooLong:           syscall.ENAMETOOLONG,
	sandboxfs.ErrnoSymlinkLoop:           syscall.ELOOP,
	sandboxfs.ErrnoFileTooLarge:          syscall.EFBIG,
	sandboxfs.ErrnoOverflow:              syscall.EOVERFLOW,
	sandboxfs.ErrnoBusy:                  syscall.EBUSY,
	sandboxfs.ErrnoAgain:                 syscall.EAGAIN,
	sandboxfs.ErrnoInterrupted:           syscall.EINTR,
	sandboxfs.ErrnoIO:                    syscall.EIO,
	sandboxfs.ErrnoNoDevice:              syscall.ENODEV,
	sandboxfs.ErrnoNoSuchDeviceOrAddress: syscall.ENXIO,
	sandboxfs.ErrnoBrokenPipe:            syscall.EPIPE,
	sandboxfs.ErrnoNotSupported:          syscall.EOPNOTSUPP,
	sandboxfs.ErrnoNoLocks:               syscall.ENOLCK,
	sandboxfs.ErrnoDeadlock:              syscall.EDEADLK,
}

// errnoOf returns the Linux errno for a failed request: the mapped errno, ESTALE for a stale node or handle, and EIO for anything else.
func errnoOf(err error) syscall.Errno {
	var fail *sandboxfs.Failure
	if !errors.As(err, &fail) {
		return syscall.EIO
	}
	switch fail.Code {
	case sandboxfs.CodeErrno:
		if e, ok := errnos[fail.Errno]; ok {
			return e
		}
	case sandboxfs.CodeStaleNode, sandboxfs.CodeStaleHandle:
		return syscall.ESTALE
	}
	return syscall.EIO
}

func status(err error) fuse.Status {
	if err == nil {
		return fuse.OK
	}
	return fuse.Status(errnoOf(err))
}

func errno(e syscall.Errno) fuse.Status { return fuse.Status(e) }
