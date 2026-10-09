package sandboxfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire/sandboxwiretest"
)

func treeRequest() OpenTreeRequest {
	return OpenTreeRequest{Handle: 1, Node: testNode, MaxEntries: 1000, MaxDataBytes: 20 << 20, RequireReadOnlyFiles: true}
}
func treeRecord(name string, parent uint32, kind uint32, size uint64) TreeRecord {
	return TreeRecord{Parent: parent, Name: []byte(name), Attr: Attr{Mode: kind | 0o500, Size: size}}
}

func encodeTree(t *testing.T, records []TreeRecord, bodies [][]byte) []byte {
	t.Helper()
	var total uint64
	for _, r := range records {
		if r.Attr.Mode&ModeType == ModeRegular {
			total += r.Attr.Size
		}
	}
	var b bytes.Buffer
	e, err := NewTreeEncoder(&b, treeRequest(), testCaps, uint32(len(records)), total)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range records {
		var body io.Reader
		if r.Attr.Mode&ModeType == ModeRegular {
			body = bytes.NewReader(bodies[i])
		}
		if err = e.Write(r, body); err != nil {
			t.Fatal(err)
		}
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestTreeStreamingLargeFileAndDeepDirectories(t *testing.T) {
	// This is a valid business-sized single file, larger than a transport frame.
	payload := bytes.Repeat([]byte{0xa5}, 20<<20)
	records := []TreeRecord{treeRecord("", 0, ModeDirectory, 4096)}
	bodies := [][]byte{nil}
	for i := 1; i < 999; i++ {
		records = append(records, treeRecord("directory", uint32(i-1), ModeDirectory, 4096))
		bodies = append(bodies, nil)
	}
	records = append(records, treeRecord("f", 998, ModeRegular, uint64(len(payload))))
	bodies = append(bodies, payload)
	encoded := encodeTree(t, records, bodies)
	// A stream may split every metadata primitive across reads.
	d, err := NewTreeDecoder(&smallRead{r: bytes.NewReader(encoded)}, treeRequest(), testCaps, uint64(len(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	for i := range records {
		r, body, err := d.Next()
		if err != nil {
			t.Fatal(err)
		}
		if r.Parent != records[i].Parent || !bytes.Equal(r.Name, records[i].Name) || r.Attr != records[i].Attr {
			t.Fatalf("record %d differs", i)
		}
		got, err := io.ReadAll(body)
		if err != nil || !bytes.Equal(got, bodies[i]) {
			t.Fatalf("body %d: %v", i, err)
		}
	}
	if _, _, err = d.Next(); err != io.EOF {
		t.Fatalf("finish: %v", err)
	}
}

type smallRead struct{ r io.Reader }

func (r *smallRead) Read(p []byte) (int, error) { return r.r.Read(p[:min(len(p), 701)]) }

func TestTreeRejectsInvalidStructure(t *testing.T) {
	root := treeRecord("", 0, ModeDirectory, 0)
	dir := treeRecord("a", 0, ModeDirectory, 0)
	file := treeRecord("f", 1, ModeRegular, 0)
	cases := map[string][]TreeRecord{
		"root-file":       {treeRecord("", 0, ModeRegular, 0)},
		"root-name":       {treeRecord("root", 0, ModeDirectory, 0)},
		"root-parent":     {treeRecord("", 1, ModeDirectory, 0)},
		"future-parent":   {root, treeRecord("a", 2, ModeDirectory, 0)},
		"file-parent":     {root, treeRecord("a", 0, ModeRegular, 0), treeRecord("b", 1, ModeRegular, 0)},
		"reenter-subtree": {root, dir, file, treeRecord("b", 0, ModeDirectory, 0), treeRecord("g", 1, ModeRegular, 0)},
		"duplicate":       {root, dir, dir},
		"order":           {root, treeRecord("b", 0, ModeDirectory, 0), dir},
		"symlink":         {root, treeRecord("a", 0, ModeSymlink, 0)},
		"slash":           {root, treeRecord("a/b", 0, ModeRegular, 0)},
		"dot":             {root, treeRecord("..", 0, ModeRegular, 0)},
		"nul":             {root, treeRecord("a\x00b", 0, ModeRegular, 0)},
	}
	writable := treeRecord("a", 0, ModeRegular, 0)
	writable.Attr.Mode |= 0o200
	cases["writable"] = []TreeRecord{root, writable}
	invalidAttr := dir
	invalidAttr.Attr.Mtime.Nsec = 1e9
	cases["invalid-attr"] = []TreeRecord{root, invalidAttr}
	for name, records := range cases {
		t.Run(name, func(t *testing.T) {
			// Bypass the encoder to exercise decoder safety against an untrusted peer.
			var raw sandboxwire.Encoder
			raw.U32(uint32(len(records)))
			raw.U64(0)
			for _, r := range records {
				raw.U32(r.Parent)
				raw.Bytes(r.Name)
				r.Attr.encode(&raw)
			}
			d, err := NewTreeDecoder(bytes.NewReader(raw.Payload()), treeRequest(), testCaps, uint64(len(raw.Payload())))
			if err == nil {
				for range records {
					_, body, nextErr := d.Next()
					err = nextErr
					if err != nil {
						break
					}
					_, err = io.Copy(io.Discard, body)
					if err != nil {
						break
					}
				}
				if err == nil {
					_, _, err = d.Next()
				}
			}
			if err == nil || err == io.EOF {
				t.Fatalf("accepted invalid structure: %v", err)
			}
		})
	}
}

func TestTreeRejectsCountsSizesAndTruncation(t *testing.T) {
	raw := encodeTree(t, []TreeRecord{treeRecord("", 0, ModeDirectory, 0), treeRecord("f", 0, ModeRegular, 3)}, [][]byte{nil, []byte("abc")})
	for _, tc := range []struct {
		name      string
		mutate    func([]byte) []byte
		sizeDelta int
	}{
		{"zero-count", func(b []byte) []byte { binary.BigEndian.PutUint32(b, 0); return b }, 0},
		{"excess-count", func(b []byte) []byte { binary.BigEndian.PutUint32(b, 1001); return b }, 0},
		{"excess-data", func(b []byte) []byte { binary.BigEndian.PutUint64(b[4:], 21<<20); return b }, 0},
		{"short-declared-data", func(b []byte) []byte { binary.BigEndian.PutUint64(b[4:], 2); return b }, 0},
		{"long-declared-data", func(b []byte) []byte { binary.BigEndian.PutUint64(b[4:], 4); return b }, 0},
		{"huge-name", func(b []byte) []byte { binary.BigEndian.PutUint32(b[112:], math.MaxUint32); return b }, 0},
		{"trailing", func(b []byte) []byte { return append(b, 1) }, 0},
		{"declared-short", func(b []byte) []byte { return b }, -1},
		{"declared-long", func(b []byte) []byte { return b }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.mutate(bytes.Clone(raw))
			d, err := NewTreeDecoder(bytes.NewReader(b), treeRequest(), testCaps, uint64(len(raw)+tc.sizeDelta))
			if err == nil {
				for {
					_, body, e := d.Next()
					if e != nil {
						err = e
						break
					}
					if _, err = io.Copy(io.Discard, body); err != nil {
						break
					}
				}
			}
			if err == nil || err == io.EOF {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
	for n := 0; n < len(raw); n++ {
		d, err := NewTreeDecoder(bytes.NewReader(raw[:n]), treeRequest(), testCaps, uint64(len(raw)))
		if err == nil {
			for {
				_, body, e := d.Next()
				if e != nil {
					err = e
					break
				}
				if _, err = io.Copy(io.Discard, body); err != nil {
					break
				}
			}
		}
		if err == nil || err == io.EOF {
			t.Fatalf("accepted truncation at %d: %v", n, err)
		}
	}
}

func TestTreeEncoderBodyAndTotals(t *testing.T) {
	for _, body := range []string{"ab", "abcd"} {
		var b bytes.Buffer
		e, err := NewTreeEncoder(&b, treeRequest(), testCaps, 2, 3)
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Write(treeRecord("", 0, ModeDirectory, 0), nil); err != nil {
			t.Fatal(err)
		}
		if err = e.Write(treeRecord("f", 0, ModeRegular, 3), bytes.NewBufferString(body)); err == nil {
			t.Fatal("accepted changed size")
		}
		if e.Close() == nil {
			t.Fatal("lost encoder error")
		}
	}
	e, err := NewTreeEncoder(io.Discard, treeRequest(), testCaps, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Write(treeRecord("", 0, ModeDirectory, 0), nil); err != nil {
		t.Fatal(err)
	}
	if e.Close() == nil {
		t.Fatal("accepted missing record")
	}
	raw := encodeTree(t, []TreeRecord{treeRecord("", 0, ModeDirectory, 0), treeRecord("f", 0, ModeRegular, 3)}, [][]byte{nil, []byte("abc")})
	d, err := NewTreeDecoder(bytes.NewReader(raw), treeRequest(), testCaps, uint64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	d.Next()
	d.Next()
	if _, _, err = d.Next(); err == nil || err == io.EOF {
		t.Fatal("accepted unread body")
	}
}

func TestOpenTreeAdmissionAndEffects(t *testing.T) {
	req := treeRequest()
	if id, ok := acquires(&req); !ok || id != req.Handle || sideEffectFree(&req) || modifiesFiles(&req) {
		t.Fatal("wrong acquisition classification")
	}
	if err := testCaps.Admit(&req, true); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Capabilities){func(c *Capabilities) { c.MaxTreeEntries = 0; c.MaxTreeDataBytes = 0 }, func(c *Capabilities) { c.MaxTreeEntries = 999 }, func(c *Capabilities) { c.MaxTreeDataBytes = 1 }} {
		caps := testCaps
		mutate(&caps)
		if fail := caps.Admit(&req, false); fail == nil || fail.Code != CodeUnsupported {
			t.Fatalf("admission: %v", fail)
		}
	}
	caps := testCaps
	caps.MaxTreeDataBytes = math.MaxUint64
	if caps.validate() == nil {
		t.Fatal("accepted overflowing caps")
	}
	req.MaxDataBytes = 0
	if _, err := TreeSizeBound(req, testCaps); err != nil {
		t.Fatal(err)
	}
	req.MaxEntries = 0
	if _, err := TreeSizeBound(req, testCaps); !errors.Is(err, sandboxwire.ErrMalformed) {
		t.Fatalf("bad limits: %v", err)
	}
}

func TestTreeResultFixture(t *testing.T) {
	want := sandboxwiretest.ReadHex(t, "tree_result.hex")
	got := encodeTree(t, []TreeRecord{treeRecord("", 0, ModeDirectory, 0), treeRecord("f", 0, ModeRegular, 3)}, [][]byte{nil, []byte("abc")})
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded result differs: %x", got)
	}
	d, err := NewTreeDecoder(bytes.NewReader(want), treeRequest(), testCaps, uint64(len(want)))
	if err != nil {
		t.Fatal(err)
	}
	if root, _, err := d.Next(); err != nil || root.Attr.Mode != ModeDirectory|0o500 {
		t.Fatalf("root %+v: %v", root, err)
	}
	r, body, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(body)
	if err != nil || string(r.Name) != "f" || r.Attr.Size != 3 || string(content) != "abc" {
		t.Fatalf("file %+v %q: %v", r, content, err)
	}
	if _, _, err = d.Next(); err != io.EOF {
		t.Fatalf("finish: %v", err)
	}
}

func FuzzTreeDecode(f *testing.F) {
	f.Add(sandboxwiretest.ReadHex(f, "tree_result.hex"))
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := NewTreeDecoder(bytes.NewReader(data), treeRequest(), testCaps, uint64(len(data)))
		if err != nil {
			return
		}
		for {
			_, body, err := d.Next()
			if err != nil {
				return
			}
			if _, err = io.Copy(io.Discard, body); err != nil {
				return
			}
		}
	})
}
