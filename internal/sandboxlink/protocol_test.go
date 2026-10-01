package sandboxlink

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
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func testID(b byte) sandboxwire.ID {
	var id sandboxwire.ID
	for i := range id {
		id[i] = b
	}
	return id
}

var (
	testResource = ResourceRef{TenantID: testID(0x01), EnvironmentID: testID(0x02), Kind: ResourceAllocation, ID: testID(0x03), Generation: 2}
	testLease    = time.UnixMilli(1790000000000).UTC()
	testExports  = []ExportGrant{{ID: "world"}, {ID: "logs", ReadOnly: true}}
	testEgress   = []EgressRule{
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), PortFirst: 443, PortLast: 443},
		{Prefix: netip.MustParsePrefix("2001:db8::/32"), PortFirst: 1, PortLast: 65535},
	}
)

type golden struct {
	requestID uint64
	m         Message
}

// goldenFrames are the frames in testdata/link_v1.hex, in order.
var goldenFrames = []golden{
	{1, ServeHello{Version: 1, Credential: []byte("serve"), Resource: testResource, ServerInstanceID: testID(0x05),
		Services: []ServiceVersion{{ServiceFile, 1}, {ServiceNetwork, 1}}}},
	{1, AttachHello{Version: 1, RuntimeID: testID(0x06), Credential: []byte("runtime")}},
	{1, HelloAccepted{LinkID: testID(0x0a), MaxStreams: 256, MaxFrameBytes: 1 << 20}},
	{1, Open{Service: ServiceFile, Version: 1, Resource: testResource, ExpectedServerInstanceID: testID(0x05), AttachmentID: testID(0x07),
		SessionID: testID(0x08), AssignmentID: testID(0x09), AssignmentEpoch: 3, AttachGrant: []byte("grant")}},
	{1, Opened{AttachmentID: testID(0x07), ServerInstanceID: testID(0x05), LeaseExpiresAt: testLease, MaxFrameBytes: 1 << 20}},
	{1, Failure{Op: OpOpen, Code: StaleGeneration, Effect: sandboxwire.EffectNone}},
	{1, Bind{AttachmentID: testID(0x07), Service: ServiceFile, Version: 1, SessionID: testID(0x08), AssignmentID: testID(0x09),
		AssignmentEpoch: 3, LeaseExpiresAt: testLease, ExpectedServerInstanceID: testID(0x05), MaxFrameBytes: 1 << 20, Exports: testExports}},
	{1, Bind{AttachmentID: testID(0x07), Service: ServiceNetwork, Version: 1, SessionID: testID(0x08), AssignmentID: testID(0x09),
		AssignmentEpoch: 3, LeaseExpiresAt: testLease, ExpectedServerInstanceID: testID(0x05), MaxFrameBytes: 1 << 20, Egress: testEgress}},
	{1, Bound{}},
	{0, AttachmentClosed{AttachmentID: testID(0x07), Reason: CloseLeaseExpired}},
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
	want := readHexFixture(t, "testdata/link_v1.hex")
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
		id, m, err := ReadMessage(r, sandboxwire.MaxPayload)
		if err != nil || id != g.requestID || !reflect.DeepEqual(m, g.m) {
			t.Fatalf("decoded %d %#v %v, want %d %#v", id, m, err, g.requestID, g.m)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("%d bytes left", r.Len())
	}
}

// frameOf returns the golden frame i with its payload changed by edit.
func frameOf(t *testing.T, i int, edit func(p []byte) []byte) sandboxwire.Frame {
	f, err := Encode(goldenFrames[i].requestID, goldenFrames[i].m)
	if err != nil {
		t.Fatal(err)
	}
	f.Payload = edit(bytes.Clone(f.Payload))
	return f
}

func TestDecodeRejects(t *testing.T) {
	// Offsets into the Bind payloads: Service at 16 and exports presence at
	// 88. In the file Bind (golden frame 6) the export count ends at 92, the
	// first export runs from 93 to 102 with its ID at 97, and egress presence
	// is at 112. In the network Bind (golden frame 7) egress presence is at 89,
	// the rule count ends at 93, and the first rule's family is at 94, address
	// at 96, prefix length at 100 and ports at 101 and 103.
	set := func(off int, v ...byte) func([]byte) []byte {
		return func(p []byte) []byte { copy(p[off:], v); return p }
	}
	cases := []struct {
		name  string
		frame int
		edit  func([]byte) []byte
	}{
		{"trailing byte", 3, func(p []byte) []byte { return append(p, 0) }},
		{"unknown service", 3, set(0, 0, 9)},
		{"duplicate service", 0, func(p []byte) []byte { copy(p[len(p)-4:], p[len(p)-8:len(p)-4]); return p }},
		{"file without exports", 7, set(16, 0, 1)},
		{"exports on network", 6, set(16, 0, 3)},
		{"empty exports", 6, func(p []byte) []byte {
			p[92] = 0
			return append(p[:93], 0)
		}},
		{"invalid export ID", 6, set(97, 'W')},
		{"duplicate export", 6, func(p []byte) []byte {
			q := append(bytes.Clone(p[:112]), p[93:103]...)
			q[92] = 3
			return append(q, 0)
		}},
		{"host bits set", 7, set(99, 1)},
		{"prefix too long", 7, set(100, 33)},
		{"unknown family", 7, set(94, 0, 3)},
		{"zero first port", 7, set(101, 0, 0)},
		{"first port above last", 7, set(101, 0x01, 0xbc)},
		{"duplicate rule", 7, func(p []byte) []byte {
			p[93] = 3
			return append(p, p[94:105]...)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Decode(frameOf(t, c.frame, c.edit)); !errors.Is(err, sandboxwire.ErrMalformed) {
				t.Fatalf("decode: %v, want ErrMalformed", err)
			}
		})
	}

	hello := frameOf(t, 1, set(0, 0, 2))
	if _, err := Decode(hello); !errors.Is(err, VersionMismatch) {
		t.Fatalf("hello of version 2: %v, want VersionMismatch", err)
	}
}

// Exports belong to file bindings and egress to network bindings only.
func TestGrantsMatchService(t *testing.T) {
	file, network := goldenFrames[6].m.(Bind), goldenFrames[7].m.(Bind)
	file.Egress, network.Exports = testEgress, testExports
	for _, b := range []Bind{file, network} {
		if _, err := Encode(1, b); !errors.Is(err, sandboxwire.ErrMalformed) {
			t.Fatalf("%s Bind with the other service's grants: %v", b.Service, err)
		}
	}
	open := goldenFrames[3].m.(Open)
	auth := Authorization{Identity: open.Identity(), Service: ServiceFile, LeaseExpiresAt: testLease, Exports: testExports}
	if err := auth.Validate(&open); err != nil {
		t.Fatalf("file authorization: %v", err)
	}
	auth.Egress = testEgress
	if err := auth.Validate(&open); !errors.Is(err, sandboxwire.ErrMalformed) {
		t.Fatalf("file authorization with egress: %v", err)
	}
}

func TestCheckRelayURL(t *testing.T) {
	for _, u := range []string{"wss://core.example.com/api/v1/sandbox-link", "ws://127.0.0.1:8080/link", "ws://localhost/link", "ws://[::1]:9/link"} {
		if err := CheckRelayURL(u); err != nil {
			t.Errorf("CheckRelayURL(%q): %v", u, err)
		}
	}
	for _, u := range []string{"ws://core.example.com/link", "http://127.0.0.1/link", "wss://user@core.example.com/link", "wss://core.example.com/link?token=x", "wss:///link"} {
		if err := CheckRelayURL(u); !errors.Is(err, ErrRelayURL) {
			t.Errorf("CheckRelayURL(%q): %v, want ErrRelayURL", u, err)
		}
	}
}

// FuzzDecode decodes arbitrary frames. Whatever decodes re-encodes to the same
// bytes; every failure is ErrMalformed or, for a Hello, VersionMismatch.
func FuzzDecode(f *testing.F) {
	golden := readHexFixture(f, "testdata/link_v1.hex")
	for r := bytes.NewReader(golden); r.Len() > 0; {
		start := len(golden) - r.Len()
		if _, err := sandboxwire.ReadFrame(r, sandboxwire.MaxPayload); err != nil {
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
			if !errors.Is(err, sandboxwire.ErrMalformed) && !errors.Is(err, VersionMismatch) {
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
