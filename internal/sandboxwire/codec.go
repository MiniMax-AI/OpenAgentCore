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

// Decoder reads primitives from a payload. Every error wraps ErrMalformed;
// after an error the Decoder's position is unspecified.
type Decoder struct {
	b []byte
}

func NewDecoder(payload []byte) *Decoder { return &Decoder{b: payload} }

// Remaining returns the number of unread bytes.
func (d *Decoder) Remaining() int { return len(d.b) }

// Finish fails when bytes remain unread.
func (d *Decoder) Finish() error {
	if len(d.b) != 0 {
		return fmt.Errorf("%w: %d trailing bytes", ErrMalformed, len(d.b))
	}
	return nil
}

func (d *Decoder) take(n uint64, what string) ([]byte, error) {
	if n > uint64(len(d.b)) {
		return nil, fmt.Errorf("%w: %s needs %d bytes, %d remain", ErrMalformed, what, n, len(d.b))
	}
	v := d.b[:n:n]
	d.b = d.b[n:]
	return v, nil
}

func (d *Decoder) U8() (uint8, error) {
	b, err := d.take(1, "u8")
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (d *Decoder) U16() (uint16, error) {
	b, err := d.take(2, "u16")
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

func (d *Decoder) U32() (uint32, error) {
	b, err := d.take(4, "u32")
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (d *Decoder) U64() (uint64, error) {
	b, err := d.take(8, "u64")
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

func (d *Decoder) I64() (int64, error) {
	v, err := d.U64()
	return int64(v), err
}

// Bool accepts exactly 0 or 1.
func (d *Decoder) Bool() (bool, error) {
	v, err := d.U8()
	if err == nil && v > 1 {
		err = fmt.Errorf("%w: boolean byte %d", ErrMalformed, v)
	}
	return v == 1, err
}

// Bytes reads a uint32 length and that many bytes. The result aliases the
// payload, with its capacity clipped to its length.
func (d *Decoder) Bytes() ([]byte, error) {
	n, err := d.U32()
	if err != nil {
		return nil, err
	}
	return d.take(uint64(n), "byte string")
}

// Count reads an array's uint32 element count. The count is at most max and at
// most the remaining bytes, because every element encodes to at least one byte,
// so a caller may allocate count elements.
func (d *Decoder) Count(max uint32) (int, error) {
	n, err := d.U32()
	switch {
	case err != nil:
		return 0, err
	case n > max:
		return 0, fmt.Errorf("%w: count %d exceeds maximum %d", ErrMalformed, n, max)
	case uint64(n) > uint64(len(d.b)):
		return 0, fmt.Errorf("%w: count %d exceeds %d remaining bytes", ErrMalformed, n, len(d.b))
	}
	return int(n), nil
}

// Present reads the presence byte of an optional field.
func (d *Decoder) Present() (bool, error) { return d.Bool() }

// Enum reads a uint16 enum value, rejecting zero and values valid does not
// accept.
func (d *Decoder) Enum(valid func(uint16) bool) (uint16, error) {
	v, err := d.U16()
	if err == nil && (v == 0 || !valid(v)) {
		err = fmt.Errorf("%w: enum value %d", ErrMalformed, v)
	}
	return v, err
}

// ID reads a required identifier, rejecting the zero ID.
func (d *Decoder) ID() (ID, error) {
	b, err := d.take(uint64(len(ID{})), "identifier")
	if err != nil {
		return ID{}, err
	}
	if id := ID(b); !id.IsZero() {
		return id, nil
	}
	return ID{}, fmt.Errorf("%w: zero identifier", ErrMalformed)
}

func (d *Decoder) Effect() (Effect, error) {
	v, err := d.Enum(func(v uint16) bool { return Effect(v).Valid() })
	return Effect(v), err
}
