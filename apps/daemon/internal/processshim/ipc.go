// Package processshim is oac-process-shim, the Session's process relay, and
// their local IPC with the process broker (apps/daemon/internal/processbroker).
// The IPC is private to the shim, the relay and the broker; it is not part of
// the process protocol.
//
// A Harness in a Session view executes the shim under a declared name or path.
// The shim holds no credentials and never contacts the sandbox: it hands its
// invocation to the relay, and the broker runs the program in the sandbox
// through the process protocol. The relay is the same binary in relay mode,
// one per Session, running in the view as the Session user with no
// capabilities. It is the only process that receives or operates on the
// shim's descriptors; the broker, outside the view, holds only its own end of
// the relay connection.
//
// # Shim and relay
//
// One invocation is one connection to SocketPath:
//
//  1. The shim sends a Request. The first byte of its frame carries, as one
//     SCM_RIGHTS message, exactly three descriptors: the shim's fds 0, 1 and
//     2, in that order.
//  2. The relay answers with an Ack once the broker accepts the invocation, or
//     with a Result instead of the Ack when the invocation is refused.
//  3. After the Ack the shim closes its fds 0, 1 and 2, so the relay's copies
//     are the invocation's only references to them, and sends a Signal for
//     each signal it catches.
//  4. The relay sends exactly one Result, and the shim exits with it.
//
// The relay owns the received descriptors from the moment it reads them and
// closes each one when it is done with it; after a refusing Result it has
// closed all three. Whoever holds fd 2 writes a failure message: the shim
// before the Ack, the relay after it. Received descriptors are close-on-exec.
// Truncated control data, anything other than one SCM_RIGHTS message with
// three descriptors on the Request, and any control data on a later frame are
// protocol violations; the receiver closes every descriptor it got and drops
// the connection.
//
// # Relay and broker
//
// The relay and the broker share one stream socket, which sessionview creates;
// the relay holds its end at RelayBrokerFD. Each message names its invocation
// by an ID the relay assigns, starting at 1 and increasing. No descriptor
// crosses this connection: the broker reads it without a control buffer, so
// the kernel discards any SCM_RIGHTS the relay attaches. The broker treats
// every relay message as untrusted Session input, and a message that breaks
// these rules ends the connection.
//
//  1. The relay sends Open with the shim's Request and, when fds 0 and 1 are
//     both terminals, the Terminal they share.
//  2. The broker answers with Accept, after which the relay sends the shim its
//     Ack, or refuses with an Exit whose Result carries the reason.
//  3. The broker sends Started once the program runs; on a terminal the relay
//     then makes the terminal raw.
//  4. Stdin is read on demand. Each Read grants one Input or InputEnd; the
//     relay reads fd 0 once per grant, sends what it read, and sends InputEnd
//     at end of file or on a read error. StopInput ends reading, and the relay
//     closes fd 0 without sending InputEnd.
//  5. The broker sends each output stream as Output and then Close, with the
//     process protocol's event sequence numbers, on FD 1 (stdout or the
//     terminal) or FD 2 (stderr). The relay writes each FD's messages in order
//     and reports each one with Written once it is written or, for Close, once
//     the descriptor is closed. On a terminal, Close of FD 1 closes fd 2 too.
//     The first write that fails is reported with WriteFailed; the relay then
//     reports nothing more for that FD, discards its Output and still closes
//     it on Close. At most OutputWindow bytes of unreported Output are
//     outstanding per FD.
//  6. The relay reports each signal the shim forwards with Signaled, and the
//     shim's loss before its Result with Gone.
//  7. Exit ends the shim. The relay stops reading stdin, waits until every
//     Mark's FD has reported Written through the Mark's Seq or has failed,
//     restores the terminal and sends the shim the Result. A Result Message
//     goes to the shim in the Result before the Ack, and to fd 2 after it.
//  8. Notice writes "oac-process-shim: <Message>" to fd 2 after the output
//     queued before it.
//  9. End closes the invocation: the relay stops its pumps, closes every
//     descriptor and forgets the ID. When the shim has had no Result, it gets
//     ExitLost. The broker sends nothing with the ID after End; it ignores
//     relay messages for an ID it has ended.
//
// The relay has at most MaxInvocations open and refuses the shim's request
// beyond that. When the broker's end closes, the relay writes the reason to
// each invocation's fd 2, sends each waiting shim ExitLost and exits.
//
// Frames use the internal/sandboxwire header with RequestID zero, and payloads
// use its primitive encoding.
package processshim

import (
	"errors"
	"fmt"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

const (
	// SocketName is the relay's socket in the view's run directory.
	SocketName = "process.sock"
	// SocketPath is the fixed path the shim connects to: SocketName in the
	// run directory of the view layout in package agent. It is not
	// configurable, so the Harness environment cannot redirect it. It is a
	// literal so that the shim does not link package agent; a test checks it
	// against the layout.
	SocketPath = "/.oac/run/" + SocketName

	// RelayName is the relay's name in the view's shim directory, which no
	// declared shim may take.
	RelayName = "oac-process-shim"
	// RelayPath is where sessionview executes the relay, with argv
	// RelayArgs.
	RelayPath = "/.oac/bin/" + RelayName
	// RelayBrokerFD is the relay's end of the broker connection, and
	// RelayListenerFD the listening socket bound at SocketPath, when the
	// relay starts.
	RelayBrokerFD   = 3
	RelayListenerFD = 4

	// Version is the shim's IPC version. The relay refuses any other.
	Version = 1
	// MaxFrameBytes bounds a frame payload.
	MaxFrameBytes = sandboxwire.MaxPayload
	// MaxRequestBytes bounds a Request's payload, and so the argument list and
	// environment, leaving room for Open around it.
	MaxRequestBytes = MaxFrameBytes - 1024
	// MaxMessageBytes bounds a Result or Notice message.
	MaxMessageBytes = 4096
	// MaxControlChars bounds Terminal.Cc.
	MaxControlChars = 32
	// MaxInvocations bounds the invocations a relay has open.
	MaxInvocations = 256
	// OutputWindow bounds the unreported Output bytes per FD.
	OutputWindow = 256 << 10
)

// RelayArgs is the relay's argv.
var RelayArgs = []string{RelayName, "relay"}

// Exit codes the shim uses when the program did not run or its exit is
// unknown.
const (
	ExitNotFound  = 127
	ExitCannotRun = 126
	ExitLost      = 255
)

// Message types between the shim and the relay.
const (
	TypeRequest uint16 = iota + 1
	TypeAck
	TypeSignal
	TypeResult
)

// Message types from the relay to the broker.
const (
	TypeOpen uint16 = iota + 0x11
	TypeInput
	TypeInputEnd
	TypeWritten
	TypeWriteFailed
	TypeSignaled
	TypeGone
)

// Message types from the broker to the relay.
const (
	TypeAccept uint16 = iota + 0x21
	TypeStarted
	TypeRead
	TypeStopInput
	TypeOutput
	TypeClose
	TypeExit
	TypeNotice
	TypeEnd
)

// ErrProtocol wraps every IPC violation.
var ErrProtocol = errors.New("processshim: protocol violation")

// Message is one IPC message.
type Message interface {
	messageType() uint16
	encode(*sandboxwire.Encoder)
}

// RelayMessage is a message from the relay to the broker.
type RelayMessage interface {
	Message
	// Invocation is the ID of the invocation the message is about.
	Invocation() uint64
	fromRelay()
}

// BrokerMessage is a message from the broker to the relay.
type BrokerMessage interface {
	Message
	// Invocation is the ID of the invocation the message is about.
	Invocation() uint64
	fromBroker()
}

// Request is the shim's invocation. Every field is what the shim's process
// has; the broker decides what reaches the sandbox.
type Request struct {
	Version uint16
	// ExecPath is the path the shim was executed by: AT_EXECFN, else argv[0].
	ExecPath []byte
	Argv     [][]byte
	// Env holds the raw environ entries.
	Env   [][]byte
	Cwd   []byte
	Umask uint32
}

// Ack says the relay owns the three descriptors.
type Ack struct{}

// Signal reports a signal the shim caught.
type Signal struct{ Number uint16 }

// Result ends the invocation. A nonzero Signal is the signal that ended the
// remote program, which the shim re-raises; otherwise the shim exits with
// Code. A Message goes to the shim only in a Result sent instead of the Ack,
// and the shim writes it to its stderr as "oac-process-shim: <Message>".
type Result struct {
	Signal  uint16
	Code    uint8
	Message []byte
}

// WindowSize is a terminal's size.
type WindowSize struct{ Rows, Cols, XPixels, YPixels uint16 }

// Terminal is the terminal on the shim's fds 0 and 1: its size, and the mode
// saved before any invocation made it raw.
type Terminal struct {
	Size                       WindowSize
	Iflag, Oflag, Cflag, Lflag uint32
	// Cc holds the control characters, at most MaxControlChars.
	Cc []byte
}

// Open hands the broker an invocation.
type Open struct {
	ID      uint64
	Request Request
	// Terminal is set when fds 0 and 1 are both terminals.
	Terminal *Terminal
}

// Input is stdin the relay read for one Read.
type Input struct {
	ID   uint64
	Data []byte
}

// InputEnd answers a Read at end of file or after a read error.
type InputEnd struct{ ID uint64 }

// Written reports that Output or Close Seq on FD is done.
type Written struct {
	ID  uint64
	FD  uint8
	Seq uint64
}

// WriteFailed reports that writing Output Seq on FD failed with Errno.
type WriteFailed struct {
	ID    uint64
	FD    uint8
	Seq   uint64
	Errno uint32
}

// Signaled reports a signal from the shim. Size is the terminal's new size
// for SIGWINCH on a terminal.
type Signaled struct {
	ID     uint64
	Number uint16
	Size   *WindowSize
}

// Gone reports that the shim's connection ended before its Result.
type Gone struct{ ID uint64 }

// Accept accepts an invocation: the relay acknowledges the shim.
type Accept struct{ ID uint64 }

// Started says the program runs.
type Started struct{ ID uint64 }

// Read grants one Input of at most Max bytes, or InputEnd.
type Read struct {
	ID  uint64
	Max uint32
}

// StopInput ends reading stdin.
type StopInput struct{ ID uint64 }

// Output is data for FD.
type Output struct {
	ID   uint64
	FD   uint8
	Seq  uint64
	Data []byte
}

// Close closes FD after its queued Output.
type Close struct {
	ID  uint64
	FD  uint8
	Seq uint64
}

// Mark is the last Output or Close on FD that must be done before the shim
// exits.
type Mark struct {
	FD  uint8
	Seq uint64
}

// Exit ends the shim with Result once Marks are done.
type Exit struct {
	ID     uint64
	Result Result
	Marks  []Mark
}

// Notice is a message for fd 2.
type Notice struct {
	ID      uint64
	Message []byte
}

// End closes the invocation.
type End struct{ ID uint64 }

func (Request) messageType() uint16     { return TypeRequest }
func (Ack) messageType() uint16         { return TypeAck }
func (Signal) messageType() uint16      { return TypeSignal }
func (Result) messageType() uint16      { return TypeResult }
func (Open) messageType() uint16        { return TypeOpen }
func (Input) messageType() uint16       { return TypeInput }
func (InputEnd) messageType() uint16    { return TypeInputEnd }
func (Written) messageType() uint16     { return TypeWritten }
func (WriteFailed) messageType() uint16 { return TypeWriteFailed }
func (Signaled) messageType() uint16    { return TypeSignaled }
func (Gone) messageType() uint16        { return TypeGone }
func (Accept) messageType() uint16      { return TypeAccept }
func (Started) messageType() uint16     { return TypeStarted }
func (Read) messageType() uint16        { return TypeRead }
func (StopInput) messageType() uint16   { return TypeStopInput }
func (Output) messageType() uint16      { return TypeOutput }
func (Close) messageType() uint16       { return TypeClose }
func (Exit) messageType() uint16        { return TypeExit }
func (Notice) messageType() uint16      { return TypeNotice }
func (End) messageType() uint16         { return TypeEnd }

func (m Open) Invocation() uint64        { return m.ID }
func (m Input) Invocation() uint64       { return m.ID }
func (m InputEnd) Invocation() uint64    { return m.ID }
func (m Written) Invocation() uint64     { return m.ID }
func (m WriteFailed) Invocation() uint64 { return m.ID }
func (m Signaled) Invocation() uint64    { return m.ID }
func (m Gone) Invocation() uint64        { return m.ID }
func (m Accept) Invocation() uint64      { return m.ID }
func (m Started) Invocation() uint64     { return m.ID }
func (m Read) Invocation() uint64        { return m.ID }
func (m StopInput) Invocation() uint64   { return m.ID }
func (m Output) Invocation() uint64      { return m.ID }
func (m Close) Invocation() uint64       { return m.ID }
func (m Exit) Invocation() uint64        { return m.ID }
func (m Notice) Invocation() uint64      { return m.ID }
func (m End) Invocation() uint64         { return m.ID }

func (Open) fromRelay()        {}
func (Input) fromRelay()       {}
func (InputEnd) fromRelay()    {}
func (Written) fromRelay()     {}
func (WriteFailed) fromRelay() {}
func (Signaled) fromRelay()    {}
func (Gone) fromRelay()        {}
func (Accept) fromBroker()     {}
func (Started) fromBroker()    {}
func (Read) fromBroker()       {}
func (StopInput) fromBroker()  {}
func (Output) fromBroker()     {}
func (Close) fromBroker()      {}
func (Exit) fromBroker()       {}
func (Notice) fromBroker()     {}
func (End) fromBroker()        {}

func (m Request) encode(e *sandboxwire.Encoder) {
	e.U16(m.Version)
	e.Bytes(m.ExecPath)
	e.Count(len(m.Argv))
	for _, a := range m.Argv {
		e.Bytes(a)
	}
	e.Count(len(m.Env))
	for _, v := range m.Env {
		e.Bytes(v)
	}
	e.Bytes(m.Cwd)
	e.U32(m.Umask)
}

func (Ack) encode(*sandboxwire.Encoder) {}

func (m Signal) encode(e *sandboxwire.Encoder) { e.U16(m.Number) }

func (m Result) encode(e *sandboxwire.Encoder) {
	e.U16(m.Signal)
	e.U8(m.Code)
	e.Bytes(m.Message)
}

func (s WindowSize) encode(e *sandboxwire.Encoder) {
	e.U16(s.Rows)
	e.U16(s.Cols)
	e.U16(s.XPixels)
	e.U16(s.YPixels)
}

func (m Open) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	m.Request.encode(e)
	e.Present(m.Terminal != nil)
	if t := m.Terminal; t != nil {
		t.Size.encode(e)
		e.U32(t.Iflag)
		e.U32(t.Oflag)
		e.U32(t.Cflag)
		e.U32(t.Lflag)
		e.Bytes(t.Cc)
	}
}

func (m Input) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	e.Bytes(m.Data)
}

func (m InputEnd) encode(e *sandboxwire.Encoder) { e.U64(m.ID) }

func (m Written) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	e.U8(m.FD)
	e.U64(m.Seq)
}

func (m WriteFailed) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	e.U8(m.FD)
	e.U64(m.Seq)
	e.U32(m.Errno)
}

func (m Signaled) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	e.U16(m.Number)
	e.Present(m.Size != nil)
	if m.Size != nil {
		m.Size.encode(e)
	}
}

func (m Gone) encode(e *sandboxwire.Encoder)      { e.U64(m.ID) }
func (m Accept) encode(e *sandboxwire.Encoder)    { e.U64(m.ID) }
func (m Started) encode(e *sandboxwire.Encoder)   { e.U64(m.ID) }
func (m StopInput) encode(e *sandboxwire.Encoder) { e.U64(m.ID) }
func (m End) encode(e *sandboxwire.Encoder)       { e.U64(m.ID) }

func (m Read) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	e.U32(m.Max)
}

func (m Output) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	e.U8(m.FD)
	e.U64(m.Seq)
	e.Bytes(m.Data)
}

func (m Close) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	e.U8(m.FD)
	e.U64(m.Seq)
}

func (m Exit) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	m.Result.encode(e)
	e.Count(len(m.Marks))
	for _, k := range m.Marks {
		e.U8(k.FD)
		e.U64(k.Seq)
	}
}

func (m Notice) encode(e *sandboxwire.Encoder) {
	e.U64(m.ID)
	e.Bytes(m.Message)
}

// Frame returns m as a frame.
func Frame(m Message) sandboxwire.Frame {
	var e sandboxwire.Encoder
	m.encode(&e)
	return sandboxwire.Frame{Type: m.messageType(), Payload: e.Payload()}
}

// Decode decodes and validates a frame between the shim and the relay. A
// Request with another Version decodes with only its Version set, so the
// relay can refuse it.
func Decode(f sandboxwire.Frame) (Message, error) {
	return decode(f, func(d *sandboxwire.Decoder) (Message, error) {
		switch f.Type {
		case TypeRequest:
			r, err := decodeRequest(d)
			if err == nil && r.Version != Version {
				return r, nil // the rest is another version's
			}
			return r, finish(d, err)
		case TypeAck:
			return Ack{}, d.Finish()
		case TypeSignal:
			n, err := decodeSignal(d)
			return Signal{Number: n}, finish(d, err)
		case TypeResult:
			r, err := decodeResult(d)
			return r, finish(d, err)
		}
		return nil, errType
	})
}

// DecodeRelay decodes and validates a frame from the relay.
func DecodeRelay(f sandboxwire.Frame) (RelayMessage, error) {
	m, err := decode(f, func(d *sandboxwire.Decoder) (Message, error) {
		id, err := decodeID(d)
		if err != nil {
			return nil, err
		}
		switch f.Type {
		case TypeOpen:
			m := Open{ID: id}
			m.Request, err = decodeRequest(d)
			if err == nil && m.Request.Version != Version {
				err = fmt.Errorf("request version %d", m.Request.Version)
			}
			if err == nil {
				m.Terminal, err = decodeTerminal(d)
			}
			return m, finish(d, err)
		case TypeInput:
			m := Input{ID: id}
			m.Data, err = decodeData(d)
			return m, finish(d, err)
		case TypeInputEnd:
			return InputEnd{ID: id}, d.Finish()
		case TypeWritten:
			m := Written{ID: id}
			m.FD, m.Seq, err = decodeFDSeq(d)
			return m, finish(d, err)
		case TypeWriteFailed:
			m := WriteFailed{ID: id}
			m.FD, m.Seq, err = decodeFDSeq(d)
			if err == nil {
				m.Errno, err = d.U32()
			}
			if err == nil && m.Errno == 0 {
				err = errors.New("errno 0")
			}
			return m, finish(d, err)
		case TypeSignaled:
			m := Signaled{ID: id}
			m.Number, err = decodeSignal(d)
			if err == nil {
				m.Size, err = decodeSize(d)
			}
			return m, finish(d, err)
		case TypeGone:
			return Gone{ID: id}, d.Finish()
		}
		return nil, errType
	})
	if err != nil {
		return nil, err
	}
	return m.(RelayMessage), nil
}

// DecodeBroker decodes and validates a frame from the broker.
func DecodeBroker(f sandboxwire.Frame) (BrokerMessage, error) {
	m, err := decode(f, func(d *sandboxwire.Decoder) (Message, error) {
		id, err := decodeID(d)
		if err != nil {
			return nil, err
		}
		switch f.Type {
		case TypeAccept:
			return Accept{ID: id}, d.Finish()
		case TypeStarted:
			return Started{ID: id}, d.Finish()
		case TypeRead:
			m := Read{ID: id}
			m.Max, err = d.U32()
			if err == nil && (m.Max == 0 || m.Max > sandboxwire.MaxChunk) {
				err = fmt.Errorf("read of %d bytes", m.Max)
			}
			return m, finish(d, err)
		case TypeStopInput:
			return StopInput{ID: id}, d.Finish()
		case TypeOutput:
			m := Output{ID: id}
			m.FD, m.Seq, err = decodeFDSeq(d)
			if err == nil {
				m.Data, err = decodeData(d)
			}
			return m, finish(d, err)
		case TypeClose:
			m := Close{ID: id}
			m.FD, m.Seq, err = decodeFDSeq(d)
			return m, finish(d, err)
		case TypeExit:
			m := Exit{ID: id}
			if m.Result, err = decodeResult(d); err == nil {
				m.Marks, err = decodeMarks(d)
			}
			return m, finish(d, err)
		case TypeNotice:
			m := Notice{ID: id}
			m.Message, err = d.Bytes()
			if err == nil && (len(m.Message) == 0 || len(m.Message) > MaxMessageBytes) {
				err = fmt.Errorf("notice of %d bytes", len(m.Message))
			}
			return m, finish(d, err)
		case TypeEnd:
			return End{ID: id}, d.Finish()
		}
		return nil, errType
	})
	if err != nil {
		return nil, err
	}
	return m.(BrokerMessage), nil
}

var errType = errors.New("message type")

func decode(f sandboxwire.Frame, body func(*sandboxwire.Decoder) (Message, error)) (Message, error) {
	if f.RequestID != 0 {
		return nil, fmt.Errorf("%w: request ID %d", ErrProtocol, f.RequestID)
	}
	m, err := body(sandboxwire.NewDecoder(f.Payload))
	switch {
	case err == errType:
		return nil, fmt.Errorf("%w: message type %#x", ErrProtocol, f.Type)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	return m, nil
}

// finish returns err, or the decoder's error for trailing bytes.
func finish(d *sandboxwire.Decoder, err error) error {
	if err != nil {
		return err
	}
	return d.Finish()
}

func decodeID(d *sandboxwire.Decoder) (uint64, error) {
	id, err := d.U64()
	if err == nil && id == 0 {
		err = errors.New("invocation ID 0")
	}
	return id, err
}

func decodeRequest(d *sandboxwire.Decoder) (Request, error) {
	var r Request
	var err error
	if r.Version, err = d.U16(); err != nil || r.Version != Version {
		return Request{Version: r.Version}, err
	}
	if r.ExecPath, err = d.Bytes(); err != nil {
		return r, err
	}
	if r.Argv, err = decodeList(d); err != nil {
		return r, err
	}
	if r.Env, err = decodeList(d); err != nil {
		return r, err
	}
	if r.Cwd, err = d.Bytes(); err != nil {
		return r, err
	}
	if r.Umask, err = d.U32(); err != nil {
		return r, err
	}
	switch {
	case slices.Contains(r.ExecPath, 0) || slices.Contains(r.Cwd, 0):
		return r, errors.New("NUL in path")
	case len(r.Cwd) == 0 || r.Cwd[0] != '/':
		return r, errors.New("cwd is not absolute")
	case r.Umask > 0o777:
		return r, fmt.Errorf("umask %#o", r.Umask)
	}
	for _, b := range slices.Concat(r.Argv, r.Env) {
		if slices.Contains(b, 0) {
			return r, errors.New("NUL in argv or environment")
		}
	}
	return r, nil
}

func decodeList(d *sandboxwire.Decoder) ([][]byte, error) {
	n, err := d.Count(MaxFrameBytes / 4)
	if err != nil {
		return nil, err
	}
	list := make([][]byte, n)
	for i := range list {
		if list[i], err = d.Bytes(); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func decodeSignal(d *sandboxwire.Decoder) (uint16, error) {
	n, err := d.U16()
	if err == nil && (n == 0 || n > 64) {
		err = fmt.Errorf("signal %d", n)
	}
	return n, err
}

func decodeResult(d *sandboxwire.Decoder) (Result, error) {
	var r Result
	var err error
	if r.Signal, err = d.U16(); err != nil {
		return r, err
	}
	if r.Code, err = d.U8(); err != nil {
		return r, err
	}
	if r.Message, err = d.Bytes(); err != nil {
		return r, err
	}
	switch {
	case r.Signal > 64 || (r.Signal != 0 && r.Code != 0):
		return r, fmt.Errorf("signal %d with code %d", r.Signal, r.Code)
	case len(r.Message) > MaxMessageBytes:
		return r, fmt.Errorf("message of %d bytes", len(r.Message))
	}
	return r, nil
}

func decodeSize(d *sandboxwire.Decoder) (*WindowSize, error) {
	ok, err := d.Present()
	if err != nil || !ok {
		return nil, err
	}
	var s WindowSize
	for _, v := range []*uint16{&s.Rows, &s.Cols, &s.XPixels, &s.YPixels} {
		if *v, err = d.U16(); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

func decodeTerminal(d *sandboxwire.Decoder) (*Terminal, error) {
	size, err := decodeSize(d)
	if err != nil || size == nil {
		return nil, err
	}
	t := &Terminal{Size: *size}
	for _, v := range []*uint32{&t.Iflag, &t.Oflag, &t.Cflag, &t.Lflag} {
		if *v, err = d.U32(); err != nil {
			return nil, err
		}
	}
	if t.Cc, err = d.Bytes(); err != nil {
		return nil, err
	}
	if len(t.Cc) > MaxControlChars {
		return nil, fmt.Errorf("%d control characters", len(t.Cc))
	}
	return t, nil
}

func decodeData(d *sandboxwire.Decoder) ([]byte, error) {
	b, err := d.Bytes()
	if err == nil && (len(b) == 0 || len(b) > sandboxwire.MaxChunk) {
		err = fmt.Errorf("data of %d bytes", len(b))
	}
	return b, err
}

func decodeFD(d *sandboxwire.Decoder) (uint8, error) {
	fd, err := d.U8()
	if err == nil && fd != 1 && fd != 2 {
		err = fmt.Errorf("fd %d", fd)
	}
	return fd, err
}

func decodeFDSeq(d *sandboxwire.Decoder) (uint8, uint64, error) {
	fd, err := decodeFD(d)
	if err != nil {
		return 0, 0, err
	}
	seq, err := d.U64()
	if err == nil && seq == 0 {
		err = errors.New("sequence 0")
	}
	return fd, seq, err
}

func decodeMarks(d *sandboxwire.Decoder) ([]Mark, error) {
	n, err := d.Count(2)
	if err != nil || n == 0 {
		return nil, err
	}
	marks := make([]Mark, n)
	for i := range marks {
		if marks[i].FD, marks[i].Seq, err = decodeFDSeq(d); err != nil {
			return nil, err
		}
		if i > 0 && marks[i].FD == marks[0].FD {
			return nil, fmt.Errorf("two marks on fd %d", marks[i].FD)
		}
	}
	return marks, nil
}
