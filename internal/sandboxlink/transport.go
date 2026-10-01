package sandboxlink

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/gorilla/websocket"
	"github.com/libp2p/go-yamux/v5"
)

// Stream is an opened service stream. It keeps an orderly end distinct from
// an abort: after the other end's CloseWrite, Read returns io.EOF once every
// byte written before it has been read; after a Reset anywhere on the path,
// including the relay's for lease expiry, revocation or link loss, Read and
// Write return an error that is not io.EOF. One goroutine may read while
// another writes; concurrent reads, or concurrent writes, need the caller's
// own serialization.
type Stream interface {
	io.Reader
	io.Writer
	// CloseWrite ends the write direction in order.
	CloseWrite() error
	// Close ends the write direction in order and discards further input.
	Close() error
	// Reset aborts both directions.
	Reset() error
	// SetDeadline, SetReadDeadline and SetWriteDeadline bound how long
	// pending and future reads and writes wait, as yamux's do; a zero time
	// removes the bound. A call that would wait past the deadline fails with
	// an error whose Timeout is true and leaves the stream usable. A Read
	// still returns input already buffered after its deadline has passed.
	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

// Dialer opens the byte stream a peer runs yamux over. Production peers use
// DialWebSocket; tests may inject another transport.
type Dialer func(ctx context.Context) (net.Conn, error)

// maxWebSocketMessage bounds one WebSocket message. yamux writes each of its
// frames, at most a header and 64 KiB of data, as one message.
const maxWebSocketMessage = 1 << 20

// HandshakeTimeout bounds each handshake step a peer or the relay waits on:
// the WebSocket upgrade, a Hello, an Open or a Bind and its answer.
const HandshakeTimeout = 10 * time.Second

// DialWebSocket dials a relay URL that CheckRelayURL accepts and returns the
// WebSocket as a byte stream. Any other URL returns ErrRelayURL. A nil
// tlsConfig uses the system roots.
func DialWebSocket(ctx context.Context, rawURL string, tlsConfig *tls.Config) (net.Conn, error) {
	if err := CheckRelayURL(rawURL); err != nil {
		return nil, err
	}
	d := websocket.Dialer{TLSClientConfig: tlsConfig, HandshakeTimeout: HandshakeTimeout}
	ws, resp, err := d.DialContext(ctx, rawURL, nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("sandbox link: dial relay: %w", err)
	}
	return newWSConn(ws), nil
}

var upgrader = websocket.Upgrader{HandshakeTimeout: HandshakeTimeout}

// UpgradeWebSocket upgrades a relay request to a WebSocket byte stream. On
// failure the upgrader has already answered the request. The relay is served
// behind the installation's HTTPS ingress, so it accepts the request the
// ingress forwards; peers enforce TLS when they dial.
func UpgradeWebSocket(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	return newWSConn(ws), nil
}

// wsConn presents a WebSocket as a byte stream: each Write is one binary
// message and Read concatenates messages.
type wsConn struct {
	ws  *websocket.Conn
	r   io.Reader
	wmu sync.Mutex
}

func newWSConn(ws *websocket.Conn) *wsConn {
	ws.SetReadLimit(maxWebSocketMessage)
	return &wsConn{ws: ws}
}

func (c *wsConn) Read(p []byte) (int, error) {
	for {
		if c.r == nil {
			kind, r, err := c.ws.NextReader()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return 0, io.EOF
				}
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				return 0, errors.New("sandbox link: unexpected WebSocket text message")
			}
			c.r = r
		}
		n, err := c.r.Read(p)
		if err == io.EOF {
			c.r = nil
			if n == 0 {
				continue
			}
			err = nil
		}
		return n, err
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error                       { return c.ws.Close() }
func (c *wsConn) LocalAddr() net.Addr                { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr               { return c.ws.RemoteAddr() }
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }

func (c *wsConn) SetDeadline(t time.Time) error {
	return errors.Join(c.ws.SetReadDeadline(t), c.ws.SetWriteDeadline(t))
}

func muxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	return c
}

// ClientSession starts yamux on a peer's byte stream. The peer opens the
// control stream first.
func ClientSession(conn net.Conn) (*yamux.Session, error) {
	return yamux.Client(conn, muxConfig(), nil)
}

// ServerSession starts yamux on the relay's side of a byte stream. The relay
// buffers at most one fixed window per stream direction, so it never grows a
// stream's receive window.
func ServerSession(conn net.Conn) (*yamux.Session, error) {
	c := muxConfig()
	c.MaxStreamWindowSize = c.InitialStreamWindowSize
	return yamux.Server(conn, c, nil)
}

// connect dials, starts yamux, opens the control stream and exchanges the
// Hello, which takes the first ID of seq. A refused Hello returns the relay's
// *Error.
func connect(ctx context.Context, dial Dialer, hello Message, seq *sandboxwire.RequestSequence) (*yamux.Session, *yamux.Stream, HelloAccepted, error) {
	conn, err := dial(ctx)
	if err != nil {
		return nil, nil, HelloAccepted{}, err
	}
	sess, err := ClientSession(conn)
	if err != nil {
		conn.Close()
		return nil, nil, HelloAccepted{}, err
	}
	stop := context.AfterFunc(ctx, func() { sess.Close() })
	defer stop()
	ctl, err := sess.OpenStream(ctx)
	if err == nil {
		ctl.SetDeadline(time.Now().Add(HandshakeTimeout))
		err = WriteMessage(ctl, seq.Next(), hello)
	}
	var m Message
	if err == nil {
		_, m, err = ReadMessage(ctl, MaxMessageBytes)
	}
	if err == nil {
		switch r := m.(type) {
		case HelloAccepted:
			ctl.SetDeadline(time.Time{})
			return sess, ctl, r, nil
		case Failure:
			err = r.Err()
		default:
			err = Fail(ProtocolViolation)
		}
	}
	sess.Close()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return nil, nil, HelloAccepted{}, err
}

func dialerFor(dial Dialer, rawURL string, tlsConfig *tls.Config) Dialer {
	if dial != nil {
		return dial
	}
	return func(ctx context.Context) (net.Conn, error) { return DialWebSocket(ctx, rawURL, tlsConfig) }
}
