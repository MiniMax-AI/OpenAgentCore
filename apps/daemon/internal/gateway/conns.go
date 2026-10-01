package gateway

import (
	"net"
	"sync"
)

// sessionConns are the Harness's open connections to the gateway, hijacked
// and upgraded ones included. http.Server.Close does not close a hijacked
// connection, so the end of the Session aborts them here.
type sessionConns struct {
	mu    sync.Mutex
	open  map[*sessionConn]struct{}
	ended bool
}

// abort resets every open connection and refuses later ones.
func (s *sessionConns) abort() {
	s.mu.Lock()
	open := s.open
	s.open, s.ended = nil, true
	s.mu.Unlock()
	for c := range open {
		c.SetLinger(0)
		c.TCPConn.Close()
	}
}

// sessionListener records each connection it accepts in conns.
type sessionListener struct {
	*net.TCPListener
	conns *sessionConns
}

func (l sessionListener) Accept() (net.Conn, error) {
	tc, err := l.AcceptTCP()
	if err != nil {
		return nil, err
	}
	c := &sessionConn{TCPConn: tc, conns: l.conns}
	l.conns.mu.Lock()
	ended := l.conns.ended
	if !ended {
		l.conns.open[c] = struct{}{}
	}
	l.conns.mu.Unlock()
	if ended {
		tc.SetLinger(0)
		tc.Close()
		return nil, net.ErrClosed
	}
	return c, nil
}

// sessionConn is an accepted connection that leaves conns when it closes.
type sessionConn struct {
	*net.TCPConn
	conns *sessionConns
}

func (c *sessionConn) Close() error {
	c.conns.mu.Lock()
	delete(c.conns.open, c)
	c.conns.mu.Unlock()
	return c.TCPConn.Close()
}
