// Package processshim is oac-process-shim and its local IPC with the process
// broker (apps/daemon/internal/processbroker).
//
// A Harness in a Session view executes the shim under a declared name or path.
// The shim holds no credentials and never contacts the sandbox: it hands its
// invocation to the broker, which runs the program in the sandbox through the
// process protocol, and then mirrors the remote exit.
//
// One invocation is one connection to SocketPath:
//
//  1. The shim sends a Request. The first byte of its frame carries, as one
//     SCM_RIGHTS message, exactly three descriptors: the shim's fds 0, 1 and
//     2, in that order.
//  2. The broker answers with an Ack once it owns those descriptors, or with a
//     Result instead of the Ack when it refuses the invocation.
//  3. After the Ack the shim closes its fds 0, 1 and 2, so the broker's copies
//     are the invocation's only references to them, and sends a Signal for
//     each signal it catches.
//  4. The broker sends exactly one Result, and the shim exits with it.
//
// Descriptor ownership: the broker owns the received descriptors from the
// moment it reads them and closes each one when it is done with it; after a
// refusing Result it has closed all three. Whoever holds fd 2 writes the
// failure message: the shim before the Ack, the broker after it. Received
// descriptors are close-on-exec. Truncated control data, anything other than
// one SCM_RIGHTS message with three descriptors on the Request, and any
// control data on a later frame are protocol violations; the receiver closes
// every descriptor it got and drops the connection.
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
	// RunDir is the view directory the broker listens in: the Session's
	// private directory named "run".
	RunDir = "/.oac/run"
	// SocketName is the broker's socket in RunDir.
	SocketName = "process.sock"
	// SocketPath is the fixed path the shim connects to. It is not
	// configurable, so the Harness environment cannot redirect it.
	SocketPath = RunDir + "/" + SocketName

	// Version is the IPC version. The broker refuses any other.
	Version = 1
	// MaxFrameBytes bounds a frame payload, and so the request's argv and
	// environment.
	MaxFrameBytes = sandboxwire.MaxPayload
	// MaxMessageBytes bounds a Result message.
	MaxMessageBytes = 4096
)

// Exit codes the shim uses when the program did not run or its exit is
// unknown.
const (
	ExitNotFound  = 127
	ExitCannotRun = 126
	ExitLost      = 255
)

// Message types.
const (
	TypeRequest uint16 = iota + 1
	TypeAck
	TypeSignal
	TypeResult
)

// ErrProtocol wraps every IPC violation.
var ErrProtocol = errors.New("processshim: protocol violation")

// Message is one IPC message.
type Message interface {
	messageType() uint16
	encode(*sandboxwire.Encoder)
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

// Ack says the broker owns the three descriptors.
type Ack struct{}

// Signal reports a signal the shim caught.
type Signal struct{ Number uint16 }

// Result ends the invocation. A nonzero Signal is the signal that ended the
// remote program, which the shim re-raises; otherwise the shim exits with
// Code. Message is set only in a Result sent instead of the Ack, and the shim
// writes it to its stderr as "oac-process-shim: <Message>".
type Result struct {
	Signal  uint16
	Code    uint8
	Message []byte
}

func (Request) messageType() uint16 { return TypeRequest }
func (Ack) messageType() uint16     { return TypeAck }
func (Signal) messageType() uint16  { return TypeSignal }
func (Result) messageType() uint16  { return TypeResult }

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

// Frame returns m as a frame.
func Frame(m Message) sandboxwire.Frame {
	var e sandboxwire.Encoder
	m.encode(&e)
	return sandboxwire.Frame{Type: m.messageType(), Payload: e.Payload()}
}

// Decode decodes and validates a frame. A Request with another Version
// decodes with only its Version set, so the broker can refuse it.
func Decode(f sandboxwire.Frame) (Message, error) {
	if f.RequestID != 0 {
		return nil, fmt.Errorf("%w: request ID %d", ErrProtocol, f.RequestID)
	}
	d := sandboxwire.NewDecoder(f.Payload)
	var m Message
	var err error
	switch f.Type {
	case TypeRequest:
		m, err = decodeRequest(d)
	case TypeAck:
		m = Ack{}
	case TypeSignal:
		var s Signal
		s.Number, err = d.U16()
		if err == nil && (s.Number == 0 || s.Number > 64) {
			err = fmt.Errorf("signal %d", s.Number)
		}
		m = s
	case TypeResult:
		m, err = decodeResult(d)
	default:
		return nil, fmt.Errorf("%w: message type %d", ErrProtocol, f.Type)
	}
	if err == nil {
		if r, ok := m.(Request); !ok || r.Version == Version {
			err = d.Finish()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	return m, nil
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
