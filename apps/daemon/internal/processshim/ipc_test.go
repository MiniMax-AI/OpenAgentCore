package processshim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire/sandboxwiretest"
)

func b(s string) []byte { return []byte(s) }

var fixtures = []struct {
	name string
	msg  Message
}{
	{"request.hex", Request{
		Version:  Version,
		ExecPath: b("/.oac/bin/git"),
		Argv:     [][]byte{b("git"), b("status")},
		Env:      [][]byte{b("HOME=/home/u")},
		Cwd:      b("/work"),
		Umask:    0o022,
	}},
	{"ack.hex", Ack{}},
	{"signal.hex", Signal{Number: 2}},
	{"result_code.hex", Result{Code: 3, Message: []byte{}}},
	{"result_signal.hex", Result{Signal: 15, Message: []byte{}}},
	{"result_refused.hex", Result{Code: ExitNotFound, Message: b("not found")}},
	{"open.hex", Open{ID: 1, Request: Request{
		Version:  Version,
		ExecPath: b("/.oac/bin/sh"),
		Argv:     [][]byte{b("sh")},
		Env:      [][]byte{},
		Cwd:      b("/w"),
		Umask:    0o022,
	}, Terminal: &Terminal{
		Size:  WindowSize{Rows: 24, Cols: 80},
		Iflag: 0x500, Oflag: 0x5, Cflag: 0xbf, Lflag: 0x8a3b,
		Cc: []byte{3, 28},
	}}},
	{"input.hex", Input{ID: 1, Data: b("hi\n")}},
	{"input_end.hex", InputEnd{ID: 1}},
	{"written.hex", Written{ID: 1, FD: 1, Seq: 7}},
	{"write_failed.hex", WriteFailed{ID: 1, FD: 1, Seq: 8, Errno: 13}},
	{"signaled.hex", Signaled{ID: 1, Number: 28, Size: &WindowSize{Rows: 30, Cols: 100}}},
	{"gone.hex", Gone{ID: 2}},
	{"accept.hex", Accept{ID: 1}},
	{"started.hex", Started{ID: 1}},
	{"read.hex", Read{ID: 1, Max: 64 << 10}},
	{"stop_input.hex", StopInput{ID: 1}},
	{"output.hex", Output{ID: 1, FD: 2, Seq: 5, Data: b("err\n")}},
	{"close.hex", Close{ID: 1, FD: 1, Seq: 9}},
	{"exit.hex", Exit{ID: 1, Result: Result{Code: 3, Message: []byte{}}, Marks: []Mark{{FD: 1, Seq: 9}, {FD: 2, Seq: 6}}}},
	{"notice.hex", Notice{ID: 1, Message: b("link lost")}},
	{"end.hex", End{ID: 1}},
}

// decoders are the three decoders: shim and relay, relay to broker, and
// broker to relay.
var decoders = []func(sandboxwire.Frame) (Message, error){
	Decode,
	func(f sandboxwire.Frame) (Message, error) { return DecodeRelay(f) },
	func(f sandboxwire.Frame) (Message, error) { return DecodeBroker(f) },
}

// decoderFor picks the decoder of a message type.
func decoderFor(t uint16) func(sandboxwire.Frame) (Message, error) {
	return decoders[min(t>>4, 2)]
}

func TestGoldenFixtures(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			want := sandboxwiretest.ReadHex(t, fx.name)
			var got bytes.Buffer
			if err := sandboxwire.WriteFrame(&got, Frame(fx.msg)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("encoded\n got %x\nwant %x", got.Bytes(), want)
			}
			f, err := sandboxwire.ReadFrame(bytes.NewReader(want), MaxFrameBytes)
			if err != nil {
				t.Fatal(err)
			}
			m, err := decoderFor(f.Type)(f)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(m, fx.msg) {
				t.Fatalf("decoded\n got %#v\nwant %#v", m, fx.msg)
			}
		})
	}
}

// FuzzDecode takes a message type and a payload and runs every decoder on
// them. Whatever decodes must re-encode to the same bytes, except a Request
// of another version, which decodes to its version alone.
func FuzzDecode(f *testing.F) {
	for _, fx := range fixtures {
		fr := Frame(fx.msg)
		f.Add(binary.BigEndian.AppendUint16(nil, fr.Type), fr.Payload)
	}
	f.Add([]byte{0, 1}, []byte{0, 2})
	f.Fuzz(func(t *testing.T, tag, payload []byte) {
		if len(tag) != 2 {
			return
		}
		typ := binary.BigEndian.Uint16(tag)
		for _, decode := range decoders {
			m, err := decode(sandboxwire.Frame{Type: typ, Payload: payload})
			if err != nil {
				if !errors.Is(err, ErrProtocol) {
					t.Fatalf("error %v does not wrap ErrProtocol", err)
				}
				continue
			}
			if r, ok := m.(Request); ok && r.Version != Version {
				continue
			}
			if got := Frame(m); got.Type != typ || !bytes.Equal(got.Payload, payload) {
				t.Fatalf("%T re-encoded as %#x %x, decoded from %#x %x", m, got.Type, got.Payload, typ, payload)
			}
		}
	})
}
