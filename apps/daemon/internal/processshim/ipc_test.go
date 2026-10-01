package processshim

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
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
}

func readHexFixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var digits strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		line, _, _ = strings.Cut(line, "#")
		digits.WriteString(strings.Join(strings.Fields(line), ""))
	}
	frame, err := hex.DecodeString(digits.String())
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestGoldenFixtures(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			want := readHexFixture(t, fx.name)
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
			m, err := Decode(f)
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
// must re-encode to the same bytes, except a Request of another version,
// which decodes to its version alone.
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
		m, err := Decode(sandboxwire.Frame{Type: binary.BigEndian.Uint16(tag), Payload: payload})
		if err != nil {
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("error %v does not wrap ErrProtocol", err)
			}
			return
		}
		if r, ok := m.(Request); ok && r.Version != Version {
			return
		}
		if got := Frame(m).Payload; !bytes.Equal(got, payload) {
			t.Fatalf("%T re-encoded as %x, decoded from %x", m, got, payload)
		}
	})
}
