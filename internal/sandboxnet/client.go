package sandboxnet

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Connect asks the network service on s, a freshly opened Network stream, to
// connect to host:port within timeout, and returns the connection. Connect
// owns s: on success the Conn owns it, and on failure Connect has ended it.
//
// A failure is an *Error. A Connect that cannot be sent, or whose ctx ended
// before it was sent, fails with EffectNone. Once it may have been sent, a
// lost answer, a broken one or the end of ctx fails with EffectPossible: the
// sandbox may have connected. Connect never retries; a caller that retries an
// uncertain Connect opens a second connection.
func Connect(ctx context.Context, s sandboxlink.Stream, host string, port uint16, timeout time.Duration) (*Conn, error) {
	var seq sandboxwire.RequestSequence
	id := seq.Next()
	f, err := Encode(id, ConnectRequest{Network: NetworkTCP, Host: host, Port: port, TimeoutMillis: millis(timeout)})
	if err != nil {
		s.Reset()
		return nil, &Error{Code: CodeInvalidArgument, Effect: sandboxwire.EffectNone, Cause: err}
	}
	if err := ctx.Err(); err != nil {
		s.Reset()
		return nil, &Error{Code: contextCode(err), Effect: sandboxwire.EffectNone, Cause: err}
	}
	stop := context.AfterFunc(ctx, func() { s.Reset() })
	resp, err := exchange(s, f)
	if !stop() {
		return nil, &Error{Code: contextCode(ctx.Err()), Effect: sandboxwire.EffectPossible, Cause: ctx.Err()}
	}
	if err != nil {
		s.Reset()
		return nil, err
	}
	if err := resp.Err(); err != nil {
		s.Close()
		return nil, err
	}
	return &Conn{s: s, remote: Addr{Host: host, Port: port}}, nil
}

// exchange writes the Connect frame and reads its answer.
func exchange(s sandboxlink.Stream, f sandboxwire.Frame) (ConnectResponse, error) {
	if err := sandboxwire.WriteFrame(s, f); err != nil {
		return ConnectResponse{}, &Error{Code: CodeIO, Effect: sandboxwire.EffectPossible, Cause: err}
	}
	answer, err := sandboxwire.ReadFrame(s, MaxMessageBytes)
	if err != nil {
		if errors.Is(err, sandboxwire.ErrMalformed) {
			return ConnectResponse{}, &Error{Code: CodeUnknown, Effect: sandboxwire.EffectPossible, Cause: err}
		}
		return ConnectResponse{}, &Error{Code: CodeIO, Effect: sandboxwire.EffectPossible, Cause: err}
	}
	m, err := Decode(answer)
	if err == nil && (answer.Type != ConnectResponseTag || answer.RequestID != f.RequestID) {
		err = invalid("answer type %#04x request ID %d to request %d", answer.Type, answer.RequestID, f.RequestID)
	}
	if err != nil {
		return ConnectResponse{}, &Error{Code: CodeUnknown, Effect: sandboxwire.EffectPossible, Cause: err}
	}
	return m.(ConnectResponse), nil
}

// millis rounds timeout up to whole milliseconds. A timeout out of range
// yields a value the request validator rejects.
func millis(timeout time.Duration) uint32 {
	if timeout <= 0 {
		return 0
	}
	if timeout > MaxTimeoutMillis*time.Millisecond {
		return MaxTimeoutMillis + 1
	}
	return uint32((timeout + time.Millisecond - 1) / time.Millisecond)
}

func contextCode(err error) Code {
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeTimedOut
	}
	return CodeCancelled
}

// Conn is a connection the sandbox made. Read returns io.EOF only after the
// destination ended its write side and every byte before that arrived; an
// abort anywhere on the path, such as a reset from the destination or the end
// of the attachment, is an error that is not io.EOF. Its methods may be called
// concurrently, as net.Conn's may.
type Conn struct {
	s      sandboxlink.Stream
	remote Addr
	// rmu and wmu serialize reads and writes, which the stream does not.
	rmu, wmu sync.Mutex
	// rdl and wdl hold the deadlines, so a passed one fails a call even when
	// the stream could complete it from its buffer.
	rdl, wdl atomic.Pointer[time.Time]
	closed   atomic.Bool
	failed   atomic.Bool // a Read or Write failed for a reason other than a deadline
	eof      atomic.Bool // Read returned io.EOF
}

var _ net.Conn = (*Conn)(nil)

func (c *Conn) Read(b []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	if passed(&c.rdl) {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.s.Read(b)
	if err == io.EOF {
		c.eof.Store(true)
		return n, err
	}
	return n, c.note(err)
}

func (c *Conn) Write(b []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	if passed(&c.wdl) {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.s.Write(b)
	return n, c.note(err)
}

// note records a failure. A passed deadline is not one: it returns
// os.ErrDeadlineExceeded, as net.Conn requires, and the Conn stays usable.
func (c *Conn) note(err error) error {
	if err == nil {
		return nil
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return os.ErrDeadlineExceeded
	}
	c.failed.Store(true)
	return err
}

// CloseWrite ends the write direction in order: the destination reads EOF
// after every byte written before it. Reading carries on.
func (c *Conn) CloseWrite() error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	return c.s.CloseWrite()
}

// Close ends the connection. After Read returned io.EOF and with no failed
// Read or Write, it ends the write direction in order. Otherwise it aborts the
// connection like Reset, because input may remain unread.
func (c *Conn) Close() error {
	if c.closed.Swap(true) {
		return net.ErrClosed
	}
	if c.eof.Load() && !c.failed.Load() {
		return c.s.Close()
	}
	return c.s.Reset()
}

// Reset aborts both directions: the sandbox resets the destination
// connection.
func (c *Conn) Reset() error {
	if c.closed.Swap(true) {
		return net.ErrClosed
	}
	return c.s.Reset()
}

// LocalAddr returns the zero Addr: the protocol does not report the sandbox's
// local endpoint.
func (c *Conn) LocalAddr() net.Addr { return Addr{} }

// RemoteAddr returns the host and port the Conn was asked to reach.
func (c *Conn) RemoteAddr() net.Addr { return c.remote }

// SetDeadline, SetReadDeadline and SetWriteDeadline follow net.Conn: once a
// deadline has passed, Read or Write fails with os.ErrDeadlineExceeded until
// the deadline is extended, and a zero time removes it.
func (c *Conn) SetDeadline(t time.Time) error {
	c.rdl.Store(&t)
	c.wdl.Store(&t)
	return c.s.SetDeadline(t)
}

func (c *Conn) SetReadDeadline(t time.Time) error {
	c.rdl.Store(&t)
	return c.s.SetReadDeadline(t)
}

func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.wdl.Store(&t)
	return c.s.SetWriteDeadline(t)
}

// passed reports whether the deadline in d is set and has passed.
func passed(d *atomic.Pointer[time.Time]) bool {
	t := d.Load()
	return t != nil && !t.IsZero() && !time.Now().Before(*t)
}

// Addr is a destination as the Connect named it.
type Addr struct {
	Host string
	Port uint16
}

func (Addr) Network() string { return "tcp" }

func (a Addr) String() string { return net.JoinHostPort(a.Host, strconv.Itoa(int(a.Port))) }
