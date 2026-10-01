package sandboxnet

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

var goldenFrames = []struct {
	requestID uint64
	m         Message
}{
	{1, ConnectRequest{Network: NetworkTCP, Host: "example.com", Port: 443, TimeoutMillis: 10_000}},
	{1, ConnectResponse{Result: ResultConnected}},
	{1, ConnectResponse{Result: ResultFailed, Code: CodeDenied, Effect: sandboxwire.EffectNone}},
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

func TestGolden(t *testing.T) {
	want := readHexFixture(t, "testdata/network_v1.hex")
	var buf bytes.Buffer
	for _, g := range goldenFrames {
		if err := WriteMessage(&buf, g.requestID, g.m); err != nil {
			t.Fatalf("encode %T: %v", g.m, err)
		}
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("encoded frames differ:\n got %x\nwant %x", buf.Bytes(), want)
	}
	r := bytes.NewReader(want)
	for _, g := range goldenFrames {
		id, m, err := ReadMessage(r)
		if err != nil || id != g.requestID || !reflect.DeepEqual(m, g.m) {
			t.Fatalf("decoded %d %#v %v, want %d %#v", id, m, err, g.requestID, g.m)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("%d bytes left", r.Len())
	}
}

func TestDecodeRejects(t *testing.T) {
	frameOf := func(i int, edit func(p []byte) []byte) sandboxwire.Frame {
		f, err := Encode(goldenFrames[i].requestID, goldenFrames[i].m)
		if err != nil {
			t.Fatal(err)
		}
		f.Payload = edit(bytes.Clone(f.Payload))
		return f
	}
	// Offsets into the Connect payload: Network at 0, host length at 2, host
	// at 6, Port at 17 and TimeoutMillis at 19.
	set := func(off int, v ...byte) func([]byte) []byte {
		return func(p []byte) []byte { copy(p[off:], v); return p }
	}
	same := func(p []byte) []byte { return p }
	cases := []struct {
		name  string
		frame sandboxwire.Frame
	}{
		{"trailing byte", frameOf(0, func(p []byte) []byte { return append(p, 0) })},
		{"unknown network", frameOf(0, set(0, 0, 2))},
		{"host past the payload", frameOf(0, set(2, 0, 0, 0, 0xff))},
		{"NUL in host", frameOf(0, set(6, 0))},
		{"zero port", frameOf(0, set(17, 0, 0))},
		{"zero timeout", frameOf(0, set(19, 0, 0, 0, 0))},
		{"timeout above the maximum", frameOf(0, set(19, 0, 0, 0xea, 0x61))},
		{"unknown result", frameOf(1, set(0, 0, 3))},
		{"connected with a code", frameOf(1, func(p []byte) []byte { return append(p, 0, 3, 0, 1) })},
		{"unknown code", frameOf(2, set(2, 0, 13))},
		{"unknown effect", frameOf(2, set(4, 0, 3))},
		{"zero request ID", func() sandboxwire.Frame { f := frameOf(0, same); f.RequestID = 0; return f }()},
		{"unknown tag", func() sandboxwire.Frame { f := frameOf(0, same); f.Type = 2; return f }()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Decode(c.frame); !errors.Is(err, sandboxwire.ErrMalformed) {
				t.Fatalf("decode: %v, want ErrMalformed", err)
			}
		})
	}
}

func TestHostGrammar(t *testing.T) {
	label := strings.Repeat("a", 63)
	longest := strings.Join([]string{label, label, label, label[:61]}, ".")
	valid := []string{"example.com", "example.com.", "localhost", "xn--bcher-kva.example", "my_service.internal", "a-b.c1",
		"10.0.0.1", "::1", "2001:db8::1", "::ffff:10.0.0.1", longest}
	invalid := []string{"", ".", "a..b", "-a.com", "a-.com", "exa mple.com", "example.com\x00", "bücher.de", "http://example.com",
		"example.com:443", "10.0.0.1:443", "[::1]", "fe80::1%eth0", "127.1", "1.2.3.4.5", "host.123", label + "a.com", longest + "a"}
	for _, host := range valid {
		if err := (ConnectRequest{Network: NetworkTCP, Host: host, Port: 1, TimeoutMillis: 1}).validate(); err != nil {
			t.Errorf("host %q: %v, want valid", host, err)
		}
	}
	for _, host := range invalid {
		if err := (ConnectRequest{Network: NetworkTCP, Host: host, Port: 1, TimeoutMillis: 1}).validate(); err == nil {
			t.Errorf("host %q is valid, want rejected", host)
		}
	}
}

func TestEgress(t *testing.T) {
	egress := []sandboxlink.EgressRule{
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), PortFirst: 443, PortLast: 443},
		{Prefix: netip.MustParsePrefix("2001:db8::/32"), PortFirst: 1, PortLast: 65535},
		{Prefix: netip.MustParsePrefix("::/0"), PortFirst: 22, PortLast: 22},
		{Prefix: netip.MustParsePrefix("0.0.0.0/8"), PortFirst: 443, PortLast: 443},
	}
	for addr, want := range map[string]bool{
		"10.1.2.3:443":          true,
		"[::ffff:10.1.2.3]:443": true,
		"[2001:db8::1]:22":      true,
		"10.1.2.3:80":           false,
		"11.0.0.1:443":          false,
		"[fe80::1%eth0]:443":    false,
		"[::1]:22":              true,
		"[::]:22":               false,
		"[::ffff:0.0.0.0]:443":  false,
	} {
		if got := permits(egress, netip.MustParseAddrPort(addr)); got != want {
			t.Errorf("permits %s = %v, want %v", addr, got, want)
		}
	}
	if permits(nil, netip.MustParseAddrPort("10.1.2.3:443")) || permitsPort(nil, 443) {
		t.Error("empty egress permits a connection")
	}
}

func FuzzDecode(f *testing.F) {
	golden := readHexFixture(f, "testdata/network_v1.hex")
	for r := bytes.NewReader(golden); r.Len() > 0; {
		start := len(golden) - r.Len()
		if _, err := sandboxwire.ReadFrame(r, MaxMessageBytes); err != nil {
			f.Fatal(err)
		}
		f.Add(golden[start : len(golden)-r.Len()])
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, err := sandboxwire.ReadFrame(bytes.NewReader(data), MaxMessageBytes)
		if err != nil {
			if !errors.Is(err, sandboxwire.ErrMalformed) && err != io.EOF && err != io.ErrUnexpectedEOF {
				t.Fatalf("frame error %v", err)
			}
			return
		}
		m, err := Decode(fr)
		if err != nil {
			if !errors.Is(err, sandboxwire.ErrMalformed) {
				t.Fatalf("decode error %v", err)
			}
			return
		}
		var w bytes.Buffer
		if err := WriteMessage(&w, fr.RequestID, m); err != nil {
			t.Fatalf("re-encode %#v: %v", m, err)
		}
		if !bytes.HasPrefix(data, w.Bytes()) {
			t.Fatalf("round trip changed the frame:\n got %x\nwant prefix of %x", w.Bytes(), data)
		}
	})
}
