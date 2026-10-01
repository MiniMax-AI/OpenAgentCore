// Package sandboxlink is the Link protocol. It connects the Sandbox I/O service
// (the serve peer) and the agent-host Runtime (the attach peer) to a relay that
// authenticates both, authorizes each service stream and then splices the two
// streams without reading them.
//
// This file is the protocol's one authored definition: its vocabulary, message
// tags and payload layouts, validators and the Authority the relay calls. The
// framing and primitive encoding come from sandboxwire. The protocol document
// is docs/sandbox-link-protocol.md.
package sandboxlink

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Version is the Link protocol version. Peers match it exactly.
const Version uint16 = 1

const (
	// MaxMessageBytes bounds every Link message payload. A relay never
	// advertises a MaxFrameBytes below it.
	MaxMessageBytes = 16 << 10
	// MaxCredentialBytes bounds a serve or Runtime credential.
	MaxCredentialBytes = 4 << 10
	// MaxGrantBytes bounds an attachment grant.
	MaxGrantBytes = 8 << 10
	// MaxEgressRules bounds the egress rules of one binding.
	MaxEgressRules = 256
	// MaxExports bounds the export grants of one binding.
	MaxExports = 64
	// MaxExportIDBytes bounds an export ID.
	MaxExportIDBytes = 64
	// MaxControlRequests bounds the control requests an attach peer has
	// outstanding on one link. The relay refuses more with LimitExceeded.
	MaxControlRequests = 16
)

// ErrRelayURL rejects a relay URL that does not reach the relay over TLS or
// that carries credentials, a query or a fragment.
var ErrRelayURL = errors.New("sandbox link: the relay URL must be wss://, or ws:// to a loopback host, without credentials, query or fragment")

// CheckRelayURL checks the URL a peer dials. Peers reach the relay over TLS:
// wss://, or ws:// only to localhost or a loopback address. Credentials travel
// in the Hello, never in the URL.
func CheckRelayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.ContainsAny(raw, "?#") || strings.ContainsFunc(raw, unicode.IsSpace) {
		return ErrRelayURL
	}
	switch u.Scheme {
	case "wss":
		return nil
	case "ws":
		if ip := net.ParseIP(u.Hostname()); u.Hostname() == "localhost" || ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return ErrRelayURL
}

// Op is a request tag. Its response uses sandboxwire.ResponseType(op).
type Op uint16

const (
	OpHello           Op = 1 // ServeHello or AttachHello; answered by HelloAccepted
	OpOpen            Op = 2 // Open; answered by Opened
	OpBind            Op = 3 // Bind; answered by Bound
	OpRenewAttachment Op = 4 // RenewAttachment; answered by AttachmentRenewed
	OpCloseAttachment Op = 5 // CloseAttachment; answered by CloseAccepted
)

// EventAttachmentClosed is the tag of the AttachmentClosed event.
const EventAttachmentClosed uint16 = sandboxwire.FirstEvent

var tags = sandboxwire.Tags{Requests: uint16(OpCloseAttachment), Events: 1}

// A response payload begins with this discriminator.
const (
	resultSuccess uint16 = 1
	resultFailure uint16 = 2
)

// Role is the kind of peer a Hello introduces.
type Role uint16

const (
	RoleServe  Role = 1
	RoleAttach Role = 2
)

// Service is a protocol carried on an opened stream.
type Service uint16

const (
	ServiceFile    Service = 1
	ServiceProcess Service = 2
	ServiceNetwork Service = 3
)

func (s Service) Valid() bool { return s >= ServiceFile && s <= ServiceNetwork }

func (s Service) String() string {
	switch s {
	case ServiceFile:
		return "file"
	case ServiceProcess:
		return "process"
	case ServiceNetwork:
		return "network"
	}
	return fmt.Sprintf("service(%d)", uint16(s))
}

// ResourceKind records where a resource came from. File and Process execution
// never branch on it.
type ResourceKind uint16

const (
	ResourceAllocation ResourceKind = 1
	ResourceEnrollment ResourceKind = 2
)

func (k ResourceKind) Valid() bool { return k == ResourceAllocation || k == ResourceEnrollment }

// ResourceRef names the sandbox a serve peer serves. Generation starts at 1 and
// grows each time the resource is recreated.
type ResourceRef struct {
	TenantID      sandboxwire.ID
	EnvironmentID sandboxwire.ID
	Kind          ResourceKind
	ID            sandboxwire.ID
	Generation    uint64
}

// SameResource reports whether r and o name the same resource, whatever their
// generations.
func (r ResourceRef) SameResource(o ResourceRef) bool {
	return r.TenantID == o.TenantID && r.EnvironmentID == o.EnvironmentID && r.Kind == o.Kind && r.ID == o.ID
}

// AddressFamily is the address family of an egress prefix.
type AddressFamily uint16

const (
	FamilyIPv4 AddressFamily = 1
	FamilyIPv6 AddressFamily = 2
)

// EgressRule permits connections to an address inside Prefix on a port in
// PortFirst..PortLast. Prefix is masked: its host bits are zero.
type EgressRule struct {
	Prefix    netip.Prefix
	PortFirst uint16
	PortLast  uint16
}

// ExportID names an export of the File service, such as "world": 1 to 64
// bytes of lowercase ASCII letters, digits, '_' and '-'.
type ExportID string

func (id ExportID) Valid() bool {
	if len(id) == 0 || len(id) > MaxExportIDBytes {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ExportGrant grants a File stream one export. A ReadOnly grant allows only
// read-only access to it.
type ExportGrant struct {
	ID       ExportID
	ReadOnly bool
}

// CloseReason says why the relay closed an attachment.
type CloseReason uint16

const (
	// CloseRequested: the attach peer sent CloseAttachment.
	CloseRequested CloseReason = 1
	// CloseLeaseExpired: the lease ran out without renewal.
	CloseLeaseExpired CloseReason = 2
	// CloseRevoked: authority for the attachment or its resource was revoked.
	CloseRevoked CloseReason = 3
	// CloseStaleGeneration: a newer generation of the resource connected.
	CloseStaleGeneration CloseReason = 4
)

func (r CloseReason) Valid() bool { return r >= CloseRequested && r <= CloseStaleGeneration }

// Code is a typed Link failure. It is also an error value, so
// errors.Is(err, sandboxlink.LeaseExpired) matches an *Error with that code.
type Code uint16

const (
	VersionMismatch Code = iota + 1
	AuthenticationFailed
	PermissionDenied
	ResourceNotFound
	ServiceUnavailable
	StaleGeneration
	StaleAssignment
	InstanceChanged
	LeaseExpired
	AttachmentConflict
	LimitExceeded
	ProtocolViolation
)

var codeNames = [...]string{
	VersionMismatch:      "version mismatch",
	AuthenticationFailed: "authentication failed",
	PermissionDenied:     "permission denied",
	ResourceNotFound:     "resource not found",
	ServiceUnavailable:   "service unavailable",
	StaleGeneration:      "stale generation",
	StaleAssignment:      "stale assignment",
	InstanceChanged:      "instance changed",
	LeaseExpired:         "lease expired",
	AttachmentConflict:   "attachment conflict",
	LimitExceeded:        "limit exceeded",
	ProtocolViolation:    "protocol violation",
}

func (c Code) Valid() bool { return c >= VersionMismatch && c <= ProtocolViolation }

func (c Code) String() string {
	if c.Valid() {
		return codeNames[c]
	}
	return fmt.Sprintf("code(%d)", uint16(c))
}

func (c Code) Error() string { return "sandbox link: " + c.String() }

// Error is a typed Link failure with its effect. Cause is the local error that
// produced it, such as a transport failure or a canceled context; it is never
// sent.
type Error struct {
	Code   Code
	Effect sandboxwire.Effect
	Cause  error
}

// Fail returns the failure for code with EffectNone.
func Fail(code Code) *Error { return &Error{Code: code, Effect: sandboxwire.EffectNone} }

// Uncertain returns the failure of a request whose frame began to be sent and
// whose answer never arrived: ServiceUnavailable with EffectPossible, caused
// by err.
func Uncertain(err error) *Error {
	return &Error{Code: ServiceUnavailable, Effect: sandboxwire.EffectPossible, Cause: err}
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Code.Error() + ": " + e.Cause.Error()
	}
	return e.Code.Error()
}

func (e *Error) Unwrap() error { return e.Cause }

// Is matches a Code target.
func (e *Error) Is(target error) bool {
	c, ok := target.(Code)
	return ok && c == e.Code
}

// ServiceVersion is one service a serve peer offers.
type ServiceVersion struct {
	Service Service
	Version uint16
}

// ServeHello introduces the Sandbox I/O service. The credential identifies
// the peer, so the Hello carries no peer ID. ServerInstanceID changes whenever
// the service loses its operation or handle registry.
type ServeHello struct {
	Version          uint16
	Credential       []byte
	Resource         ResourceRef
	ServerInstanceID sandboxwire.ID
	Services         []ServiceVersion
}

// AttachHello introduces an agent-host Runtime.
type AttachHello struct {
	Version    uint16
	RuntimeID  sandboxwire.ID
	Credential []byte
}

// HelloAccepted answers either Hello. MaxStreams bounds the link's concurrent
// service streams and MaxFrameBytes the payload of any frame on the link.
type HelloAccepted struct {
	LinkID        sandboxwire.ID
	MaxStreams    uint32
	MaxFrameBytes uint32
}

// Open is the first message on a service stream the attach peer opens. A zero
// ExpectedServerInstanceID means no expectation.
type Open struct {
	Service                  Service
	Version                  uint16
	Resource                 ResourceRef
	ExpectedServerInstanceID sandboxwire.ID
	AttachmentID             sandboxwire.ID
	SessionID                sandboxwire.ID
	AssignmentID             sandboxwire.ID
	AssignmentEpoch          uint64
	AttachGrant              []byte
}

// Identity is the binding identity of an attachment. Reopening an attachment
// requires the identical identity.
type Identity struct {
	AttachmentID    sandboxwire.ID
	Resource        ResourceRef
	SessionID       sandboxwire.ID
	AssignmentID    sandboxwire.ID
	AssignmentEpoch uint64
}

func (o Open) Identity() Identity {
	return Identity{AttachmentID: o.AttachmentID, Resource: o.Resource, SessionID: o.SessionID, AssignmentID: o.AssignmentID, AssignmentEpoch: o.AssignmentEpoch}
}

// Opened answers Open. After it the stream carries the service's frames.
type Opened struct {
	AttachmentID     sandboxwire.ID
	ServerInstanceID sandboxwire.ID
	LeaseExpiresAt   time.Time
	MaxFrameBytes    uint32
}

// Bind is the first message on a stream the relay opens to the serve peer. It
// carries the authorized binding, never a grant or credential. Exports are the
// authorized exports of a ServiceFile stream: at least one, each ID once.
// Egress is the authorized egress of a ServiceNetwork stream, where an empty
// list denies everything. Other services carry neither.
type Bind struct {
	AttachmentID             sandboxwire.ID
	Service                  Service
	Version                  uint16
	SessionID                sandboxwire.ID
	AssignmentID             sandboxwire.ID
	AssignmentEpoch          uint64
	LeaseExpiresAt           time.Time
	ExpectedServerInstanceID sandboxwire.ID
	MaxFrameBytes            uint32
	Exports                  []ExportGrant
	Egress                   []EgressRule
}

// Bound accepts a Bind.
type Bound struct{}

// RenewAttachment extends an attachment's lease with a current grant.
type RenewAttachment struct {
	AttachmentID sandboxwire.ID
	AttachGrant  []byte
}

// AttachmentRenewed answers RenewAttachment.
type AttachmentRenewed struct {
	AttachmentID   sandboxwire.ID
	LeaseExpiresAt time.Time
}

// CloseAttachment ends an attachment and all its streams.
type CloseAttachment struct {
	AttachmentID sandboxwire.ID
}

// CloseAccepted answers CloseAttachment.
type CloseAccepted struct{}

// AttachmentClosed tells a peer that the relay closed an attachment.
type AttachmentClosed struct {
	AttachmentID sandboxwire.ID
	Reason       CloseReason
}

// Failure is the failed response to the request tagged Op.
type Failure struct {
	Op     Op
	Code   Code
	Effect sandboxwire.Effect
}

// Err returns the failure as an *Error.
func (f Failure) Err() error { return &Error{Code: f.Code, Effect: f.Effect} }

// FailureFor returns the Failure answering op for err: the code and effect of
// an *Error, or ServiceUnavailable with EffectNone for any other error.
func FailureFor(op Op, err error) Failure {
	var e *Error
	if errors.As(err, &e) && e.Code.Valid() && e.Effect.Valid() {
		return Failure{Op: op, Code: e.Code, Effect: e.Effect}
	}
	return Failure{Op: op, Code: ServiceUnavailable, Effect: sandboxwire.EffectNone}
}

// ServePeer is what the Authority verified about a serve peer: its identity
// and the resource, including generation, its credential serves.
type ServePeer struct {
	PeerID   sandboxwire.ID
	Resource ResourceRef
}

// AttachPeer is what the Authority verified about an attach peer. The relay
// hands it back on every later call, so Revision lets the Authority reject a
// link authenticated with a since-rotated credential.
type AttachPeer struct {
	RuntimeID sandboxwire.ID
	Revision  uint64
}

// Authorization is the Authority's decision for one Open or renewal. Service is
// the authorized service of an Open and zero for a renewal. Exports are the
// authorized exports of a ServiceFile Open and Egress the authorized egress of
// a ServiceNetwork Open, with the rules of Bind; every other authorization
// carries neither. A renewal extends only the lease: the service, exports and
// egress of a stream are fixed when it opens.
type Authorization struct {
	Identity       Identity
	Service        Service
	LeaseExpiresAt time.Time
	Exports        []ExportGrant
	Egress         []EgressRule
}

// Validate checks an Authority's answer for open, or for a renewal when open
// is nil.
func (a Authorization) Validate(open *Open) error {
	if err := checkLease(a.LeaseExpiresAt); err != nil {
		return err
	}
	if open == nil {
		if a.Service != 0 || len(a.Exports) != 0 || len(a.Egress) != 0 {
			return invalid("renewal with service %d, %d exports and %d egress rules", a.Service, len(a.Exports), len(a.Egress))
		}
		return checkIDs(a.Identity.AttachmentID)
	}
	if a.Identity != open.Identity() || a.Service != open.Service {
		return invalid("authorization for another binding")
	}
	if err := checkExports(a.Service, a.Exports); err != nil {
		return err
	}
	return checkEgress(a.Service, a.Egress)
}

// Authority is what the relay consults. Core implements it; tests use
// sandboxlinktest.Authority. A method returns an *Error for a typed refusal;
// any other error means the authority is unavailable and the relay answers
// ServiceUnavailable.
type Authority interface {
	// AuthenticateServe verifies a serve credential and the resource it serves.
	AuthenticateServe(ctx context.Context, hello ServeHello) (ServePeer, error)
	// AuthenticateAttach verifies a Runtime credential.
	AuthenticateAttach(ctx context.Context, hello AttachHello) (AttachPeer, error)
	// AuthorizeOpen verifies the grant, the current assignment, the resource
	// generation, the permitted service and access, and that the resource's
	// serve authority is current.
	AuthorizeOpen(ctx context.Context, peer AttachPeer, open Open) (Authorization, error)
	// Renew verifies a grant for an existing attachment and returns its new
	// lease.
	Renew(ctx context.Context, peer AttachPeer, renew RenewAttachment) (Authorization, error)
}

// Message is any Link message: a request, a success response, a Failure or an
// event.
type Message interface {
	frameType() uint16
	encode(*sandboxwire.Encoder)
	validate() error
}

func (ServeHello) frameType() uint16      { return uint16(OpHello) }
func (AttachHello) frameType() uint16     { return uint16(OpHello) }
func (HelloAccepted) frameType() uint16   { return sandboxwire.ResponseType(uint16(OpHello)) }
func (Open) frameType() uint16            { return uint16(OpOpen) }
func (Opened) frameType() uint16          { return sandboxwire.ResponseType(uint16(OpOpen)) }
func (Bind) frameType() uint16            { return uint16(OpBind) }
func (Bound) frameType() uint16           { return sandboxwire.ResponseType(uint16(OpBind)) }
func (RenewAttachment) frameType() uint16 { return uint16(OpRenewAttachment) }
func (AttachmentRenewed) frameType() uint16 {
	return sandboxwire.ResponseType(uint16(OpRenewAttachment))
}
func (CloseAttachment) frameType() uint16  { return uint16(OpCloseAttachment) }
func (CloseAccepted) frameType() uint16    { return sandboxwire.ResponseType(uint16(OpCloseAttachment)) }
func (AttachmentClosed) frameType() uint16 { return EventAttachmentClosed }
func (f Failure) frameType() uint16        { return sandboxwire.ResponseType(uint16(f.Op)) }

// Encode validates m and returns its frame. Requests and responses need a
// nonzero requestID; an event needs zero.
func Encode(requestID uint64, m Message) (sandboxwire.Frame, error) {
	kind, err := tags.Classify(m.frameType())
	if err != nil {
		return sandboxwire.Frame{}, err
	}
	if (kind == sandboxwire.KindEvent) == sandboxwire.ValidRequestID(requestID) {
		return sandboxwire.Frame{}, fmt.Errorf("%w: request ID %d for message type %#04x", sandboxwire.ErrMalformed, requestID, m.frameType())
	}
	if err := m.validate(); err != nil {
		return sandboxwire.Frame{}, err
	}
	var e sandboxwire.Encoder
	if kind == sandboxwire.KindResponse {
		if _, failed := m.(Failure); failed {
			e.Enum(resultFailure)
		} else {
			e.Enum(resultSuccess)
		}
	}
	m.encode(&e)
	return sandboxwire.Frame{Type: m.frameType(), RequestID: requestID, Payload: e.Payload()}, nil
}

// Decode returns the message a frame carries. It rejects unknown tags, a
// request ID that does not fit the tag, invalid values and trailing bytes with
// errors wrapping sandboxwire.ErrMalformed. A Hello of another version returns
// an *Error with VersionMismatch.
func Decode(f sandboxwire.Frame) (Message, error) {
	kind, err := tags.Classify(f.Type)
	if err != nil {
		return nil, err
	}
	if (kind == sandboxwire.KindEvent) == sandboxwire.ValidRequestID(f.RequestID) {
		return nil, fmt.Errorf("%w: request ID %d for message type %#04x", sandboxwire.ErrMalformed, f.RequestID, f.Type)
	}
	r := &reader{d: sandboxwire.NewDecoder(f.Payload)}
	var m Message
	switch kind {
	case sandboxwire.KindRequest:
		m = decodeRequest(r, Op(f.Type))
	case sandboxwire.KindResponse:
		op := Op(f.Type &^ sandboxwire.ResponseType(0))
		if r.enum(func(v uint16) bool { return v == resultSuccess || v == resultFailure }) == resultFailure {
			m = Failure{Op: op, Code: Code(r.enum(func(v uint16) bool { return Code(v).Valid() })), Effect: r.effect()}
		} else {
			m = decodeSuccess(r, op)
		}
	case sandboxwire.KindEvent:
		m = AttachmentClosed{AttachmentID: r.id(), Reason: CloseReason(r.enum(func(v uint16) bool { return CloseReason(v).Valid() }))}
	}
	if r.otherVersion {
		return nil, Fail(VersionMismatch)
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

// ReadMessage reads and decodes one frame no larger than maxPayload. The
// request ID is returned whenever a frame was read, even if it failed to
// decode, so a reader can answer it.
func ReadMessage(r io.Reader, maxPayload uint32) (uint64, Message, error) {
	f, err := sandboxwire.ReadFrame(r, maxPayload)
	if err != nil {
		return 0, nil, err
	}
	m, err := Decode(f)
	return f.RequestID, m, err
}

func decodeRequest(r *reader, op Op) Message {
	switch op {
	case OpHello:
		if r.u16() != Version && r.err == nil {
			// Nothing after the version is readable in another version.
			r.otherVersion = true
			return nil
		}
		if Role(r.enum(func(v uint16) bool { return Role(v) == RoleServe || Role(v) == RoleAttach })) == RoleServe {
			h := ServeHello{Version: Version, Credential: r.bytes(), Resource: r.resource(), ServerInstanceID: r.id()}
			n := r.count(uint32(ServiceNetwork))
			for range n {
				h.Services = append(h.Services, ServiceVersion{Service: r.service(), Version: r.u16()})
			}
			return h
		}
		return AttachHello{Version: Version, RuntimeID: r.id(), Credential: r.bytes()}
	case OpOpen:
		o := Open{Service: r.service(), Version: r.u16(), Resource: r.resource()}
		if r.present() {
			o.ExpectedServerInstanceID = r.id()
		}
		o.AttachmentID, o.SessionID, o.AssignmentID = r.id(), r.id(), r.id()
		o.AssignmentEpoch, o.AttachGrant = r.u64(), r.bytes()
		return o
	case OpBind:
		b := Bind{AttachmentID: r.id(), Service: r.service(), Version: r.u16(), SessionID: r.id(), AssignmentID: r.id(),
			AssignmentEpoch: r.u64(), LeaseExpiresAt: r.time(), ExpectedServerInstanceID: r.id(), MaxFrameBytes: r.u32()}
		if r.present() != (b.Service == ServiceFile) && r.err == nil {
			r.err = invalid("exports presence does not match service %s", b.Service)
		}
		if b.Service == ServiceFile {
			for range r.count(MaxExports) {
				b.Exports = append(b.Exports, ExportGrant{ID: ExportID(r.bytes()), ReadOnly: r.boolean()})
			}
		}
		if r.present() != (b.Service == ServiceNetwork) && r.err == nil {
			r.err = invalid("egress presence does not match service %s", b.Service)
		}
		if b.Service == ServiceNetwork {
			for range r.count(MaxEgressRules) {
				b.Egress = append(b.Egress, r.egressRule())
			}
		}
		return b
	case OpRenewAttachment:
		return RenewAttachment{AttachmentID: r.id(), AttachGrant: r.bytes()}
	default: // OpCloseAttachment; Classify admits no other request tag.
		return CloseAttachment{AttachmentID: r.id()}
	}
}

func decodeSuccess(r *reader, op Op) Message {
	switch op {
	case OpHello:
		return HelloAccepted{LinkID: r.id(), MaxStreams: r.u32(), MaxFrameBytes: r.u32()}
	case OpOpen:
		return Opened{AttachmentID: r.id(), ServerInstanceID: r.id(), LeaseExpiresAt: r.time(), MaxFrameBytes: r.u32()}
	case OpBind:
		return Bound{}
	case OpRenewAttachment:
		return AttachmentRenewed{AttachmentID: r.id(), LeaseExpiresAt: r.time()}
	default: // OpCloseAttachment
		return CloseAccepted{}
	}
}

func (h ServeHello) encode(e *sandboxwire.Encoder) {
	e.U16(h.Version)
	e.Enum(uint16(RoleServe))
	e.Bytes(h.Credential)
	encodeResource(e, h.Resource)
	e.ID(h.ServerInstanceID)
	e.Count(len(h.Services))
	for _, s := range h.Services {
		e.Enum(uint16(s.Service))
		e.U16(s.Version)
	}
}

func (h AttachHello) encode(e *sandboxwire.Encoder) {
	e.U16(h.Version)
	e.Enum(uint16(RoleAttach))
	e.ID(h.RuntimeID)
	e.Bytes(h.Credential)
}

func (a HelloAccepted) encode(e *sandboxwire.Encoder) {
	e.ID(a.LinkID)
	e.U32(a.MaxStreams)
	e.U32(a.MaxFrameBytes)
}

func (o Open) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(o.Service))
	e.U16(o.Version)
	encodeResource(e, o.Resource)
	e.Present(!o.ExpectedServerInstanceID.IsZero())
	if !o.ExpectedServerInstanceID.IsZero() {
		e.ID(o.ExpectedServerInstanceID)
	}
	e.ID(o.AttachmentID)
	e.ID(o.SessionID)
	e.ID(o.AssignmentID)
	e.U64(o.AssignmentEpoch)
	e.Bytes(o.AttachGrant)
}

func (o Opened) encode(e *sandboxwire.Encoder) {
	e.ID(o.AttachmentID)
	e.ID(o.ServerInstanceID)
	e.I64(o.LeaseExpiresAt.UnixMilli())
	e.U32(o.MaxFrameBytes)
}

func (b Bind) encode(e *sandboxwire.Encoder) {
	e.ID(b.AttachmentID)
	e.Enum(uint16(b.Service))
	e.U16(b.Version)
	e.ID(b.SessionID)
	e.ID(b.AssignmentID)
	e.U64(b.AssignmentEpoch)
	e.I64(b.LeaseExpiresAt.UnixMilli())
	e.ID(b.ExpectedServerInstanceID)
	e.U32(b.MaxFrameBytes)
	e.Present(b.Service == ServiceFile)
	if b.Service == ServiceFile {
		e.Count(len(b.Exports))
		for _, g := range b.Exports {
			e.Bytes([]byte(g.ID))
			e.Bool(g.ReadOnly)
		}
	}
	e.Present(b.Service == ServiceNetwork)
	if b.Service == ServiceNetwork {
		e.Count(len(b.Egress))
		for _, rule := range b.Egress {
			a := rule.Prefix.Addr()
			if a.Is4() {
				e.Enum(uint16(FamilyIPv4))
				v := a.As4()
				e.U32(binary.BigEndian.Uint32(v[:]))
			} else {
				e.Enum(uint16(FamilyIPv6))
				v := a.As16()
				e.U64(binary.BigEndian.Uint64(v[:8]))
				e.U64(binary.BigEndian.Uint64(v[8:]))
			}
			e.U8(uint8(rule.Prefix.Bits()))
			e.U16(rule.PortFirst)
			e.U16(rule.PortLast)
		}
	}
}

func (Bound) encode(*sandboxwire.Encoder) {}

func (r RenewAttachment) encode(e *sandboxwire.Encoder) {
	e.ID(r.AttachmentID)
	e.Bytes(r.AttachGrant)
}

func (r AttachmentRenewed) encode(e *sandboxwire.Encoder) {
	e.ID(r.AttachmentID)
	e.I64(r.LeaseExpiresAt.UnixMilli())
}

func (c CloseAttachment) encode(e *sandboxwire.Encoder) { e.ID(c.AttachmentID) }

func (CloseAccepted) encode(*sandboxwire.Encoder) {}

func (c AttachmentClosed) encode(e *sandboxwire.Encoder) {
	e.ID(c.AttachmentID)
	e.Enum(uint16(c.Reason))
}

func (f Failure) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(f.Code))
	e.Effect(f.Effect)
}

func encodeResource(e *sandboxwire.Encoder, r ResourceRef) {
	e.ID(r.TenantID)
	e.ID(r.EnvironmentID)
	e.Enum(uint16(r.Kind))
	e.ID(r.ID)
	e.U64(r.Generation)
}

// Validators. Decoding already rejects zero IDs and unknown enum values; these
// also hold for encoding.

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{sandboxwire.ErrMalformed}, args...)...)
}

func checkSecret(name string, b []byte, max int) error {
	if len(b) == 0 || len(b) > max {
		return invalid("%s length %d outside 1..%d", name, len(b), max)
	}
	return nil
}

func checkLease(t time.Time) error {
	if t.UnixMilli() <= 0 {
		return invalid("lease expiry %d ms", t.UnixMilli())
	}
	return nil
}

func checkFrameBytes(n uint32) error {
	if n < MaxMessageBytes || n > sandboxwire.MaxPayload {
		return invalid("max frame bytes %d outside %d..%d", n, MaxMessageBytes, sandboxwire.MaxPayload)
	}
	return nil
}

func checkService(s Service, version uint16) error {
	if !s.Valid() || version == 0 {
		return invalid("service %d version %d", s, version)
	}
	return nil
}

func checkIDs(ids ...sandboxwire.ID) error {
	for _, id := range ids {
		if id.IsZero() {
			return invalid("zero identifier")
		}
	}
	return nil
}

// checkExports requires export grants for ServiceFile and admits none for
// other services: 1 to MaxExports grants of valid, distinct IDs.
func checkExports(s Service, grants []ExportGrant) error {
	if s != ServiceFile {
		if len(grants) != 0 {
			return invalid("exports on a %s binding", s)
		}
		return nil
	}
	if len(grants) == 0 || len(grants) > MaxExports {
		return invalid("%d exports", len(grants))
	}
	seen := make(map[ExportID]bool, len(grants))
	for _, g := range grants {
		if !g.ID.Valid() || seen[g.ID] {
			return invalid("export %q invalid or repeated", g.ID)
		}
		seen[g.ID] = true
	}
	return nil
}

// checkEgress admits egress rules only for ServiceNetwork. Each rule is a
// masked prefix with 1 <= PortFirst <= PortLast, and no rule repeats.
func checkEgress(s Service, rules []EgressRule) error {
	if s != ServiceNetwork && len(rules) != 0 {
		return invalid("egress rules on a %s binding", s)
	}
	if len(rules) > MaxEgressRules {
		return invalid("%d egress rules", len(rules))
	}
	seen := make(map[EgressRule]bool, len(rules))
	for _, rule := range rules {
		p := rule.Prefix
		if !p.IsValid() || p.Masked() != p || rule.PortFirst == 0 || rule.PortFirst > rule.PortLast {
			return invalid("egress rule %s ports %d-%d", p, rule.PortFirst, rule.PortLast)
		}
		if seen[rule] {
			return invalid("duplicate egress rule %s ports %d-%d", p, rule.PortFirst, rule.PortLast)
		}
		seen[rule] = true
	}
	return nil
}

func (r ResourceRef) validate() error {
	if !r.Kind.Valid() || r.Generation == 0 {
		return invalid("resource kind %d generation %d", r.Kind, r.Generation)
	}
	return checkIDs(r.TenantID, r.EnvironmentID, r.ID)
}

func (h ServeHello) validate() error {
	if h.Version != Version {
		return invalid("hello version %d", h.Version)
	}
	if len(h.Services) == 0 || len(h.Services) > int(ServiceNetwork) {
		return invalid("%d services", len(h.Services))
	}
	seen := map[Service]bool{}
	for _, s := range h.Services {
		if err := checkService(s.Service, s.Version); err != nil {
			return err
		}
		if seen[s.Service] {
			return invalid("duplicate service %s", s.Service)
		}
		seen[s.Service] = true
	}
	if err := checkSecret("credential", h.Credential, MaxCredentialBytes); err != nil {
		return err
	}
	if err := h.Resource.validate(); err != nil {
		return err
	}
	return checkIDs(h.ServerInstanceID)
}

func (h AttachHello) validate() error {
	if h.Version != Version {
		return invalid("hello version %d", h.Version)
	}
	if err := checkSecret("credential", h.Credential, MaxCredentialBytes); err != nil {
		return err
	}
	return checkIDs(h.RuntimeID)
}

func (a HelloAccepted) validate() error {
	if a.MaxStreams == 0 {
		return invalid("zero max streams")
	}
	if err := checkFrameBytes(a.MaxFrameBytes); err != nil {
		return err
	}
	return checkIDs(a.LinkID)
}

func (o Open) validate() error {
	if err := checkService(o.Service, o.Version); err != nil {
		return err
	}
	if o.AssignmentEpoch == 0 {
		return invalid("zero assignment epoch")
	}
	if err := checkSecret("attachment grant", o.AttachGrant, MaxGrantBytes); err != nil {
		return err
	}
	if err := o.Resource.validate(); err != nil {
		return err
	}
	return checkIDs(o.AttachmentID, o.SessionID, o.AssignmentID)
}

func (o Opened) validate() error {
	if err := checkLease(o.LeaseExpiresAt); err != nil {
		return err
	}
	if err := checkFrameBytes(o.MaxFrameBytes); err != nil {
		return err
	}
	return checkIDs(o.AttachmentID, o.ServerInstanceID)
}

func (b Bind) validate() error {
	if err := checkService(b.Service, b.Version); err != nil {
		return err
	}
	if b.AssignmentEpoch == 0 {
		return invalid("zero assignment epoch")
	}
	if err := checkLease(b.LeaseExpiresAt); err != nil {
		return err
	}
	if err := checkFrameBytes(b.MaxFrameBytes); err != nil {
		return err
	}
	if err := checkExports(b.Service, b.Exports); err != nil {
		return err
	}
	if err := checkEgress(b.Service, b.Egress); err != nil {
		return err
	}
	return checkIDs(b.AttachmentID, b.SessionID, b.AssignmentID, b.ExpectedServerInstanceID)
}

func (Bound) validate() error { return nil }

func (r RenewAttachment) validate() error {
	if err := checkSecret("attachment grant", r.AttachGrant, MaxGrantBytes); err != nil {
		return err
	}
	return checkIDs(r.AttachmentID)
}

func (r AttachmentRenewed) validate() error {
	if err := checkLease(r.LeaseExpiresAt); err != nil {
		return err
	}
	return checkIDs(r.AttachmentID)
}

func (c CloseAttachment) validate() error { return checkIDs(c.AttachmentID) }

func (CloseAccepted) validate() error { return nil }

func (c AttachmentClosed) validate() error {
	if !c.Reason.Valid() {
		return invalid("close reason %d", c.Reason)
	}
	return checkIDs(c.AttachmentID)
}

func (f Failure) validate() error {
	if f.Op < OpHello || f.Op > OpCloseAttachment || !f.Code.Valid() || !f.Effect.Valid() {
		return invalid("failure op %d code %d effect %d", f.Op, f.Code, f.Effect)
	}
	return nil
}

// reader decodes fields in order, keeping the first error.
type reader struct {
	d            *sandboxwire.Decoder
	err          error
	otherVersion bool // a Hello of another version
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
func (r *reader) id() sandboxwire.ID         { return read(r, r.d.ID) }
func (r *reader) bytes() []byte              { return read(r, r.d.Bytes) }
func (r *reader) present() bool              { return read(r, r.d.Present) }
func (r *reader) boolean() bool              { return read(r, r.d.Bool) }
func (r *reader) effect() sandboxwire.Effect { return read(r, r.d.Effect) }

func (r *reader) enum(valid func(uint16) bool) uint16 {
	return read(r, func() (uint16, error) { return r.d.Enum(valid) })
}

func (r *reader) count(max uint32) int {
	return read(r, func() (int, error) { return r.d.Count(max) })
}

func (r *reader) service() Service {
	return Service(r.enum(func(v uint16) bool { return Service(v).Valid() }))
}

// egressRule reads a rule; checkEgress validates it after decoding.
func (r *reader) egressRule() EgressRule {
	var a netip.Addr
	if AddressFamily(r.enum(func(v uint16) bool { return AddressFamily(v) == FamilyIPv4 || AddressFamily(v) == FamilyIPv6 })) == FamilyIPv4 {
		var v [4]byte
		binary.BigEndian.PutUint32(v[:], r.u32())
		a = netip.AddrFrom4(v)
	} else {
		var v [16]byte
		binary.BigEndian.PutUint64(v[:8], r.u64())
		binary.BigEndian.PutUint64(v[8:], r.u64())
		a = netip.AddrFrom16(v)
	}
	return EgressRule{Prefix: netip.PrefixFrom(a, int(r.u8())), PortFirst: r.u16(), PortLast: r.u16()}
}

func (r *reader) time() time.Time { return time.UnixMilli(read(r, r.d.I64)).UTC() }

func (r *reader) resource() ResourceRef {
	return ResourceRef{TenantID: r.id(), EnvironmentID: r.id(),
		Kind: ResourceKind(r.enum(func(v uint16) bool { return ResourceKind(v).Valid() })), ID: r.id(), Generation: r.u64()}
}
