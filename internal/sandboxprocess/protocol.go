// Package sandboxprocess is the Runtime–process service protocol: the one
// authored definition of its messages, payload layouts, validators and Service
// interface, plus a generic client and server over one stream.
//
// docs/process-protocol.md describes the protocol. Framing and primitive
// encoding come from internal/sandboxwire.
package sandboxprocess

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Version is the protocol version. Peers match it exactly.
const Version = 1

// Request tags. The response to a request uses the request tag | 0x8000.
const (
	OpDescribe uint16 = iota + 1
	OpStart
	OpAttach
	OpInspect
	OpWriteStdin
	OpCloseStdin
	OpCloseOutput
	OpResizePTY
	OpSignal
	OpCancel
	OpAckEvents
	OpRelease
)

// Event tags, numbered from sandboxwire.FirstEvent in this order.
const (
	EventStarted uint16 = sandboxwire.FirstEvent + iota
	EventStartFailed
	EventOutput
	EventStreamClosed
	EventExited
	EventOutputClosed
	EventScopeClosed
	EventObservationLost
)

var tags = sandboxwire.Tags{Requests: OpRelease, Events: EventObservationLost - sandboxwire.FirstEvent + 1}

const (
	// MaxFailureMessageBytes bounds Failure.Message.
	MaxFailureMessageBytes = 4096
	// MaxTermBytes bounds PTYSpec.Term.
	MaxTermBytes = 256
	// maxListEntries bounds every capability list.
	maxListEntries = 256
	// maxSpecEntries bounds Argv and Env; the payload limit binds first.
	maxSpecEntries = sandboxwire.MaxPayload / 4
)

// Enums are uint16 on the wire; zero is never valid.

// Platform is the operating system whose semantics a service implements.
type Platform uint16

const PlatformLinux Platform = 1

func (v Platform) Valid() bool { return v == PlatformLinux }

// Scope is the containment an operation's processes are started in. Both
// scopes start a new POSIX session; ScopeCgroupV2 also enforces cgroup
// containment.
type Scope uint16

const (
	ScopePOSIXSession Scope = iota + 1
	ScopeCgroupV2
)

func (v Scope) Valid() bool { return v >= ScopePOSIXSession && v <= ScopeCgroupV2 }

// IOMode selects how descriptors 0, 1 and 2 are connected.
type IOMode uint16

const (
	IOPipes IOMode = iota + 1
	IOPTY
)

func (v IOMode) Valid() bool { return v == IOPipes || v == IOPTY }

// Stream is a captured output stream. A PTY merges output into StreamTerminal.
type Stream uint16

const (
	StreamStdout Stream = iota + 1
	StreamStderr
	StreamTerminal
)

func (v Stream) Valid() bool { return v >= StreamStdout && v <= StreamTerminal }

// SignalTarget selects which processes of an operation receive a signal. A
// request never names a process ID.
type SignalTarget uint16

const (
	TargetLeader SignalTarget = iota + 1
	TargetInitialProcessGroup
	TargetPTYForegroundGroup
	TargetScope
)

func (v SignalTarget) Valid() bool { return v >= TargetLeader && v <= TargetScope }

// Signal is a Linux signal number under PlatformLinux.
type Signal uint16

func (v Signal) Valid() bool { return v >= 1 && v <= 64 }

// StartDisposition says whether Start created the operation or found it.
type StartDisposition uint16

const (
	StartCreated StartDisposition = iota + 1
	StartExisting
)

func (v StartDisposition) Valid() bool { return v == StartCreated || v == StartExisting }

// OperationState is an operation's launch and exit state.
type OperationState uint16

const (
	StateStarting OperationState = iota + 1
	StateRunning
	StateExited
	StateStartFailed
	// StateUnknown means the exit could not be observed.
	StateUnknown
)

func (v OperationState) Valid() bool { return v >= StateStarting && v <= StateUnknown }

// ScopeState says whether an operation's scope still has members.
type ScopeState uint16

const (
	ScopeStateActive ScopeState = iota + 1
	ScopeStateClosed
	ScopeStateUnknown
)

func (v ScopeState) Valid() bool { return v >= ScopeStateActive && v <= ScopeStateUnknown }

// OutputDisposition says how an output stream ended.
type OutputDisposition uint16

const (
	// OutputDrained: the stream reached end of file.
	OutputDrained OutputDisposition = iota + 1
	// OutputAbandoned: CloseOutput closed the read side.
	OutputAbandoned
	// OutputLost: reading failed.
	OutputLost
)

func (v OutputDisposition) Valid() bool { return v >= OutputDrained && v <= OutputLost }

// ExitKind discriminates ExitStatus.
type ExitKind uint16

const (
	ExitCode ExitKind = iota + 1
	ExitSignal
)

func (v ExitKind) Valid() bool { return v == ExitCode || v == ExitSignal }

// Observation names what ObservationLost reports as unavailable.
type Observation uint16

const (
	ObservationExit Observation = iota + 1
	ObservationScope
)

func (v Observation) Valid() bool { return v == ObservationExit || v == ObservationScope }

// ErrorCode is the typed outcome of a failed request.
type ErrorCode uint16

const (
	CodeInvalidArgument ErrorCode = iota + 1
	CodeUnsupported
	CodeUnauthorized
	CodeStaleAttachment
	CodeInstanceChanged
	CodeNotFound
	CodeOperationConflict
	CodeReleased
	CodeReplayGap
	CodeInputOffsetConflict
	CodeStdinClosed
	CodeOutputClosed
	CodeNotRunning
	CodeBusy
	CodeResourceExhausted
	CodeDeadlineExceeded
	CodeCancelled
	CodeIO
	CodeUnknown
)

var codeNames = [...]string{"", "InvalidArgument", "Unsupported", "Unauthorized", "StaleAttachment", "InstanceChanged", "NotFound", "OperationConflict", "Released", "ReplayGap", "InputOffsetConflict", "StdinClosed", "OutputClosed", "NotRunning", "Busy", "ResourceExhausted", "DeadlineExceeded", "Cancelled", "IO", "Unknown"}

func (v ErrorCode) Valid() bool { return v >= CodeInvalidArgument && v <= CodeUnknown }

func (v ErrorCode) String() string {
	if v.Valid() {
		return codeNames[v]
	}
	return fmt.Sprintf("ErrorCode(%d)", uint16(v))
}

// PTYMode is a terminal mode opcode from RFC 4254 §8, plus IUTF8 from RFC
// 8160. Opcodes 1–18 set a control character: the value is the character and
// 255 disables it. Opcodes 30–93 set a flag: the value is 0 or 1. The speed
// opcodes carry a baud rate.
type PTYMode uint16

const (
	ModeVINTR    PTYMode = 1
	ModeVQUIT    PTYMode = 2
	ModeVERASE   PTYMode = 3
	ModeVKILL    PTYMode = 4
	ModeVEOF     PTYMode = 5
	ModeVEOL     PTYMode = 6
	ModeVEOL2    PTYMode = 7
	ModeVSTART   PTYMode = 8
	ModeVSTOP    PTYMode = 9
	ModeVSUSP    PTYMode = 10
	ModeVDSUSP   PTYMode = 11
	ModeVREPRINT PTYMode = 12
	ModeVWERASE  PTYMode = 13
	ModeVLNEXT   PTYMode = 14
	ModeVFLUSH   PTYMode = 15
	ModeVSWTCH   PTYMode = 16
	ModeVSTATUS  PTYMode = 17
	ModeVDISCARD PTYMode = 18
	ModeIGNPAR   PTYMode = 30
	ModePARMRK   PTYMode = 31
	ModeINPCK    PTYMode = 32
	ModeISTRIP   PTYMode = 33
	ModeINLCR    PTYMode = 34
	ModeIGNCR    PTYMode = 35
	ModeICRNL    PTYMode = 36
	ModeIUCLC    PTYMode = 37
	ModeIXON     PTYMode = 38
	ModeIXANY    PTYMode = 39
	ModeIXOFF    PTYMode = 40
	ModeIMAXBEL  PTYMode = 41
	ModeIUTF8    PTYMode = 42
	ModeISIG     PTYMode = 50
	ModeICANON   PTYMode = 51
	ModeXCASE    PTYMode = 52
	ModeECHO     PTYMode = 53
	ModeECHOE    PTYMode = 54
	ModeECHOK    PTYMode = 55
	ModeECHONL   PTYMode = 56
	ModeNOFLSH   PTYMode = 57
	ModeTOSTOP   PTYMode = 58
	ModeIEXTEN   PTYMode = 59
	ModeECHOCTL  PTYMode = 60
	ModeECHOKE   PTYMode = 61
	ModePENDIN   PTYMode = 62
	ModeOPOST    PTYMode = 70
	ModeOLCUC    PTYMode = 71
	ModeONLCR    PTYMode = 72
	ModeOCRNL    PTYMode = 73
	ModeONOCR    PTYMode = 74
	ModeONLRET   PTYMode = 75
	ModeCS7      PTYMode = 90
	ModeCS8      PTYMode = 91
	ModePARENB   PTYMode = 92
	ModePARODD   PTYMode = 93
	ModeISPEED   PTYMode = 128
	ModeOSPEED   PTYMode = 129
)

// IsChar reports whether m sets a control character.
func (m PTYMode) IsChar() bool { return m >= ModeVINTR && m <= ModeVDISCARD }

// IsSpeed reports whether m sets a line speed.
func (m PTYMode) IsSpeed() bool { return m == ModeISPEED || m == ModeOSPEED }

func (m PTYMode) Valid() bool {
	switch {
	case m.IsChar(), m.IsSpeed():
		return true
	case m >= ModeIGNPAR && m <= ModeIUTF8, m >= ModeISIG && m <= ModePENDIN:
		return true
	case m >= ModeOPOST && m <= ModeONLRET, m >= ModeCS7 && m <= ModePARODD:
		return true
	}
	return false
}

// DisabledChar is the control-character value that disables the character.
const DisabledChar = 255

// Failure is the typed outcome of a failed request or launch. Effect says
// whether the request may have taken effect.
type Failure struct {
	Code    ErrorCode
	Effect  sandboxwire.Effect
	Message string
}

// Fail returns a Failure with a formatted message.
func Fail(code ErrorCode, effect sandboxwire.Effect, format string, args ...any) *Failure {
	msg := fmt.Sprintf(format, args...)
	if len(msg) > MaxFailureMessageBytes {
		msg = msg[:MaxFailureMessageBytes]
	}
	return &Failure{Code: code, Effect: effect, Message: msg}
}

func (f *Failure) Error() string {
	effect := "no effect"
	if f.Effect == sandboxwire.EffectPossible {
		effect = "effect possible"
	}
	return fmt.Sprintf("sandboxprocess: %s (%s): %s", f.Code, effect, f.Message)
}

// OperationRef addresses an operation in one service incarnation. Every
// request except Describe begins with it. The attachment comes from the
// stream, never from the payload.
type OperationRef struct {
	ServerInstanceID sandboxwire.ID
	OperationID      sandboxwire.ID
}

// EnvVar is one environment entry.
type EnvVar struct {
	Name  []byte
	Value []byte
}

// WindowSize is a terminal size in characters and pixels.
type WindowSize struct {
	Rows    uint16
	Cols    uint16
	XPixels uint16
	YPixels uint16
}

// PTYModeValue sets one terminal mode.
type PTYModeValue struct {
	Mode  PTYMode
	Value uint32
}

// PTYSpec describes the terminal of an IOPTY operation. The service sets TERM
// in the child's environment to Term.
type PTYSpec struct {
	Size  WindowSize
	Term  []byte
	Modes []PTYModeValue
}

// ProcessSpec is everything a launch uses; nothing is inherited from the
// service. Executable, Argv, Env and Cwd are bytes, not strings.
type ProcessSpec struct {
	// Executable is a name resolved with Env's PATH when it has no '/', else a
	// path, relative paths resolving against Cwd.
	Executable []byte
	// Argv is the complete argv, including argv[0].
	Argv [][]byte
	// Env is the complete environment.
	Env    []EnvVar
	Cwd    []byte
	Umask  uint32
	IOMode IOMode
	// PTY is present exactly when IOMode is IOPTY.
	PTY   *PTYSpec
	Scope Scope
}

// Digest identifies a spec for Start deduplication: the SHA-256 of its
// encoding.
func (s ProcessSpec) Digest() [sha256.Size]byte {
	var e sandboxwire.Encoder
	s.encode(&e)
	return sha256.Sum256(e.Payload())
}

// Capabilities is what a service supports. Every field is declared; a request
// outside it fails with CodeUnsupported or CodeInvalidArgument.
type Capabilities struct {
	Platform      Platform
	Scopes        []Scope
	IOModes       []IOMode
	Signals       []Signal
	SignalTargets []SignalTarget
	PTYModes      []PTYMode
	// MaxStartBytes bounds an encoded Start request payload.
	MaxStartBytes uint32
	// MaxDataBytes bounds WriteStdin data and Output event data.
	MaxDataBytes        uint32
	MaxActiveOperations uint32
	// MaxOperationRecords bounds live operations plus tombstones.
	MaxOperationRecords uint32
	// MaxReplayBytesPerOperation bounds unacknowledged Output data retained
	// per operation; output reading pauses at the limit.
	MaxReplayBytesPerOperation uint32
	// OwnerLossGraceMillis is how long an operation survives the loss of its
	// attachment before cleanup cancels it.
	OwnerLossGraceMillis uint32
	// CancelGraceLimitMillis is the largest Cancel grace, and the grace
	// cleanup uses.
	CancelGraceLimitMillis uint32
}

// CheckStart validates a spec against c.
func (c Capabilities) CheckStart(s ProcessSpec) *Failure {
	switch {
	case !slices.Contains(c.Scopes, s.Scope):
		return Fail(CodeUnsupported, sandboxwire.EffectNone, "scope %d is not supported", s.Scope)
	case !slices.Contains(c.IOModes, s.IOMode):
		return Fail(CodeUnsupported, sandboxwire.EffectNone, "I/O mode %d is not supported", s.IOMode)
	}
	if s.PTY != nil {
		for _, m := range s.PTY.Modes {
			if !slices.Contains(c.PTYModes, m.Mode) {
				return Fail(CodeUnsupported, sandboxwire.EffectNone, "terminal mode %d is not supported", m.Mode)
			}
		}
	}
	return nil
}

// CheckSignal validates a signal and target against c.
func (c Capabilities) CheckSignal(sig Signal, target SignalTarget) *Failure {
	switch {
	case !slices.Contains(c.Signals, sig):
		return Fail(CodeUnsupported, sandboxwire.EffectNone, "signal %d is not supported", sig)
	case !slices.Contains(c.SignalTargets, target):
		return Fail(CodeUnsupported, sandboxwire.EffectNone, "signal target %d is not supported", target)
	}
	return nil
}

// ExitStatus is the leader's wait result: an exit code, or the terminating
// signal and whether it dumped core.
type ExitStatus struct {
	Kind       ExitKind
	Code       uint8
	Signal     Signal
	CoreDumped bool
}

// OperationStatus is an operation's current record. The retained event range
// is FirstRetained through LastSequence; it is empty when FirstRetained is
// greater.
type OperationStatus struct {
	State OperationState
	// Exit is present exactly when State is StateExited.
	Exit *ExitStatus
	// StartFailure is present exactly when State is StateStartFailed.
	StartFailure *Failure
	StdinOffset  uint64
	StdinClosed  bool
	// Output is present once every captured stream has closed.
	Output        *OutputDisposition
	Scope         ScopeState
	Released      bool
	FirstRetained uint64
	LastSequence  uint64
}

// Message is a request, response or event.
type Message interface {
	// MessageType is the frame tag.
	MessageType() uint16
	encode(*sandboxwire.Encoder)
}

// Requests.

type DescribeRequest struct{}

type StartRequest struct {
	OperationRef
	Spec ProcessSpec
}

// AttachRequest observes an operation from the event after AfterSequence.
type AttachRequest struct {
	OperationRef
	AfterSequence uint64
}

type InspectRequest struct{ OperationRef }

// WriteStdinRequest writes Data at stdin offset Offset.
type WriteStdinRequest struct {
	OperationRef
	Offset uint64
	Data   []byte
}

// CloseStdinRequest closes pipe stdin after Offset accepted bytes.
type CloseStdinRequest struct {
	OperationRef
	Offset uint64
}

type CloseOutputRequest struct {
	OperationRef
	Stream Stream
}

type ResizePTYRequest struct {
	OperationRef
	Size WindowSize
}

type SignalRequest struct {
	OperationRef
	Signal Signal
	Target SignalTarget
}

type CancelRequest struct {
	OperationRef
	GraceMillis uint32
}

type AckEventsRequest struct {
	OperationRef
	Sequence uint64
}

type ReleaseRequest struct{ OperationRef }

// Responses.

type DescribeResponse struct {
	ServerInstanceID sandboxwire.ID
	Capabilities     Capabilities
}

type StartResponse struct{ Disposition StartDisposition }

type AttachResponse struct{ Status OperationStatus }

type InspectResponse struct{ Status OperationStatus }

type WriteStdinResponse struct{ Accepted uint32 }

type CloseStdinResponse struct{}

type CloseOutputResponse struct{}

type ResizePTYResponse struct{}

type SignalResponse struct{}

type CancelResponse struct{}

type AckEventsResponse struct{}

type ReleaseResponse struct{}

// ResponseFailure is the failed response to request tag Request.
type ResponseFailure struct {
	Request uint16
	Failure Failure
}

// Events.

// EventHeader begins every event. Sequence starts at 1 and increases by one
// per event of the operation.
type EventHeader struct {
	OperationID sandboxwire.ID
	Sequence    uint64
}

func (h EventHeader) Header() EventHeader { return h }

// Event is an unsolicited message about one operation.
type Event interface {
	Message
	Header() EventHeader
}

type StartedEvent struct{ EventHeader }

type StartFailedEvent struct {
	EventHeader
	Failure Failure
}

type OutputEvent struct {
	EventHeader
	Stream Stream
	Offset uint64
	Data   []byte
}

// StreamClosedEvent reports a stream's end at its final Offset.
type StreamClosedEvent struct {
	EventHeader
	Stream      Stream
	Offset      uint64
	Disposition OutputDisposition
}

type ExitedEvent struct {
	EventHeader
	Status ExitStatus
}

type OutputClosedEvent struct {
	EventHeader
	Disposition OutputDisposition
}

type ScopeClosedEvent struct{ EventHeader }

type ObservationLostEvent struct {
	EventHeader
	Observation Observation
	Failure     Failure
}

// Service is a process service. Serve calls one method per request with the
// Conn it arrived on. A returned *Failure is sent as is; any other error is
// sent as CodeUnknown with EffectPossible.
type Service interface {
	Describe(context.Context, *Conn, DescribeRequest) (DescribeResponse, error)
	Start(context.Context, *Conn, StartRequest) (StartResponse, error)
	Attach(context.Context, *Conn, AttachRequest) (AttachResponse, error)
	Inspect(context.Context, *Conn, InspectRequest) (InspectResponse, error)
	WriteStdin(context.Context, *Conn, WriteStdinRequest) (WriteStdinResponse, error)
	CloseStdin(context.Context, *Conn, CloseStdinRequest) (CloseStdinResponse, error)
	CloseOutput(context.Context, *Conn, CloseOutputRequest) (CloseOutputResponse, error)
	ResizePTY(context.Context, *Conn, ResizePTYRequest) (ResizePTYResponse, error)
	Signal(context.Context, *Conn, SignalRequest) (SignalResponse, error)
	Cancel(context.Context, *Conn, CancelRequest) (CancelResponse, error)
	AckEvents(context.Context, *Conn, AckEventsRequest) (AckEventsResponse, error)
	Release(context.Context, *Conn, ReleaseRequest) (ReleaseResponse, error)
}

func (DescribeRequest) MessageType() uint16    { return OpDescribe }
func (StartRequest) MessageType() uint16       { return OpStart }
func (AttachRequest) MessageType() uint16      { return OpAttach }
func (InspectRequest) MessageType() uint16     { return OpInspect }
func (WriteStdinRequest) MessageType() uint16  { return OpWriteStdin }
func (CloseStdinRequest) MessageType() uint16  { return OpCloseStdin }
func (CloseOutputRequest) MessageType() uint16 { return OpCloseOutput }
func (ResizePTYRequest) MessageType() uint16   { return OpResizePTY }
func (SignalRequest) MessageType() uint16      { return OpSignal }
func (CancelRequest) MessageType() uint16      { return OpCancel }
func (AckEventsRequest) MessageType() uint16   { return OpAckEvents }
func (ReleaseRequest) MessageType() uint16     { return OpRelease }

func (DescribeResponse) MessageType() uint16    { return sandboxwire.ResponseType(OpDescribe) }
func (StartResponse) MessageType() uint16       { return sandboxwire.ResponseType(OpStart) }
func (AttachResponse) MessageType() uint16      { return sandboxwire.ResponseType(OpAttach) }
func (InspectResponse) MessageType() uint16     { return sandboxwire.ResponseType(OpInspect) }
func (WriteStdinResponse) MessageType() uint16  { return sandboxwire.ResponseType(OpWriteStdin) }
func (CloseStdinResponse) MessageType() uint16  { return sandboxwire.ResponseType(OpCloseStdin) }
func (CloseOutputResponse) MessageType() uint16 { return sandboxwire.ResponseType(OpCloseOutput) }
func (ResizePTYResponse) MessageType() uint16   { return sandboxwire.ResponseType(OpResizePTY) }
func (SignalResponse) MessageType() uint16      { return sandboxwire.ResponseType(OpSignal) }
func (CancelResponse) MessageType() uint16      { return sandboxwire.ResponseType(OpCancel) }
func (AckEventsResponse) MessageType() uint16   { return sandboxwire.ResponseType(OpAckEvents) }
func (ReleaseResponse) MessageType() uint16     { return sandboxwire.ResponseType(OpRelease) }
func (m ResponseFailure) MessageType() uint16   { return sandboxwire.ResponseType(m.Request) }

func (StartedEvent) MessageType() uint16         { return EventStarted }
func (StartFailedEvent) MessageType() uint16     { return EventStartFailed }
func (OutputEvent) MessageType() uint16          { return EventOutput }
func (StreamClosedEvent) MessageType() uint16    { return EventStreamClosed }
func (ExitedEvent) MessageType() uint16          { return EventExited }
func (OutputClosedEvent) MessageType() uint16    { return EventOutputClosed }
func (ScopeClosedEvent) MessageType() uint16     { return EventScopeClosed }
func (ObservationLostEvent) MessageType() uint16 { return EventObservationLost }

// A response payload begins with this discriminator.
type result uint16

const (
	resultOK result = iota + 1
	resultFailure
)

func (v result) Valid() bool { return v == resultOK || v == resultFailure }

// Encode returns m's payload. It does not validate m.
func Encode(m Message) []byte {
	var e sandboxwire.Encoder
	if _, failed := m.(ResponseFailure); !failed && sandboxwire.IsResponse(m.MessageType()) {
		e.Enum(uint16(resultOK))
	}
	m.encode(&e)
	return e.Payload()
}

// Decode decodes and validates the payload of a frame of type t. Every error
// wraps sandboxwire.ErrMalformed.
func Decode(t uint16, payload []byte) (Message, error) {
	kind, err := tags.Classify(t)
	if err != nil {
		return nil, err
	}
	r := &reader{d: sandboxwire.NewDecoder(payload)}
	var m Message
	switch kind {
	case sandboxwire.KindRequest:
		m = readRequest(r, t)
	case sandboxwire.KindResponse:
		op := t &^ sandboxwire.ResponseType(0)
		if enum[result](r) == resultFailure {
			m = ResponseFailure{Request: op, Failure: readFailure(r)}
		} else {
			m = readResponse(r, op)
		}
	case sandboxwire.KindEvent:
		m = readEvent(r, t)
	}
	if r.err == nil {
		r.err = r.d.Finish()
	}
	if r.err == nil {
		r.err = validate(m)
	}
	if r.err != nil {
		return nil, r.err
	}
	return m, nil
}

// Encoding. Fields are written in declaration order.

func (r OperationRef) encode(e *sandboxwire.Encoder) {
	e.ID(r.ServerInstanceID)
	e.ID(r.OperationID)
}

func (f Failure) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(f.Code))
	e.Effect(f.Effect)
	e.Bytes([]byte(f.Message))
}

func (s WindowSize) encode(e *sandboxwire.Encoder) {
	e.U16(s.Rows)
	e.U16(s.Cols)
	e.U16(s.XPixels)
	e.U16(s.YPixels)
}

func (s ProcessSpec) encode(e *sandboxwire.Encoder) {
	e.Bytes(s.Executable)
	e.Count(len(s.Argv))
	for _, a := range s.Argv {
		e.Bytes(a)
	}
	e.Count(len(s.Env))
	for _, v := range s.Env {
		e.Bytes(v.Name)
		e.Bytes(v.Value)
	}
	e.Bytes(s.Cwd)
	e.U32(s.Umask)
	e.Enum(uint16(s.IOMode))
	e.Present(s.PTY != nil)
	if p := s.PTY; p != nil {
		p.Size.encode(e)
		e.Bytes(p.Term)
		e.Count(len(p.Modes))
		for _, m := range p.Modes {
			e.Enum(uint16(m.Mode))
			e.U32(m.Value)
		}
	}
	e.Enum(uint16(s.Scope))
}

func encodeEnums[T ~uint16](e *sandboxwire.Encoder, list []T) {
	e.Count(len(list))
	for _, v := range list {
		e.Enum(uint16(v))
	}
}

func (c Capabilities) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(c.Platform))
	encodeEnums(e, c.Scopes)
	encodeEnums(e, c.IOModes)
	encodeEnums(e, c.Signals)
	encodeEnums(e, c.SignalTargets)
	encodeEnums(e, c.PTYModes)
	e.U32(c.MaxStartBytes)
	e.U32(c.MaxDataBytes)
	e.U32(c.MaxActiveOperations)
	e.U32(c.MaxOperationRecords)
	e.U32(c.MaxReplayBytesPerOperation)
	e.U32(c.OwnerLossGraceMillis)
	e.U32(c.CancelGraceLimitMillis)
}

func (s ExitStatus) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(s.Kind))
	if s.Kind == ExitCode {
		e.U8(s.Code)
		return
	}
	e.Enum(uint16(s.Signal))
	e.Bool(s.CoreDumped)
}

func (s OperationStatus) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(s.State))
	e.Present(s.Exit != nil)
	if s.Exit != nil {
		s.Exit.encode(e)
	}
	e.Present(s.StartFailure != nil)
	if s.StartFailure != nil {
		s.StartFailure.encode(e)
	}
	e.U64(s.StdinOffset)
	e.Bool(s.StdinClosed)
	e.Present(s.Output != nil)
	if s.Output != nil {
		e.Enum(uint16(*s.Output))
	}
	e.Enum(uint16(s.Scope))
	e.Bool(s.Released)
	e.U64(s.FirstRetained)
	e.U64(s.LastSequence)
}

func (h EventHeader) encode(e *sandboxwire.Encoder) {
	e.ID(h.OperationID)
	e.U64(h.Sequence)
}

func (DescribeRequest) encode(*sandboxwire.Encoder) {}
func (m StartRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	m.Spec.encode(e)
}
func (m AttachRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	e.U64(m.AfterSequence)
}
func (m InspectRequest) encode(e *sandboxwire.Encoder) { m.OperationRef.encode(e) }
func (m WriteStdinRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	e.U64(m.Offset)
	e.Bytes(m.Data)
}
func (m CloseStdinRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	e.U64(m.Offset)
}
func (m CloseOutputRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	e.Enum(uint16(m.Stream))
}
func (m ResizePTYRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	m.Size.encode(e)
}
func (m SignalRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	e.Enum(uint16(m.Signal))
	e.Enum(uint16(m.Target))
}
func (m CancelRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	e.U32(m.GraceMillis)
}
func (m AckEventsRequest) encode(e *sandboxwire.Encoder) {
	m.OperationRef.encode(e)
	e.U64(m.Sequence)
}
func (m ReleaseRequest) encode(e *sandboxwire.Encoder) { m.OperationRef.encode(e) }

func (m DescribeResponse) encode(e *sandboxwire.Encoder) {
	e.ID(m.ServerInstanceID)
	m.Capabilities.encode(e)
}
func (m StartResponse) encode(e *sandboxwire.Encoder)      { e.Enum(uint16(m.Disposition)) }
func (m AttachResponse) encode(e *sandboxwire.Encoder)     { m.Status.encode(e) }
func (m InspectResponse) encode(e *sandboxwire.Encoder)    { m.Status.encode(e) }
func (m WriteStdinResponse) encode(e *sandboxwire.Encoder) { e.U32(m.Accepted) }
func (CloseStdinResponse) encode(*sandboxwire.Encoder)     {}
func (CloseOutputResponse) encode(*sandboxwire.Encoder)    {}
func (ResizePTYResponse) encode(*sandboxwire.Encoder)      {}
func (SignalResponse) encode(*sandboxwire.Encoder)         {}
func (CancelResponse) encode(*sandboxwire.Encoder)         {}
func (AckEventsResponse) encode(*sandboxwire.Encoder)      {}
func (ReleaseResponse) encode(*sandboxwire.Encoder)        {}
func (m ResponseFailure) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(resultFailure))
	m.Failure.encode(e)
}

func (m StartedEvent) encode(e *sandboxwire.Encoder) { m.EventHeader.encode(e) }
func (m StartFailedEvent) encode(e *sandboxwire.Encoder) {
	m.EventHeader.encode(e)
	m.Failure.encode(e)
}
func (m OutputEvent) encode(e *sandboxwire.Encoder) {
	m.EventHeader.encode(e)
	e.Enum(uint16(m.Stream))
	e.U64(m.Offset)
	e.Bytes(m.Data)
}
func (m StreamClosedEvent) encode(e *sandboxwire.Encoder) {
	m.EventHeader.encode(e)
	e.Enum(uint16(m.Stream))
	e.U64(m.Offset)
	e.Enum(uint16(m.Disposition))
}
func (m ExitedEvent) encode(e *sandboxwire.Encoder) {
	m.EventHeader.encode(e)
	m.Status.encode(e)
}
func (m OutputClosedEvent) encode(e *sandboxwire.Encoder) {
	m.EventHeader.encode(e)
	e.Enum(uint16(m.Disposition))
}
func (m ScopeClosedEvent) encode(e *sandboxwire.Encoder) { m.EventHeader.encode(e) }
func (m ObservationLostEvent) encode(e *sandboxwire.Encoder) {
	m.EventHeader.encode(e)
	e.Enum(uint16(m.Observation))
	m.Failure.encode(e)
}

// Decoding.

// reader keeps the first error, after which every read returns a zero value.
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

func (r *reader) u8() uint8                  { return read(r, r.d.U8) }
func (r *reader) u16() uint16                { return read(r, r.d.U16) }
func (r *reader) u32() uint32                { return read(r, r.d.U32) }
func (r *reader) u64() uint64                { return read(r, r.d.U64) }
func (r *reader) boolean() bool              { return read(r, r.d.Bool) }
func (r *reader) present() bool              { return read(r, r.d.Present) }
func (r *reader) bytes() []byte              { return read(r, r.d.Bytes) }
func (r *reader) id() sandboxwire.ID         { return read(r, r.d.ID) }
func (r *reader) effect() sandboxwire.Effect { return read(r, r.d.Effect) }
func (r *reader) count(max uint32) int {
	return read(r, func() (int, error) { return r.d.Count(max) })
}

func enum[T interface {
	~uint16
	Valid() bool
}](r *reader) T {
	return T(read(r, func() (uint16, error) { return r.d.Enum(func(v uint16) bool { return T(v).Valid() }) }))
}

// enums reads a list of distinct enum values.
func enums[T interface {
	~uint16
	Valid() bool
}](r *reader) []T {
	n := r.count(maxListEntries)
	list := make([]T, 0, n)
	for range n {
		v := enum[T](r)
		if r.err == nil && slices.Contains(list, v) {
			r.err = malformed("duplicate list entry %d", v)
		}
		list = append(list, v)
	}
	return list
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{sandboxwire.ErrMalformed}, args...)...)
}

func readRef(r *reader) OperationRef {
	return OperationRef{ServerInstanceID: r.id(), OperationID: r.id()}
}

func readFailure(r *reader) Failure {
	return Failure{Code: enum[ErrorCode](r), Effect: r.effect(), Message: string(r.bytes())}
}

func readWindowSize(r *reader) WindowSize {
	return WindowSize{Rows: r.u16(), Cols: r.u16(), XPixels: r.u16(), YPixels: r.u16()}
}

func readSpec(r *reader) ProcessSpec {
	var s ProcessSpec
	s.Executable = r.bytes()
	s.Argv = make([][]byte, r.count(maxSpecEntries))
	for i := range s.Argv {
		s.Argv[i] = r.bytes()
	}
	s.Env = make([]EnvVar, r.count(maxSpecEntries))
	for i := range s.Env {
		s.Env[i] = EnvVar{Name: r.bytes(), Value: r.bytes()}
	}
	s.Cwd = r.bytes()
	s.Umask = r.u32()
	s.IOMode = enum[IOMode](r)
	if r.present() {
		p := &PTYSpec{Size: readWindowSize(r), Term: r.bytes()}
		p.Modes = make([]PTYModeValue, r.count(maxListEntries))
		for i := range p.Modes {
			p.Modes[i] = PTYModeValue{Mode: enum[PTYMode](r), Value: r.u32()}
		}
		s.PTY = p
	}
	s.Scope = enum[Scope](r)
	return s
}

func readCapabilities(r *reader) Capabilities {
	return Capabilities{
		Platform:                   enum[Platform](r),
		Scopes:                     enums[Scope](r),
		IOModes:                    enums[IOMode](r),
		Signals:                    enums[Signal](r),
		SignalTargets:              enums[SignalTarget](r),
		PTYModes:                   enums[PTYMode](r),
		MaxStartBytes:              r.u32(),
		MaxDataBytes:               r.u32(),
		MaxActiveOperations:        r.u32(),
		MaxOperationRecords:        r.u32(),
		MaxReplayBytesPerOperation: r.u32(),
		OwnerLossGraceMillis:       r.u32(),
		CancelGraceLimitMillis:     r.u32(),
	}
}

func readExitStatus(r *reader) ExitStatus {
	s := ExitStatus{Kind: enum[ExitKind](r)}
	if s.Kind == ExitCode {
		s.Code = r.u8()
	} else {
		s.Signal = enum[Signal](r)
		s.CoreDumped = r.boolean()
	}
	return s
}

func readStatus(r *reader) OperationStatus {
	s := OperationStatus{State: enum[OperationState](r)}
	if r.present() {
		exit := readExitStatus(r)
		s.Exit = &exit
	}
	if r.present() {
		f := readFailure(r)
		s.StartFailure = &f
	}
	s.StdinOffset = r.u64()
	s.StdinClosed = r.boolean()
	if r.present() {
		d := enum[OutputDisposition](r)
		s.Output = &d
	}
	s.Scope = enum[ScopeState](r)
	s.Released = r.boolean()
	s.FirstRetained = r.u64()
	s.LastSequence = r.u64()
	return s
}

func readHeader(r *reader) EventHeader {
	return EventHeader{OperationID: r.id(), Sequence: r.u64()}
}

func readRequest(r *reader, t uint16) Message {
	if t == OpDescribe {
		return DescribeRequest{}
	}
	ref := readRef(r)
	switch t {
	case OpStart:
		return StartRequest{ref, readSpec(r)}
	case OpAttach:
		return AttachRequest{ref, r.u64()}
	case OpInspect:
		return InspectRequest{ref}
	case OpWriteStdin:
		return WriteStdinRequest{ref, r.u64(), r.bytes()}
	case OpCloseStdin:
		return CloseStdinRequest{ref, r.u64()}
	case OpCloseOutput:
		return CloseOutputRequest{ref, enum[Stream](r)}
	case OpResizePTY:
		return ResizePTYRequest{ref, readWindowSize(r)}
	case OpSignal:
		return SignalRequest{ref, enum[Signal](r), enum[SignalTarget](r)}
	case OpCancel:
		return CancelRequest{ref, r.u32()}
	case OpAckEvents:
		return AckEventsRequest{ref, r.u64()}
	default:
		return ReleaseRequest{ref}
	}
}

func readResponse(r *reader, op uint16) Message {
	switch op {
	case OpDescribe:
		return DescribeResponse{r.id(), readCapabilities(r)}
	case OpStart:
		return StartResponse{enum[StartDisposition](r)}
	case OpAttach:
		return AttachResponse{readStatus(r)}
	case OpInspect:
		return InspectResponse{readStatus(r)}
	case OpWriteStdin:
		return WriteStdinResponse{r.u32()}
	case OpCloseStdin:
		return CloseStdinResponse{}
	case OpCloseOutput:
		return CloseOutputResponse{}
	case OpResizePTY:
		return ResizePTYResponse{}
	case OpSignal:
		return SignalResponse{}
	case OpCancel:
		return CancelResponse{}
	case OpAckEvents:
		return AckEventsResponse{}
	default:
		return ReleaseResponse{}
	}
}

func readEvent(r *reader, t uint16) Message {
	h := readHeader(r)
	switch t {
	case EventStarted:
		return StartedEvent{h}
	case EventStartFailed:
		return StartFailedEvent{h, readFailure(r)}
	case EventOutput:
		return OutputEvent{h, enum[Stream](r), r.u64(), r.bytes()}
	case EventStreamClosed:
		return StreamClosedEvent{h, enum[Stream](r), r.u64(), enum[OutputDisposition](r)}
	case EventExited:
		return ExitedEvent{h, readExitStatus(r)}
	case EventOutputClosed:
		return OutputClosedEvent{h, enum[OutputDisposition](r)}
	case EventScopeClosed:
		return ScopeClosedEvent{h}
	default:
		return ObservationLostEvent{h, enum[Observation](r), readFailure(r)}
	}
}

// Validation beyond the primitive rules.

func validate(m Message) error {
	switch m := m.(type) {
	case StartRequest:
		return m.Spec.Validate()
	case WriteStdinRequest:
		return checkData(m.Offset, m.Data)
	case DescribeResponse:
		return m.Capabilities.Validate()
	case AttachResponse:
		return m.Status.Validate()
	case InspectResponse:
		return m.Status.Validate()
	case ResponseFailure:
		return m.Failure.validate()
	case Event:
		return validateEvent(m)
	}
	return nil
}

func validateEvent(ev Event) error {
	if ev.Header().Sequence == 0 {
		return malformed("event sequence 0")
	}
	switch ev := ev.(type) {
	case StartFailedEvent:
		return ev.Failure.validate()
	case OutputEvent:
		return checkData(ev.Offset, ev.Data)
	case ObservationLostEvent:
		return ev.Failure.validate()
	}
	return nil
}

// checkData checks a chunk written or read at offset.
func checkData(offset uint64, b []byte) error {
	switch {
	case len(b) > sandboxwire.MaxChunk:
		return malformed("data chunk of %d bytes exceeds %d", len(b), sandboxwire.MaxChunk)
	case offset > math.MaxUint64-uint64(len(b)):
		return malformed("data chunk of %d bytes at offset %d overflows", len(b), offset)
	}
	return nil
}

func (f Failure) validate() error {
	if len(f.Message) > MaxFailureMessageBytes {
		return malformed("failure message of %d bytes", len(f.Message))
	}
	return nil
}

func hasNUL(b []byte) bool { return slices.Contains(b, 0) }

// Validate checks the rules every ProcessSpec follows, whatever the service
// supports.
func (s ProcessSpec) Validate() error {
	switch {
	case len(s.Executable) == 0 || hasNUL(s.Executable):
		return malformed("executable is empty or contains NUL")
	case len(s.Argv) == 0:
		return malformed("argv is empty")
	case len(s.Cwd) == 0 || s.Cwd[0] != '/' || hasNUL(s.Cwd):
		return malformed("cwd is not an absolute path")
	case s.Umask > 0o777:
		return malformed("umask %#o", s.Umask)
	case (s.IOMode == IOPTY) != (s.PTY != nil):
		return malformed("PTY must be present exactly for IOPTY")
	}
	for _, a := range s.Argv {
		if hasNUL(a) {
			return malformed("argv entry contains NUL")
		}
	}
	names := make(map[string]struct{}, len(s.Env))
	for _, v := range s.Env {
		if len(v.Name) == 0 || hasNUL(v.Name) || slices.Contains(v.Name, '=') || hasNUL(v.Value) {
			return malformed("invalid environment entry")
		}
		if _, dup := names[string(v.Name)]; dup {
			return malformed("duplicate environment entry %q", v.Name)
		}
		names[string(v.Name)] = struct{}{}
	}
	if _, ok := names["PATH"]; !ok && !slices.Contains(s.Executable, '/') {
		return malformed("executable name needs a PATH entry")
	}
	if s.PTY == nil {
		return nil
	}
	if _, ok := names["TERM"]; ok {
		return malformed("TERM comes from the PTY spec, not the environment")
	}
	return s.PTY.validate()
}

func (p PTYSpec) validate() error {
	if len(p.Term) == 0 || len(p.Term) > MaxTermBytes || hasNUL(p.Term) {
		return malformed("invalid TERM")
	}
	seen := make([]PTYMode, 0, len(p.Modes))
	for _, m := range p.Modes {
		switch {
		case slices.Contains(seen, m.Mode):
			return malformed("duplicate terminal mode %d", m.Mode)
		case m.Mode.IsChar() && m.Value > DisabledChar:
			return malformed("terminal character %d value %d", m.Mode, m.Value)
		case !m.Mode.IsChar() && !m.Mode.IsSpeed() && m.Value > 1:
			return malformed("terminal flag %d value %d", m.Mode, m.Value)
		}
		seen = append(seen, m.Mode)
	}
	return nil
}

// Validate checks declared limits against the frame limits.
func (c Capabilities) Validate() error {
	switch {
	case c.MaxStartBytes == 0 || c.MaxStartBytes > sandboxwire.MaxPayload:
		return malformed("MaxStartBytes %d", c.MaxStartBytes)
	case c.MaxDataBytes == 0 || c.MaxDataBytes > sandboxwire.MaxChunk:
		return malformed("MaxDataBytes %d", c.MaxDataBytes)
	case c.MaxReplayBytesPerOperation < c.MaxDataBytes:
		return malformed("MaxReplayBytesPerOperation %d is below MaxDataBytes", c.MaxReplayBytesPerOperation)
	}
	return nil
}

// Validate checks the status invariants.
func (s OperationStatus) Validate() error {
	switch {
	case (s.State == StateExited) != (s.Exit != nil):
		return malformed("exit status must be present exactly in state Exited")
	case (s.State == StateStartFailed) != (s.StartFailure != nil):
		return malformed("start failure must be present exactly in state StartFailed")
	case s.FirstRetained == 0 || s.FirstRetained > s.LastSequence+1:
		return malformed("retained range %d..%d", s.FirstRetained, s.LastSequence)
	case s.StartFailure != nil:
		return s.StartFailure.validate()
	}
	return nil
}
