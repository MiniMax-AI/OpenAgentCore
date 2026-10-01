//go:build linux

package processshim

import (
	"errors"
	"fmt"
	"io"
	"net"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Conn is one end of an invocation connection.
type Conn struct {
	c *net.UnixConn
	// fds collects descriptors while the broker reads the Request; nil
	// otherwise, so any control data is a violation.
	fds *[]int
	// first is true until the first read returns.
	first bool
}

// NewConn wraps a connected Unix stream socket.
func NewConn(c *net.UnixConn) *Conn { return &Conn{c: c, first: true} }

// Close closes the socket.
func (c *Conn) Close() error { return c.c.Close() }

// Unix returns the socket.
func (c *Conn) Unix() *net.UnixConn { return c.c }

// SendRequest sends r with fds attached to its first byte.
func (c *Conn) SendRequest(r Request, fds [3]int) error {
	f := Frame(r)
	if len(f.Payload) > MaxFrameBytes {
		return fmt.Errorf("%w: request of %d bytes exceeds %d", ErrProtocol, len(f.Payload), MaxFrameBytes)
	}
	var b frameBuffer
	if err := sandboxwire.WriteFrame(&b, f); err != nil {
		return err
	}
	n, _, err := c.c.WriteMsgUnix(b, unix.UnixRights(fds[:]...), nil)
	if err != nil {
		return err
	}
	_, err = c.c.Write(b[n:])
	return err
}

// Send sends a message without descriptors.
func (c *Conn) Send(m Message) error { return sandboxwire.WriteFrame(c.c, Frame(m)) }

// ReadRequest reads the Request and the three descriptors attached to it. The
// caller owns the descriptors once it returns without error; on error none
// remain open. A Request with another Version returns with only its Version.
func (c *Conn) ReadRequest() (Request, [3]int, error) {
	var fds []int
	c.fds = &fds
	m, err := c.ReadMessage()
	c.fds = nil
	if err == nil && len(fds) != 3 {
		err = fmt.Errorf("%w: request carries %d descriptors", ErrProtocol, len(fds))
	}
	r, ok := m.(Request)
	if err == nil && !ok {
		err = fmt.Errorf("%w: first message is type %d", ErrProtocol, Frame(m).Type)
	}
	if err != nil {
		closeAll(fds)
		return Request{}, [3]int{-1, -1, -1}, err
	}
	return r, [3]int(fds), nil
}

// ReadMessage reads one message. Descriptors on any message other than the
// Request are a violation.
func (c *Conn) ReadMessage() (Message, error) {
	f, err := sandboxwire.ReadFrame(reader{c}, MaxFrameBytes)
	if err != nil {
		return nil, err
	}
	return Decode(f)
}

// reader reads the socket for ReadFrame and checks the control data that
// arrives with each read.
type reader struct{ *Conn }

func (c reader) Read(p []byte) (int, error) {
	oob := make([]byte, unix.CmsgSpace(3*4))
	n, oobn, flags, _, err := c.c.ReadMsgUnix(p, oob)
	first := c.first
	c.first = false
	if oobn == 0 && flags&unix.MSG_CTRUNC == 0 {
		if err == nil && n == 0 && len(p) > 0 {
			err = io.EOF
		}
		return n, err
	}
	fds, perr := parseRights(oob[:oobn])
	switch {
	case flags&unix.MSG_CTRUNC != 0:
		perr = errors.New("truncated control data")
	case perr == nil && (c.fds == nil || !first):
		perr = errors.New("unexpected descriptors")
	}
	if perr != nil {
		closeAll(fds)
		return 0, fmt.Errorf("%w: %w", ErrProtocol, perr)
	}
	*c.fds = fds
	return n, err
}

// parseRights returns the descriptors in one SCM_RIGHTS message. It returns
// every descriptor it found, even with an error, so the caller can close them.
func parseRights(oob []byte) ([]int, error) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, m := range msgs {
		if m.Header.Level != unix.SOL_SOCKET || m.Header.Type != unix.SCM_RIGHTS {
			err = fmt.Errorf("control message %d/%d", m.Header.Level, m.Header.Type)
			continue
		}
		got, perr := unix.ParseUnixRights(&m)
		fds = append(fds, got...)
		if perr != nil {
			err = perr
		}
	}
	if err == nil && len(msgs) != 1 {
		err = fmt.Errorf("%d control messages", len(msgs))
	}
	return fds, err
}

func closeAll(fds []int) {
	for _, fd := range fds {
		unix.Close(fd)
	}
}

type frameBuffer []byte

func (b *frameBuffer) Write(p []byte) (int, error) {
	*b = append(*b, p...)
	return len(p), nil
}
