package sandboxwire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

// sample exercises every primitive in the order the golden fixture lists them.
type sample struct {
	A      uint8
	B      uint16
	C      uint32
	D      uint64
	E      int64
	F      bool
	Kind   uint16
	Effect Effect
	Ref    *ID
	Items  [][]byte
}

const sampleMaxItems = 8

func sampleKind(v uint16) bool { return v <= 3 }

func (s sample) encode(e *Encoder) {
	e.U8(s.A)
	e.U16(s.B)
	e.U32(s.C)
	e.U64(s.D)
	e.I64(s.E)
	e.Bool(s.F)
	e.Enum(s.Kind)
	e.Effect(s.Effect)
	e.Present(s.Ref != nil)
	if s.Ref != nil {
		e.ID(*s.Ref)
	}
	e.Count(len(s.Items))
	for _, item := range s.Items {
		e.Bytes(item)
	}
}

// get runs read unless an earlier read failed, keeping the first error.
func get[T any](err *error, read func() (T, error)) T {
	var v T
	if *err == nil {
		v, *err = read()
	}
	return v
}

func decodeSample(t *testing.T, p []byte) (sample, error) {
	var s sample
	var err error
	d := NewDecoder(p)
	s.A = get(&err, d.U8)
	s.B = get(&err, d.U16)
	s.C = get(&err, d.U32)
	s.D = get(&err, d.U64)
	s.E = get(&err, d.I64)
	s.F = get(&err, d.Bool)
	s.Kind = get(&err, func() (uint16, error) { return d.Enum(sampleKind) })
	s.Effect = get(&err, d.Effect)
	if get(&err, d.Present) {
		id := get(&err, d.ID)
		s.Ref = &id
	}
	remaining := d.Remaining()
	n := get(&err, func() (int, error) { return d.Count(sampleMaxItems) })
	if n > sampleMaxItems || n > remaining {
		t.Fatalf("count %d escapes its bounds (max %d, %d bytes remained)", n, sampleMaxItems, remaining)
	}
	s.Items = make([][]byte, n)
	for i := range s.Items {
		s.Items[i] = get(&err, d.Bytes)
		if cap(s.Items[i]) != len(s.Items[i]) {
			t.Fatalf("byte string capacity %d exceeds its length %d", cap(s.Items[i]), len(s.Items[i]))
		}
	}
	if err != nil {
		return s, err
	}
	return s, d.Finish()
}

func readHexFixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var digits strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		line, _, _ = strings.Cut(line, "#")
		digits.WriteString(strings.Join(strings.Fields(line), ""))
	}
	b, err := hex.DecodeString(digits.String())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type countingWriter struct {
	bytes.Buffer
	writes int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.Buffer.Write(p)
}

func TestGoldenFrame(t *testing.T) {
	want := readHexFixture(t, "testdata/frame_v1.hex")
	ref := ID{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}
	msg := sample{A: 7, B: 0x0102, C: 0x03040506, D: 0x0708090a0b0c0d0e, E: -2, F: true, Kind: 2, Effect: EffectPossible, Ref: &ref, Items: [][]byte{[]byte("ab"), {}}}

	var e Encoder
	msg.encode(&e)
	var w countingWriter
	if err := WriteFrame(&w, Frame{Type: ResponseType(3), RequestID: 0x0102030405060708, Payload: e.Payload()}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), want) || w.writes != 1 {
		t.Fatalf("encoded frame in %d writes:\n got %x\nwant %x", w.writes, w.Bytes(), want)
	}

	f, err := ReadFrame(bytes.NewReader(want), MaxPayload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeSample(t, f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != 0x8003 || f.RequestID != 0x0102030405060708 || !reflect.DeepEqual(got, msg) {
		t.Fatalf("decoded %#x %#x %+v, want %+v", f.Type, f.RequestID, got, msg)
	}
}

func header(length uint32, flags uint16) []byte {
	h := binary.BigEndian.AppendUint32(nil, length)
	h = binary.BigEndian.AppendUint16(h, 1)
	h = binary.BigEndian.AppendUint16(h, flags)
	return binary.BigEndian.AppendUint64(h, 1)
}

func errOf[T any](_ T, err error) error { return err }

func TestRejectsMalformed(t *testing.T) {
	anyEnum := func(uint16) bool { return true }
	tags := Tags{Requests: 2, Events: 1}
	for _, tc := range []struct {
		name string
		err  error
	}{
		// Each reader holds only a header, so these fail before any payload read.
		{"length above MaxPayload", errOf(ReadFrame(bytes.NewReader(header(MaxPayload+1, 0)), ^uint32(0)))},
		{"length above the caller's limit", errOf(ReadFrame(bytes.NewReader(header(65, 0)), 64))},
		{"nonzero flags", errOf(ReadFrame(bytes.NewReader(header(0, 1)), MaxPayload))},
		{"oversize write", WriteFrame(io.Discard, Frame{Type: 1, RequestID: 1, Payload: make([]byte, MaxPayload+1)})},
		{"trailing bytes", NewDecoder([]byte{0}).Finish()},
		{"bool 2", errOf(NewDecoder([]byte{2}).Bool())},
		{"zero enum", errOf(NewDecoder([]byte{0, 0}).Enum(anyEnum))},
		{"unknown enum", errOf(NewDecoder([]byte{0, 4}).Enum(sampleKind))},
		{"zero ID", errOf(NewDecoder(make([]byte, 16)).ID())},
		{"count above remaining bytes", errOf(NewDecoder([]byte{0, 0, 0, 2, 0}).Count(10))},
		{"count above maximum", errOf(NewDecoder([]byte{0, 0, 0, 2, 0, 0}).Count(1))},
		{"byte string above remaining bytes", errOf(NewDecoder([]byte{0, 0, 0, 2, 0}).Bytes())},
		{"truncated integer", errOf(NewDecoder([]byte{0, 0, 0}).U32())},
		{"unknown request", errOf(tags.Classify(3))},
		{"unknown response", errOf(tags.Classify(ResponseType(3)))},
		{"unknown event", errOf(tags.Classify(FirstEvent + 1))},
		{"type zero", errOf(tags.Classify(0))},
	} {
		if !errors.Is(tc.err, ErrMalformed) {
			t.Errorf("%s: got %v, want ErrMalformed", tc.name, tc.err)
		}
	}
}

func TestClassify(t *testing.T) {
	tags := Tags{Requests: 2, Events: 1}
	for typ, want := range map[uint16]Kind{2: KindRequest, ResponseType(2): KindResponse, FirstEvent: KindEvent} {
		if got, err := tags.Classify(typ); got != want || err != nil {
			t.Errorf("Classify(%#x) = %v, %v; want %v", typ, got, err, want)
		}
	}
}

func TestRequestSequence(t *testing.T) {
	var sender, receiver RequestSequence
	first, second := sender.Next(), sender.Next()
	if first != 1 || second != 2 {
		t.Fatalf("Next returned %d, %d; want 1, 2", first, second)
	}
	if !receiver.Admit(first) || !receiver.Admit(5) {
		t.Fatal("receiver refused an increasing ID")
	}
	for _, id := range []uint64{0, 3, 5} {
		if receiver.Admit(id) {
			t.Errorf("receiver admitted %d after 5", id)
		}
	}
}

// FuzzDecoder decodes a frame carrying the sample payload. Whatever decodes
// must re-encode to the same bytes, every failure must be ErrMalformed, and no
// allocation may exceed the input: the payload limit is the input length and
// the item count is bounded.
func FuzzDecoder(f *testing.F) {
	f.Add(readHexFixture(f, "testdata/frame_v1.hex"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, err := ReadFrame(bytes.NewReader(data), uint32(len(data)))
		if err != nil {
			if !errors.Is(err, ErrMalformed) && err != io.EOF && err != io.ErrUnexpectedEOF {
				t.Fatalf("frame error %v", err)
			}
			return
		}
		msg, err := decodeSample(t, fr.Payload)
		if err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("payload error %v is not ErrMalformed", err)
			}
			return
		}
		var e Encoder
		msg.encode(&e)
		var w bytes.Buffer
		if err := WriteFrame(&w, Frame{Type: fr.Type, RequestID: fr.RequestID, Payload: e.Payload()}); err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(data, w.Bytes()) {
			t.Fatalf("round trip changed the frame:\n got %x\nwant prefix of %x", w.Bytes(), data)
		}
	})
}
