//go:build linux

package processbroker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// pipeBuf is PIPE_BUF, the most a short status message writes at once.
const pipeBuf = 4096

// Character devices. The memory devices never block, so the broker uses
// their passed descriptions as they are; a terminal is accepted only as a
// pty slave of the view's devpts instance.
var memoryDevices = []uint64{
	unix.Mkdev(1, 3), // null
	unix.Mkdev(1, 5), // zero
	unix.Mkdev(1, 7), // full
	unix.Mkdev(1, 8), // random
	unix.Mkdev(1, 9), // urandom
}

const (
	ptySlaveMajor  = 136 // UNIX98_PTY_SLAVE_MAJOR
	ptySlaveMajors = 8   // UNIX98_PTY_MAJOR_COUNT
)

// brokerPID names the broker as the sender of socket output. In the view's
// PID namespace it is no process: the receiver sees pid 0.
var brokerPID = int32(unix.Getpid())

var errStopped = errors.New("stopped")

// stopFlag is a level-triggered flag that pollers include. Once set it stays
// set. Setting it after close does nothing, so a late set never writes to a
// reused descriptor number.
type stopFlag struct {
	fd int

	mu           sync.Mutex
	raised, done bool
}

func newStopFlag() (*stopFlag, error) {
	fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return nil, err
	}
	return &stopFlag{fd: fd}, nil
}

func (s *stopFlag) set() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.raised && !s.done {
		s.raised = true
		var one [8]byte
		binary.NativeEndian.PutUint64(one[:], 1)
		unix.Write(s.fd, one[:])
	}
}

func (s *stopFlag) isSet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raised
}

// close closes the flag once its pollers have returned.
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
	ioReopened ioKind = iota // pipe, FIFO or view terminal
	ioSocket                 // a socket of another family than AF_UNIX
	ioUnix                   // an AF_UNIX socket
	ioFile                   // regular file, block device or memory device
)

// endpoint is a passed descriptor and the descriptor its I/O uses.
type endpoint struct {
	fd     int // the passed descriptor; -1 once closed
	io     int // fd, an independent description, or -1 for a pipe without a reader
	kind   ioKind
	sender *sender // for ioUnix
}

var closedEndpoint = endpoint{fd: -1, io: -1}

// sender is the identity that output on an AF_UNIX socket carries as
// SCM_CREDENTIALS, so a receiver with SO_PASSCRED never sees the broker's
// root credentials: the shim's pid, uid and gid while the shim runs, then
// the broker's pid with the shim's uid and gid.
type sender struct {
	pid      atomic.Int32
	uid, gid uint32
}

// shimGone stops naming the shim, whose pid may be reused.
func (s *sender) shimGone() { s.pid.Store(brokerPID) }

func (s *sender) send(fd int, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil // an empty sendmsg with control data sends a byte
	}
	for {
		pid := s.pid.Load()
		oob := unix.UnixCredentials(&unix.Ucred{Pid: pid, Uid: s.uid, Gid: s.gid})
		n, err := unix.SendmsgN(fd, data, oob, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		if err == unix.ESRCH && pid != brokerPID {
			s.shimGone() // the shim exited before the broker saw it go
			continue
		}
		return n, err
	}
}

// openEndpoint prepares passed descriptor fd for reading or writing, as the
// package documentation describes. pts is the view's devpts device.
func openEndpoint(fd int, write bool, pts uint64, snd *sender) (endpoint, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return closedEndpoint, err
	}
	switch typ := st.Mode & unix.S_IFMT; typ {
	case unix.S_IFREG, unix.S_IFBLK:
		return endpoint{fd: fd, io: fd, kind: ioFile}, nil
	case unix.S_IFSOCK:
		domain, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_DOMAIN)
		if err != nil {
			return closedEndpoint, err
		}
		if domain == unix.AF_UNIX {
			return endpoint{fd: fd, io: fd, kind: ioUnix, sender: snd}, nil
		}
		return endpoint{fd: fd, io: fd, kind: ioSocket}, nil
	case unix.S_IFCHR, unix.S_IFIFO:
		if typ == unix.S_IFCHR {
			if slices.Contains(memoryDevices, st.Rdev) {
				return endpoint{fd: fd, io: fd, kind: ioFile}, nil
			}
			if major := unix.Major(st.Rdev); major < ptySlaveMajor || major >= ptySlaveMajor+ptySlaveMajors || st.Dev != pts {
				return closedEndpoint, fmt.Errorf("character device %d:%d is neither a terminal of the view nor a memory device", unix.Major(st.Rdev), unix.Minor(st.Rdev))
			}
		}
		mode, use := unix.O_RDONLY, "reading"
		if write {
			mode, use = unix.O_WRONLY, "writing"
		}
		// The new description must not grant access the passed one lacks.
		fl, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil {
			return closedEndpoint, err
		}
		if acc := fl & unix.O_ACCMODE; fl&unix.O_PATH != 0 || acc != mode && acc != unix.O_RDWR {
			return closedEndpoint, errors.New("not open for " + use)
		}
		io, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", fd), mode|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
		if err == unix.ENXIO && write && typ == unix.S_IFIFO {
			return endpoint{fd: fd, io: -1, kind: ioReopened}, nil // no reader
		}
		if err != nil {
			return closedEndpoint, fmt.Errorf("reopen: %w", err)
		}
		return endpoint{fd: fd, io: io, kind: ioReopened}, nil
	default:
		return closedEndpoint, fmt.Errorf("file type %#o is not supported", typ)
	}
}

// closeIO closes the independent description, leaving the passed descriptor.
func (e endpoint) closeIO() {
	if e.io >= 0 && e.io != e.fd {
		unix.Close(e.io)
	}
}

func (e endpoint) close() {
	e.closeIO()
	if e.fd >= 0 {
		unix.Close(e.fd)
	}
}

func (e endpoint) read(buf []byte) (int, error) {
	if e.kind == ioSocket || e.kind == ioUnix {
		n, _, errno := unix.Syscall6(unix.SYS_RECVFROM, uintptr(e.io), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), unix.MSG_DONTWAIT, 0, 0)
		if errno != 0 {
			return 0, errno
		}
		return int(n), nil
	}
	return unix.Read(e.io, buf)
}

func (e endpoint) write(data []byte) (int, error) {
	switch {
	case e.io < 0:
		return 0, unix.EPIPE
	case e.kind == ioSocket:
		return unix.SendmsgN(e.io, data, nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
	case e.kind == ioUnix:
		return e.sender.send(e.io, data)
	}
	return unix.Write(e.io, data)
}

// readFD reads once. It returns 0 and nil at end of file.
func readFD(e endpoint, buf []byte, stops ...*stopFlag) (int, error) {
	for {
		if stopped(stops) {
			return 0, errStopped
		}
		n, err := e.read(buf)
		switch err {
		case unix.EINTR:
			continue
		case unix.EAGAIN:
			if err := waitFD(e.io, unix.POLLIN, stops...); err != nil {
				return 0, err
			}
			continue
		}
		return max(n, 0), err
	}
}

// writeFD writes all of data, handling short writes.
func writeFD(e endpoint, data []byte, stops ...*stopFlag) error {
	for len(data) > 0 {
		if stopped(stops) {
			return errStopped
		}
		n, err := e.write(data)
		switch {
		case err == unix.EINTR:
			continue
		case err == unix.EAGAIN:
			if err := waitFD(e.io, unix.POLLOUT, stops...); err != nil {
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
	e.write(data[:min(len(data), pipeBuf)])
}

func stopped(stops []*stopFlag) bool {
	for _, s := range stops {
		if s.isSet() {
			return true
		}
	}
	return false
}

// waitFD waits until fd has events or a stop flag is set.
func waitFD(fd int, events int16, stops ...*stopFlag) error {
	pfds := []unix.PollFd{{Fd: int32(fd), Events: events}}
	for _, s := range stops {
		pfds = append(pfds, unix.PollFd{Fd: int32(s.fd), Events: unix.POLLIN})
	}
	for {
		if _, err := unix.Poll(pfds, -1); err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		for _, p := range pfds[1:] {
			if p.Revents != 0 {
				return errStopped
			}
		}
		switch r := pfds[0].Revents; {
		case r&unix.POLLNVAL != 0:
			return unix.EBADF
		case r != 0:
			return nil
		}
	}
}
