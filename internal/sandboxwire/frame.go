// Package sandboxwire holds the frame header and the primitive encoding shared
// by the File, Process and Link protocols, plus the few values more than one of
// them uses.
//
// It defines no messages. Each protocol's protocol.go owns its message tags,
// ordered payload layouts, validators and encoding. On the wire an enum is a
// uint16 whose zero value is invalid, and an optional field is a presence byte
// followed by the value.
package sandboxwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// HeaderSize is the length of the frame header: PayloadLength uint32,
	// MessageType uint16, Flags uint16 and RequestID uint64, in network byte
	// order.
	HeaderSize = 16
	// MaxPayload is the hard maximum frame payload. A protocol may bound its
	// messages lower.
	MaxPayload = 1 << 20
	// MaxChunk is the maximum file or process data chunk.
	MaxChunk = 64 << 10
)

// ErrMalformed is the single error for bytes that violate the wire rules.
// Errors from this package and from protocol decoders wrap it.
var ErrMalformed = errors.New("sandboxwire: malformed message")

// Frame is one decoded frame. RequestID is zero only for unsolicited events.
type Frame struct {
	Type      uint16
	RequestID uint64
	Payload   []byte
}

// ReadFrame reads one frame. It rejects a payload length above maxPayload
// (capped at MaxPayload) before allocating, and rejects nonzero flags. It
// returns io.EOF only when r ends before the first header byte.
func ReadFrame(r io.Reader, maxPayload uint32) (Frame, error) {
	var h [HeaderSize]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Frame{}, err
	}
	n, limit := binary.BigEndian.Uint32(h[0:4]), min(maxPayload, MaxPayload)
	if n > limit {
		return Frame{}, fmt.Errorf("%w: payload length %d exceeds limit %d", ErrMalformed, n, limit)
	}
	if flags := binary.BigEndian.Uint16(h[6:8]); flags != 0 {
		return Frame{}, fmt.Errorf("%w: frame flags %#04x", ErrMalformed, flags)
	}
	f := Frame{
		Type:      binary.BigEndian.Uint16(h[4:6]),
		RequestID: binary.BigEndian.Uint64(h[8:16]),
		Payload:   make([]byte, n),
	}
	if _, err := io.ReadFull(r, f.Payload); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}
	return f, nil
}

// WriteFrame writes f with a single Write call, so frames written by goroutines
// sharing w under a mutex never interleave.
func WriteFrame(w io.Writer, f Frame) error {
	if len(f.Payload) > MaxPayload {
		return fmt.Errorf("%w: payload length %d exceeds limit %d", ErrMalformed, len(f.Payload), MaxPayload)
	}
	b := make([]byte, HeaderSize+len(f.Payload))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(f.Payload)))
	binary.BigEndian.PutUint16(b[4:6], f.Type)
	binary.BigEndian.PutUint64(b[8:16], f.RequestID)
	copy(b[HeaderSize:], f.Payload)
	_, err := w.Write(b)
	return err
}

const responseBit = 0x8000

// FirstEvent is the first event tag. A protocol numbers its events upward from
// it in the order it lists them.
const FirstEvent = 0x4001

// ResponseType returns the tag of the response to request tag req.
func ResponseType(req uint16) uint16 { return req | responseBit }

// IsResponse reports whether t is a response tag.
func IsResponse(t uint16) bool { return t&responseBit != 0 }

// ValidRequestID reports whether id may identify a request. Zero is reserved
// for unsolicited events.
func ValidRequestID(id uint64) bool { return id != 0 }

// RequestSequence holds the request ID rule: each sender's RequestIDs on one
// stream strictly increase in wire order, so uniqueness holds in constant
// memory. A sender and a receiver each keep their own. The zero value starts
// before 1.
// It is not safe for concurrent use: the caller serializes it with the frame
// writes or reads it orders.
type RequestSequence struct{ last uint64 }

// Next returns the next ID for a sender. Callers invoke it inside the same
// critical section that writes the frame.
func (s *RequestSequence) Next() uint64 {
	s.last++
	return s.last
}

// Admit reports whether id is valid and greater than the last admitted id.
// A receiver rejects an ID it does not admit as a protocol violation, with no
// dispatch.
func (s *RequestSequence) Admit(id uint64) bool {
	if !ValidRequestID(id) || id <= s.last {
		return false
	}
	s.last = id
	return true
}

// Kind is the role of a message type.
type Kind uint8

const (
	KindRequest Kind = iota + 1
	KindResponse
	KindEvent
)

// Tags is a protocol's known message types: requests 1 through Requests, their
// responses, and events FirstEvent through FirstEvent+Events-1. Requests stays
// below FirstEvent.
type Tags struct {
	Requests uint16
	Events   uint16
}

// Classify returns the kind of message type t, or ErrMalformed when t is not
// one of the known tags.
func (k Tags) Classify(t uint16) (Kind, error) {
	switch {
	case IsResponse(t):
		if req := t &^ responseBit; req >= 1 && req <= k.Requests {
			return KindResponse, nil
		}
	case t >= FirstEvent:
		if t-FirstEvent < k.Events {
			return KindEvent, nil
		}
	case t >= 1 && t <= k.Requests:
		return KindRequest, nil
	}
	return 0, fmt.Errorf("%w: unknown message type %#04x", ErrMalformed, t)
}
