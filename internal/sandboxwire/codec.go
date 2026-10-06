package sandboxwire

import (
	"encoding/binary"
	"fmt"
)

// Encoder appends primitives to a payload. The zero value is ready to use.
type Encoder struct {
	b []byte
}

// Payload returns the encoded bytes.
func (e *Encoder) Payload() []byte { return e.b }

func (e *Encoder) U8(v uint8)   { e.b = append(e.b, v) }
func (e *Encoder) U16(v uint16) { e.b = binary.BigEndian.AppendUint16(e.b, v) }
func (e *Encoder) U32(v uint32) { e.b = binary.BigEndian.AppendUint32(e.b, v) }
func (e *Encoder) U64(v uint64) { e.b = binary.BigEndian.AppendUint64(e.b, v) }
func (e *Encoder) I64(v int64)  { e.U64(uint64(v)) }

// Bool writes 1 for true and 0 for false.
func (e *Encoder) Bool(v bool) {
	if v {
		e.U8(1)
	} else {
		e.U8(0)
	}
}

// Bytes writes a uint32 length followed by v.
func (e *Encoder) Bytes(v []byte) {
	e.U32(uint32(len(v)))
	e.b = append(e.b, v...)
}

// Count writes the uint32 element count of an array.
func (e *Encoder) Count(n int) { e.U32(uint32(n)) }

// Present writes the presence byte of an optional field; the value follows only
// when ok.
func (e *Encoder) Present(ok bool) { e.Bool(ok) }

// Enum writes a uint16 enum value. Zero is invalid on the wire.
func (e *Encoder) Enum(v uint16) { e.U16(v) }

func (e *Encoder) ID(id ID) { e.b = append(e.b, id[:]...) }

func (e *Encoder) Effect(v Effect) { e.Enum(uint16(v)) }

// Decoder reads primitives from a payload. The first error sticks: every later
// read returns the zero value, and Err and Finish return that error. Its own
// errors wrap ErrMalformed.
type Decoder struct {
	b   []byte
	err error
}

func NewDecoder(payload []byte) *Decoder { return &Decoder{b: payload} }

// Err returns the first error.
func (d *Decoder) Err() error { return d.err }

// Fail records err unless an earlier error is recorded. A protocol reports its
// own decoding rules through it.
func (d *Decoder) Fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// Finish returns the first error, or an error when bytes remain unread.
func (d *Decoder) Finish() error {
	if len(d.b) != 0 {
		d.Fail(fmt.Errorf("%w: %d trailing bytes", ErrMalformed, len(d.b)))
	}
	return d.err
}

// take returns the next n bytes, or nil after an error.
func (d *Decoder) take(n uint64, what string) []byte {
	if d.err != nil {
		return nil
	}
	if n > uint64(len(d.b)) {
		d.err = fmt.Errorf("%w: %s needs %d bytes, %d remain", ErrMalformed, what, n, len(d.b))
		return nil
	}
	v := d.b[:n:n]
	d.b = d.b[n:]
	return v
}

// unsigned reads an n-byte big-endian integer.
func (d *Decoder) unsigned(n uint64, what string) uint64 {
	var v uint64
	for _, b := range d.take(n, what) {
		v = v<<8 | uint64(b)
	}
	return v
}

func (d *Decoder) U8() uint8   { return uint8(d.unsigned(1, "u8")) }
func (d *Decoder) U16() uint16 { return uint16(d.unsigned(2, "u16")) }
func (d *Decoder) U32() uint32 { return uint32(d.unsigned(4, "u32")) }
func (d *Decoder) U64() uint64 { return d.unsigned(8, "u64") }
func (d *Decoder) I64() int64  { return int64(d.U64()) }

// Bool accepts exactly 0 or 1.
func (d *Decoder) Bool() bool {
	v := d.U8()
	if v > 1 {
		d.Fail(fmt.Errorf("%w: boolean byte %d", ErrMalformed, v))
	}
	return v == 1
}

// Bytes reads a uint32 length and that many bytes. The result aliases the
// payload, with its capacity clipped to its length.
func (d *Decoder) Bytes() []byte { return d.take(uint64(d.U32()), "byte string") }

// Count reads an array's uint32 element count. The count is at most max and at
// most the remaining bytes, because every element encodes to at least one byte,
// so a caller may allocate count elements. It is zero after an error.
func (d *Decoder) Count(max uint32) int {
	switch n := d.U32(); {
	case n > max:
		d.Fail(fmt.Errorf("%w: count %d exceeds maximum %d", ErrMalformed, n, max))
	case uint64(n) > uint64(len(d.b)):
		d.Fail(fmt.Errorf("%w: count %d exceeds %d remaining bytes", ErrMalformed, n, len(d.b)))
	default:
		return int(n)
	}
	return 0
}

// Present reads the presence byte of an optional field.
func (d *Decoder) Present() bool { return d.Bool() }

// Enum reads a uint16 enum value, rejecting zero and values valid does not
// accept.
func (d *Decoder) Enum(valid func(uint16) bool) uint16 {
	v := d.U16()
	if v == 0 || !valid(v) {
		d.Fail(fmt.Errorf("%w: enum value %d", ErrMalformed, v))
	}
	return v
}

// ID reads a required identifier, rejecting the zero ID.
func (d *Decoder) ID() ID {
	var id ID
	copy(id[:], d.take(uint64(len(id)), "identifier"))
	if id.IsZero() {
		d.Fail(fmt.Errorf("%w: zero identifier", ErrMalformed))
	}
	return id
}

func (d *Decoder) Effect() Effect {
	return Effect(d.Enum(func(v uint16) bool { return Effect(v).Valid() }))
}
