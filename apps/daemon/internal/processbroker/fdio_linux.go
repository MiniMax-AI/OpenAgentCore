//go:build linux

package processbroker

import (
	"encoding/binary"
	"errors"
	"sync"

	"golang.org/x/sys/unix"
)

// The passed descriptors share open file descriptions with the Harness, so
// their flags never change. Each read or write polls first, together with
// the stop flags that end it, and then uses the descriptor as it is, blocking
// or not.

// pipeBuf is PIPE_BUF: a pipe that polls writable takes this much without
// blocking.
const pipeBuf = 4096

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

// close closes the flag once its pollers have returned.
func (s *stopFlag) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done {
		s.done = true
		unix.Close(s.fd)
	}
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

// readFD reads once from fd. It returns 0 and nil at end of file.
func readFD(fd int, buf []byte, stops ...*stopFlag) (int, error) {
	for {
		if err := waitFD(fd, unix.POLLIN, stops...); err != nil {
			return 0, err
		}
		n, err := unix.Read(fd, buf)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		return max(n, 0), err
	}
}

// writeFD writes all of data to fd, handling short writes.
func writeFD(fd int, data []byte, stops ...*stopFlag) error {
	for len(data) > 0 {
		if err := waitFD(fd, unix.POLLOUT, stops...); err != nil {
			return err
		}
		n, err := unix.Write(fd, data[:min(len(data), pipeBuf)])
		switch {
		case err == unix.EAGAIN || err == unix.EINTR:
			continue
		case err != nil:
			return err
		}
		data = data[n:]
	}
	return nil
}

// tryWrite writes data to fd only if it takes it now.
func tryWrite(fd int, data []byte) {
	pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
	if n, err := unix.Poll(pfd, 0); err == nil && n == 1 && pfd[0].Revents == unix.POLLOUT {
		unix.Write(fd, data[:min(len(data), pipeBuf)])
	}
}
