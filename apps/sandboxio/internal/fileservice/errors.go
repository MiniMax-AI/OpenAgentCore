//go:build linux

package fileservice

import (
	"errors"
	"os"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"golang.org/x/sys/unix"
)

var errnos = map[unix.Errno]sandboxfs.Errno{
	unix.EACCES:       sandboxfs.ErrnoPermissionDenied,
	unix.EPERM:        sandboxfs.ErrnoOperationNotPermitted,
	unix.ENOENT:       sandboxfs.ErrnoNotFound,
	unix.EEXIST:       sandboxfs.ErrnoExists,
	unix.ENOTDIR:      sandboxfs.ErrnoNotDirectory,
	unix.EISDIR:       sandboxfs.ErrnoIsDirectory,
	unix.ENOTEMPTY:    sandboxfs.ErrnoDirectoryNotEmpty,
	unix.EINVAL:       sandboxfs.ErrnoInvalidArgument,
	unix.EBADF:        sandboxfs.ErrnoBadDescriptor,
	unix.EMFILE:       sandboxfs.ErrnoTooManyOpenFiles,
	unix.ENFILE:       sandboxfs.ErrnoTooManyOpenFiles,
	unix.ENOSPC:       sandboxfs.ErrnoNoSpace,
	unix.EDQUOT:       sandboxfs.ErrnoQuotaExceeded,
	unix.EROFS:        sandboxfs.ErrnoReadOnlyFilesystem,
	unix.EXDEV:        sandboxfs.ErrnoCrossDevice,
	unix.ENAMETOOLONG: sandboxfs.ErrnoNameTooLong,
	unix.ELOOP:        sandboxfs.ErrnoSymlinkLoop,
	unix.EFBIG:        sandboxfs.ErrnoFileTooLarge,
	unix.EOVERFLOW:    sandboxfs.ErrnoOverflow,
	unix.EBUSY:        sandboxfs.ErrnoBusy,
	unix.ETXTBSY:      sandboxfs.ErrnoBusy,
	unix.EAGAIN:       sandboxfs.ErrnoAgain,
	unix.EINTR:        sandboxfs.ErrnoInterrupted,
	unix.EIO:          sandboxfs.ErrnoIO,
	unix.ENODEV:       sandboxfs.ErrnoNoDevice,
	unix.ENXIO:        sandboxfs.ErrnoNoSuchDeviceOrAddress,
	unix.EPIPE:        sandboxfs.ErrnoBrokenPipe,
	unix.EOPNOTSUPP:   sandboxfs.ErrnoNotSupported,
	unix.ENOSYS:       sandboxfs.ErrnoNotSupported,
	unix.ENOLCK:       sandboxfs.ErrnoNoLocks,
	unix.EDEADLK:      sandboxfs.ErrnoDeadlock,
}

// failure converts err to the *Failure a Service method returns. A Linux
// errno without an entry becomes IO. EffectPossible overrides the EffectNone
// of a typed failure, because a step before it may have changed state.
func failure(err error, effect sandboxwire.Effect) error {
	var f *sandboxfs.Failure
	if errors.As(err, &f) {
		if effect == sandboxwire.EffectPossible && f.Effect != effect {
			promoted := *f
			promoted.Effect = effect
			return &promoted
		}
		return f
	}
	var e unix.Errno
	if errors.As(err, &e) {
		errno, ok := errnos[e]
		if !ok {
			errno = sandboxfs.ErrnoIO
		}
		return sandboxfs.NewErrnoFailure(errno, effect, e.Error())
	}
	return sandboxfs.NewErrnoFailure(sandboxfs.ErrnoIO, effect, err.Error())
}

func unsupported(what string) error {
	return sandboxfs.NewFailure(sandboxfs.CodeUnsupported, sandboxwire.EffectNone, what+" is not supported")
}

func errnoFailure(errno sandboxfs.Errno, message string) error {
	return sandboxfs.NewErrnoFailure(errno, sandboxwire.EffectNone, message)
}

// use runs fn with f's descriptor. f cannot be closed while fn runs, so the
// descriptor number is never reused under fn. A closed f yields stale().
func use(f *os.File, stale func() error, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return stale()
	}
	var inner error
	if err := rc.Control(func(fd uintptr) { inner = fn(int(fd)) }); err != nil {
		return stale()
	}
	return inner
}

// viaProc runs fn with the /proc/self/fd directory and fd's name in it; fd
// is always a descriptor the service opened and holds. Resolving that name
// reaches the object fd refers to, even after it was renamed or unlinked,
// and stops at a symlink or proc magic link the object is.
func (s *Service) viaProc(fd int, fn func(dir int, name string) error) error {
	return use(s.proc, errStaleAttachment, func(dir int) error { return fn(dir, strconv.Itoa(fd)) })
}

// eintr retries a system call interrupted by a signal.
func eintr[T any](call func() (T, error)) (T, error) {
	for {
		v, err := call()
		if err != unix.EINTR {
			return v, err
		}
	}
}

func openat(dir int, name string, flags int, mode uint32) (int, error) {
	return eintr(func() (int, error) { return unix.Openat(dir, name, flags|unix.O_CLOEXEC, mode) })
}

func openFile(dir int, name string, flags int) (*os.File, error) {
	fd, err := openat(dir, name, flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
