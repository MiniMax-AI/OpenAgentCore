// Package sandboxnet is the Network protocol. On a Link stream of
// sandboxlink.ServiceNetwork, the agent-host Runtime asks the sandbox network
// service to connect to a TCP destination; once the service answers Connected,
// the stream carries the connection's raw bytes in both directions. Name
// resolution, the egress check and the dial happen in the sandbox, under the
// egress the Link bound to the stream.
//
// This file is the protocol's one authored definition: its vocabulary, message
// tags and payload layouts, validators, the egress rule and the Service a
// network service implements. The framing and primitive encoding come from
// sandboxwire. The protocol document is docs/sandbox-network-protocol.md.
package sandboxnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Version is the Network protocol version. It is matched exactly, through the
// Link's service version.
const Version uint16 = 1

// OpConnect is the only request: it opens the stream's one connection.
const OpConnect uint16 = 1

// ConnectResponseTag is the tag of the answer to OpConnect, OpConnect|0x8000.
const ConnectResponseTag uint16 = 0x8001

var tags = sandboxwire.Tags{Requests: OpConnect}

const (
	// MaxHostBytes bounds a Connect host.
	MaxHostBytes = 253
	// MaxTimeoutMillis bounds a Connect timeout.
	MaxTimeoutMillis = 60_000
	// MaxMessageBytes bounds every Network message payload: a Connect with
	// the longest host.
	MaxMessageBytes = 2 + 4 + MaxHostBytes + 2 + 4
)

// ErrProtocolViolation ends a stream whose peer broke the protocol: a frame
// that is not a Connect, a request ID that does not increase, a second
// Connect, or bytes that arrive before the dial succeeds.
var ErrProtocolViolation = errors.New("sandbox network: protocol violation")

// Network is the transport a Connect asks for.
type Network uint16

const NetworkTCP Network = 1

func (n Network) Valid() bool { return n == NetworkTCP }

// Result says whether a Connect connected.
type Result uint16

const (
	ResultConnected Result = 1
	ResultFailed    Result = 2
)

func (r Result) Valid() bool { return r == ResultConnected || r == ResultFailed }

// Code is the typed outcome of a failed Connect. It is also an error value, so
// errors.Is(err, sandboxnet.CodeDenied) matches an *Error with that code.
type Code uint16

const (
	// CodeInvalidArgument: the Connect is malformed or its host, port or
	// timeout is out of range.
	CodeInvalidArgument Code = iota + 1
	// CodeUnsupportedNetwork: the service does not serve the Connect's
	// network.
	CodeUnsupportedNetwork
	// CodeDenied: the binding's egress permits neither the port nor any
	// address of the host, or the sandbox refused the connection.
	CodeDenied
	// CodeNameNotResolved: the host has no address in the sandbox.
	CodeNameNotResolved
	// CodeNameResolutionFailed: resolution failed for another reason.
	CodeNameResolutionFailed
	// CodeConnectionRefused: the destination refused the connection.
	CodeConnectionRefused
	// CodeUnreachable: the sandbox has no route to the destination.
	CodeUnreachable
	// CodeTimedOut: resolution and dialing did not finish within the timeout.
	CodeTimedOut
	// CodeResourceExhausted: the sandbox ran out of sockets, ports or memory.
	CodeResourceExhausted
	// CodeCancelled: the caller or the attachment gave up.
	CodeCancelled
	// CodeIO: the stream failed before an answer arrived.
	CodeIO
	// CodeUnknown: any other failure, including an answer that breaks the
	// protocol.
	CodeUnknown
)

var codeNames = [...]string{"", "invalid argument", "unsupported network", "denied", "name not resolved",
	"name resolution failed", "connection refused", "unreachable", "timed out", "resource exhausted",
	"cancelled", "io", "unknown"}

func (c Code) Valid() bool { return c >= CodeInvalidArgument && c <= CodeUnknown }

func (c Code) String() string {
	if c.Valid() {
		return codeNames[c]
	}
	return fmt.Sprintf("code(%d)", uint16(c))
}

func (c Code) Error() string { return "sandbox network: " + c.String() }

// Error is a failed Connect with its effect: the service's answer or a local
// failure of the exchange. Cause is the local error that produced it; it is
// never sent.
type Error struct {
	Code   Code
	Effect sandboxwire.Effect
	Cause  error
}

func (e *Error) Error() string {
	effect := "no effect"
	if e.Effect == sandboxwire.EffectPossible {
		effect = "effect possible"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s (%s): %v", e.Code.Error(), effect, e.Cause)
	}
	return fmt.Sprintf("%s (%s)", e.Code.Error(), effect)
}

func (e *Error) Unwrap() error { return e.Cause }

// Is matches a Code target.
func (e *Error) Is(target error) bool {
	c, ok := target.(Code)
	return ok && c == e.Code
}

// ConnectRequest asks the service to connect to Host:Port. Host is an ASCII
// DNS name in A-label form or an unbracketed IP literal without a zone.
// TimeoutMillis bounds resolution plus dialing.
type ConnectRequest struct {
	Network       Network
	Host          string
	Port          uint16
	TimeoutMillis uint32
}

// ConnectResponse answers a ConnectRequest. Code and Effect are set exactly
// when Result is ResultFailed.
type ConnectResponse struct {
	Result Result
	Code   Code
	Effect sandboxwire.Effect
}

// Err returns the failure an answer carries, or nil for Connected.
func (r ConnectResponse) Err() error {
	if r.Result == ResultConnected {
		return nil
	}
	return &Error{Code: r.Code, Effect: r.Effect}
}

// Service is what a network service provides to Serve: resolution and a dial
// in the sandbox. Serve owns the protocol, the egress check and the splice.
type Service interface {
	// Resolve returns the addresses of a DNS name. A typed outcome is an
	// *Error; Serve answers any other error NameResolutionFailed, or
	// TimedOut when the Connect's timeout passed.
	Resolve(ctx context.Context, host string) ([]netip.Addr, error)
	// Dial connects to exactly addr, in its address family, without
	// resolving anything or falling back to another address. A typed outcome
	// is an *Error; Serve answers any other error IO with EffectPossible, or
	// TimedOut with EffectPossible when the Connect's timeout passed.
	Dial(ctx context.Context, addr netip.AddrPort) (*net.TCPConn, error)
}

// Message is a ConnectRequest or a ConnectResponse.
type Message interface {
	frameType() uint16
	encode(*sandboxwire.Encoder)
	validate() error
}

func (ConnectRequest) frameType() uint16  { return OpConnect }
func (ConnectResponse) frameType() uint16 { return ConnectResponseTag }

// Encode validates m and returns its frame. requestID must be nonzero.
func Encode(requestID uint64, m Message) (sandboxwire.Frame, error) {
	if !sandboxwire.ValidRequestID(requestID) {
		return sandboxwire.Frame{}, invalid("request ID %d", requestID)
	}
	if err := m.validate(); err != nil {
		return sandboxwire.Frame{}, err
	}
	var e sandboxwire.Encoder
	m.encode(&e)
	return sandboxwire.Frame{Type: m.frameType(), RequestID: requestID, Payload: e.Payload()}, nil
}

// Decode returns the message a frame carries. It rejects unknown tags, a zero
// request ID, invalid values and trailing bytes with errors wrapping
// sandboxwire.ErrMalformed.
func Decode(f sandboxwire.Frame) (Message, error) {
	kind, err := tags.Classify(f.Type)
	if err != nil {
		return nil, err
	}
	if !sandboxwire.ValidRequestID(f.RequestID) {
		return nil, invalid("request ID %d", f.RequestID)
	}
	r := &reader{d: sandboxwire.NewDecoder(f.Payload)}
	var m Message
	if kind == sandboxwire.KindRequest {
		m = ConnectRequest{Network: Network(r.enum(func(v uint16) bool { return Network(v).Valid() })),
			Host: string(r.bytes()), Port: r.u16(), TimeoutMillis: r.u32()}
	} else {
		resp := ConnectResponse{Result: Result(r.enum(func(v uint16) bool { return Result(v).Valid() }))}
		if resp.Result == ResultFailed {
			resp.Code = Code(r.enum(func(v uint16) bool { return Code(v).Valid() }))
			resp.Effect = r.effect()
		}
		m = resp
	}
	if r.err == nil {
		r.err = r.d.Finish()
	}
	if r.err != nil {
		return nil, r.err
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// WriteMessage encodes m and writes it as one frame.
func WriteMessage(w io.Writer, requestID uint64, m Message) error {
	f, err := Encode(requestID, m)
	if err != nil {
		return err
	}
	return sandboxwire.WriteFrame(w, f)
}

// ReadMessage reads and decodes one frame. The request ID is returned whenever
// a frame was read, even if it failed to decode.
func ReadMessage(r io.Reader) (uint64, Message, error) {
	f, err := sandboxwire.ReadFrame(r, MaxMessageBytes)
	if err != nil {
		return 0, nil, err
	}
	m, err := Decode(f)
	return f.RequestID, m, err
}

func (c ConnectRequest) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(c.Network))
	e.Bytes([]byte(c.Host))
	e.U16(c.Port)
	e.U32(c.TimeoutMillis)
}

func (r ConnectResponse) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(r.Result))
	if r.Result == ResultFailed {
		e.Enum(uint16(r.Code))
		e.Effect(r.Effect)
	}
}

func (c ConnectRequest) validate() error {
	switch {
	case !c.Network.Valid():
		return invalid("network %d", c.Network)
	case c.Port == 0:
		return invalid("port 0")
	case c.TimeoutMillis == 0 || c.TimeoutMillis > MaxTimeoutMillis:
		return invalid("timeout %d ms", c.TimeoutMillis)
	}
	return checkHost(c.Host)
}

func (r ConnectResponse) validate() error {
	switch {
	case r.Result == ResultConnected && r.Code == 0 && r.Effect == 0:
		return nil
	case r.Result == ResultFailed && r.Code.Valid() && r.Effect.Valid():
		return nil
	}
	return invalid("response result %d code %d effect %d", r.Result, r.Code, r.Effect)
}

// checkHost admits an unbracketed IP literal without a zone, or a DNS name:
// dot-separated labels of 1 to 63 ASCII letters, digits, hyphens and
// underscores, with no label starting or ending with a hyphen, an optional
// final dot, and a last label that is not all digits. Non-ASCII names arrive
// as IDNA A-labels. The host is 1 to MaxHostBytes bytes.
func checkHost(host string) error {
	if len(host) == 0 || len(host) > MaxHostBytes {
		return invalid("host of %d bytes", len(host))
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" {
			return invalid("host %q has a zone", host)
		}
		return nil
	}
	name := host
	if name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}
	label, digits := 0, true
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if label == 0 || label > 63 || name[i-1] == '-' {
				return invalid("host %q", host)
			}
			label, digits = 0, true
			continue
		}
		c := name[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
			digits = false
		case c == '-' && label > 0:
			digits = false
		default:
			return invalid("host %q", host)
		}
		label++
		if i == len(name)-1 && digits {
			return invalid("host %q ends in a numeric label", host)
		}
	}
	return nil
}

// permits reports whether egress admits a connection to addr: some rule's
// prefix contains the address and its port range contains the port. An
// IPv4-mapped IPv6 address is checked as IPv4. An address with a zone, and an
// unspecified address, which a TCP stack connects to this host, are never
// admitted.
func permits(egress []sandboxlink.EgressRule, addr netip.AddrPort) bool {
	a := addr.Addr().Unmap()
	if a.IsUnspecified() {
		return false
	}
	for _, r := range egress {
		if r.Prefix.Contains(a) && addr.Port() >= r.PortFirst && addr.Port() <= r.PortLast {
			return true
		}
	}
	return false
}

// permitsPort reports whether some rule of egress admits port.
func permitsPort(egress []sandboxlink.EgressRule, port uint16) bool {
	for _, r := range egress {
		if port >= r.PortFirst && port <= r.PortLast {
			return true
		}
	}
	return false
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{sandboxwire.ErrMalformed}, args...)...)
}

// reader decodes fields in order, keeping the first error.
type reader struct {
	d   *sandboxwire.Decoder
	err error
}

func read[T any](r *reader, f func() (T, error)) T {
	var v T
	if r.err == nil {
		v, r.err = f()
	}
	return v
}

func (r *reader) u16() uint16                { return read(r, r.d.U16) }
func (r *reader) u32() uint32                { return read(r, r.d.U32) }
func (r *reader) bytes() []byte              { return read(r, r.d.Bytes) }
func (r *reader) effect() sandboxwire.Effect { return read(r, r.d.Effect) }

func (r *reader) enum(valid func(uint16) bool) uint16 {
	return read(r, func() (uint16, error) { return r.d.Enum(valid) })
}
