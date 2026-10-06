//go:build linux

package processshim

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// pipeBuf is PIPE_BUF: a write of at most this many bytes to a pipe that
// poll reports writable does not block.
const pipeBuf = 4096

const (
	ptySlaveMajor  = 136 // UNIX98_PTY_SLAVE_MAJOR
	ptySlaveMajors = 8   // UNIX98_PTY_MAJOR_COUNT
)

var errStopped = errors.New("stopped")

// stopFlag is a level-triggered flag that pollers include, with a channel
// that closes when it is set. Once set it stays set. Setting it after close
// does nothing, so a late set never writes to a reused descriptor number.
type stopFlag struct {
	fd int
	c  chan struct{}

	mu           sync.Mutex
	raised, done bool
}

func newStopFlag() (*stopFlag, error) {
	fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return nil, err
	}
	return &stopFlag{fd: fd, c: make(chan struct{})}, nil
}

func (s *stopFlag) set() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.raised {
		s.raised = true
		close(s.c)
		if !s.done {
			var one [8]byte
			binary.NativeEndian.PutUint64(one[:], 1)
			unix.Write(s.fd, one[:])
		}
	}
}

func (s *stopFlag) isSet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raised
}

// close closes the flag's descriptor once its pollers have returned.
func (s *stopFlag) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done {
		s.done = true
		unix.Close(s.fd)
	}
}

type ioKind uint8

const (
	ioOwn    ioKind = iota // the relay's own non-blocking description
	ioSocket               // a socket, used with MSG_DONTWAIT
	ioFile                 // a regular file or block device, used as it is
	ioShared               // another description, polled before each call
)

// endpoint is a received descriptor and the descriptor its I/O uses.
type endpoint struct {
	fd   int // the received descriptor
	io   int // fd, an own description, or -1 for a FIFO without a reader
	kind ioKind
}

// openEndpoint prepares received descriptor fd for reading or writing. A
// pipe, FIFO or pty slave is reopened through /proc/self/fd as the relay's
// own non-blocking description, so a poll that ending the invocation
// interrupts is the only wait on a peer; a socket gets MSG_DONTWAIT. A
// regular file or block device is used as it is. Any other descriptor, and
// one the relay may not reopen, is shared with the Harness: the relay polls
// it before each call and writes at most pipeBuf bytes at once. The flags of
// the shared description never change.
func openEndpoint(fd int, write bool) (endpoint, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return endpoint{}, err
	}
	shared := endpoint{fd: fd, io: fd, kind: ioShared}
	switch typ := st.Mode & unix.S_IFMT; typ {
	case unix.S_IFREG, unix.S_IFBLK:
		return endpoint{fd: fd, io: fd, kind: ioFile}, nil
	case unix.S_IFSOCK:
		return endpoint{fd: fd, io: fd, kind: ioSocket}, nil
	case unix.S_IFCHR, unix.S_IFIFO:
		if major := unix.Major(st.Rdev); typ == unix.S_IFCHR && (major < ptySlaveMajor || major >= ptySlaveMajor+ptySlaveMajors) {
			return shared, nil
		}
		mode, use := unix.O_RDONLY, "reading"
		if write {
			mode, use = unix.O_WRONLY, "writing"
		}
		// The own description gets no access the received one lacks.
		fl, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil {
			return endpoint{}, err
		}
		if acc := fl & unix.O_ACCMODE; fl&unix.O_PATH != 0 || acc != mode && acc != unix.O_RDWR {
			return endpoint{}, errors.New("not open for " + use)
		}
		io, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", fd), mode|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
		switch {
		case err == unix.ENXIO && write && typ == unix.S_IFIFO:
			return endpoint{fd: fd, io: -1, kind: ioOwn}, nil // no reader
		case err == unix.EACCES || err == unix.EPERM:
			return shared, nil
		case err != nil:
			return endpoint{}, fmt.Errorf("reopen: %w", err)
		}
		return endpoint{fd: fd, io: io, kind: ioOwn}, nil
	default:
		return endpoint{}, fmt.Errorf("file type %#o is not supported", typ)
	}
}

func (e endpoint) close() {
	if e.io >= 0 && e.io != e.fd {
		unix.Close(e.io)
	}
	unix.Close(e.fd)
}

func (e endpoint) read(buf []byte) (int, error) {
	if e.kind == ioSocket {
		n, _, errno := unix.Syscall6(unix.SYS_RECVFROM, uintptr(e.io), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), unix.MSG_DONTWAIT, 0, 0)
		if errno != 0 {
			return 0, errno
		}
		return int(n), nil
	}
	return unix.Read(e.io, buf)
}

// write writes once. Output on a Unix socket carries the relay's own
// credentials.
func (e endpoint) write(data []byte) (int, error) {
	switch e.kind {
	case ioSocket:
		return unix.SendmsgN(e.io, data, nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
	case ioShared:
		data = data[:min(len(data), pipeBuf)]
	}
	if e.io < 0 {
		return 0, unix.EPIPE
	}
	return unix.Write(e.io, data)
}

// readFD reads once. It returns 0 and nil at end of file.
func readFD(e endpoint, buf []byte, stop *stopFlag) (int, error) {
	for {
		if stop.isSet() {
			return 0, errStopped
		}
		if e.kind == ioShared {
			if err := waitFD(e.io, unix.POLLIN, stop); err != nil {
				return 0, err
			}
		}
		n, err := e.read(buf)
		switch err {
		case unix.EINTR:
			continue
		case unix.EAGAIN:
			if err := waitFD(e.io, unix.POLLIN, stop); err != nil {
				return 0, err
			}
			continue
		}
		return max(n, 0), err
	}
}

// writeFD writes all of data, handling short writes.
func writeFD(e endpoint, data []byte, stop *stopFlag) error {
	for len(data) > 0 {
		if stop.isSet() {
			return errStopped
		}
		if e.kind == ioShared {
			if err := waitFD(e.io, unix.POLLOUT, stop); err != nil {
				return err
			}
		}
		n, err := e.write(data)
		switch {
		case err == unix.EINTR:
			continue
		case err == unix.EAGAIN:
			if err := waitFD(e.io, unix.POLLOUT, stop); err != nil {
				return err
			}
			continue
		case err != nil:
			return err
		}
		data = data[n:]
	}
	return nil
}

// tryWrite writes data only as far as the endpoint takes it now.
func tryWrite(e endpoint, data []byte) {
	if e.kind == ioShared {
		p := []unix.PollFd{{Fd: int32(e.io), Events: unix.POLLOUT}}
		if n, err := unix.Poll(p, 0); n != 1 || err != nil || p[0].Revents&unix.POLLOUT == 0 {
			return
		}
	}
	e.write(data[:min(len(data), pipeBuf)])
}

// waitFD waits until fd has events or stop is set.
func waitFD(fd int, events int16, stop *stopFlag) error {
	pfds := []unix.PollFd{{Fd: int32(fd), Events: events}, {Fd: int32(stop.fd), Events: unix.POLLIN}}
	for {
		if _, err := unix.Poll(pfds, -1); err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		switch r := pfds[0].Revents; {
		case pfds[1].Revents != 0:
			return errStopped
		case r&unix.POLLNVAL != 0:
			return unix.EBADF
		case r != 0:
			return nil
		}
	}
}
