package gateway

import (
	"net"
	"sync"
)

// conn is a connection the gateway accepts or makes: a TCP connection, or a
// connection through the sandbox. CloseWrite ends its write side in order and
// Reset aborts both sides.
type conn interface {
	net.Conn
	CloseWrite() error
	Reset() error
}

// tcpConn is a TCP connection whose Reset sends a TCP reset.
type tcpConn struct{ *net.TCPConn }

func (c tcpConn) Reset() error {
	c.SetLinger(0)
	return c.Close()
}

// connSet holds the gateway's open connections in both directions, hijacked,
// upgraded, tunnelled and HTTP/2 ones included, which net/http neither tracks
// nor closes while they are in use, so that closeAll ends every one.
type connSet struct {
	mu     sync.Mutex
	open   map[*trackedConn]struct{}
	closed bool
}

// add records c, or resets it and returns net.ErrClosed once the set is
// closed.
func (s *connSet) add(c conn) (conn, error) {
	t := &trackedConn{conn: c, set: s}
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.open[t] = struct{}{}
	}
	s.mu.Unlock()
	if closed {
		c.Reset()
		return nil, net.ErrClosed
	}
	return t, nil
}

// closeAll closes the set and resets each connection in it. It returns once
// every one has closed, those already closing included.
func (s *connSet) closeAll() {
	s.mu.Lock()
	open := s.open
	s.open, s.closed = nil, true
	s.mu.Unlock()
	for c := range open {
		c.Reset()
	}
}

// trackedConn is a connection in a connSet. It leaves the set once it has
// closed, and a concurrent Close or Reset returns only then.
type trackedConn struct {
	conn
	set  *connSet
	once sync.Once
}

func (c *trackedConn) Close() error { return c.end(c.conn.Close) }

func (c *trackedConn) Reset() error { return c.end(c.conn.Reset) }

func (c *trackedConn) end(close func() error) error {
	err := net.ErrClosed
	c.once.Do(func() {
		err = close()
		c.set.mu.Lock()
		delete(c.set.open, c)
		c.set.mu.Unlock()
	})
	return err
}

// sessionListener records each connection it accepts in conns.
type sessionListener struct {
	*net.TCPListener
	conns *connSet
}

func (l sessionListener) Accept() (net.Conn, error) {
	tc, err := l.AcceptTCP()
	if err != nil {
		return nil, err
	}
	return l.conns.add(tcpConn{tc})
}
