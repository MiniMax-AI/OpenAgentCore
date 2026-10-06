package sandboxprocess

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire/sandboxwiretest"
)

var (
	fixtureInstance = sandboxwire.ID{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}
	fixtureOp       = sandboxwire.ID{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f}
	fixtureRef      = OperationRef{ServerInstanceID: fixtureInstance, OperationID: fixtureOp}
)

func b(s string) []byte { return []byte(s) }

var fixtures = []struct {
	name      string
	requestID uint64
	msg       Message
}{
	{"start_pipes.hex", 1, StartRequest{fixtureRef, ProcessSpec{
		Executable: b("sh"),
		Argv:       [][]byte{b("sh"), b("-c"), b("echo hi")},
		Env:        []EnvVar{{b("PATH"), b("/usr/bin:/bin")}},
		Cwd:        b("/work"),
		Umask:      0o022,
		IOMode:     IOPipes,
		Scope:      ScopePOSIXSession,
	}}},
	{"start_pty.hex", 2, StartRequest{fixtureRef, ProcessSpec{
		Executable: b("/bin/bash"),
		Argv:       [][]byte{b("-bash")},
		Env:        []EnvVar{{b("HOME"), b("/root")}},
		Cwd:        b("/"),
		Umask:      0o077,
		IOMode:     IOPTY,
		PTY: &PTYSpec{
			Size:  WindowSize{Rows: 24, Cols: 80, XPixels: 640, YPixels: 480},
			Term:  b("xterm-256color"),
			Modes: []PTYModeValue{{ModeVINTR, 3}, {ModeIUTF8, 1}, {ModeECHO, 0}},
		},
		Scope: ScopePOSIXSession,
	}}},
	{"output.hex", 0, OutputEvent{EventHeader{fixtureOp, 3}, StreamStdout, 0, b("hi\n")}},
	{"exited_code.hex", 0, ExitedEvent{EventHeader{fixtureOp, 5}, ExitStatus{Kind: ExitCode, Code: 7}}},
	{"exited_signal.hex", 0, ExitedEvent{EventHeader{fixtureOp, 5}, ExitStatus{Kind: ExitSignal, Signal: 11, CoreDumped: true}}},
	{"output_closed.hex", 0, OutputClosedEvent{EventHeader{fixtureOp, 6}, OutputDrained}},
	{"failure.hex", 1, ResponseFailure{OpStart, Failure{CodeOperationConflict, sandboxwire.EffectNone, "operation has a different spec"}}},
}

func TestGoldenFixtures(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			want := sandboxwiretest.ReadHex(t, fx.name)
			var got bytes.Buffer
			if err := sandboxwire.WriteFrame(&got, sandboxwire.Frame{Type: fx.msg.MessageType(), RequestID: fx.requestID, Payload: Encode(fx.msg)}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("encoded\n got %x\nwant %x", got.Bytes(), want)
			}
			f, err := sandboxwire.ReadFrame(bytes.NewReader(want), sandboxwire.MaxPayload)
			if err != nil || f.RequestID != fx.requestID {
				t.Fatalf("frame %+v: %v", f, err)
			}
			m, err := Decode(f.Type, f.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(m, fx.msg) {
				t.Fatalf("decoded\n got %#v\nwant %#v", m, fx.msg)
			}
		})
	}
}

// FuzzDecode takes a message type followed by a payload. Whatever decodes
// must re-encode to the same bytes, because every encoding is canonical.
func FuzzDecode(f *testing.F) {
	for _, fx := range fixtures {
		f.Add(binary.BigEndian.AppendUint16(nil, fx.msg.MessageType()), Encode(fx.msg))
	}
	for op := OpDescribe; op <= OpRelease; op++ {
		f.Add(binary.BigEndian.AppendUint16(nil, op), []byte{})
		f.Add(binary.BigEndian.AppendUint16(nil, sandboxwire.ResponseType(op)), []byte{0, 1})
	}
	f.Fuzz(func(t *testing.T, tag, payload []byte) {
		if len(tag) != 2 {
			return
		}
		m, err := Decode(binary.BigEndian.Uint16(tag), payload)
		if err != nil {
			if !errors.Is(err, sandboxwire.ErrMalformed) {
				t.Fatalf("error %v does not wrap ErrMalformed", err)
			}
			return
		}
		if got := Encode(m); !bytes.Equal(got, payload) {
			t.Fatalf("%T re-encoded as %x, decoded from %x", m, got, payload)
		}
	})
}
