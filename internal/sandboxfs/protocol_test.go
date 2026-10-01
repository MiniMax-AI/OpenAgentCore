package sandboxfs

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

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
	b, err := hex.DecodeString(digits.String())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var (
	testNode    = NodeRef{ID: 1, Generation: 7}
	testNode2   = NodeRef{ID: 2, Generation: 9}
	testAttr    = Attr{Ino: 0x102, Mode: ModeRegular | 0o644, Nlink: 1, UID: 1000, GID: 1000, Size: 5, Blocks: 8, Blksize: 4096, Atime: Timestamp{1, 2}, Mtime: Timestamp{3, 4}, Ctime: Timestamp{-5, 6}}
	testDirAttr = Attr{Ino: 0x101, Mode: ModeDirectory | 0o755, Nlink: 2, Blksize: 4096}
	testEntry   = Entry{Node: testNode2, Attr: testAttr}
	testCaps    = Capabilities{
		PathProfile: PathProfileLinuxBytes, CacheProfile: CacheProfileUncached, Durability: DurabilityFsyncRequired,
		MaxNameBytes: 255, MaxPathBytes: 4095, MaxReadBytes: 65536, MaxWriteBytes: 65536, MaxWalkComponents: 256,
		MaxReadDirBytes: 65536, MaxOpenHandles: 4096, AtomicAppend: true, AtomicRename: true, RenameNoReplace: true,
		RenameExchange: true, HardLinks: true, Symlinks: true, SetMode: true, SetOwner: true, SetTimes: true,
		DirectoryFsync: true, ReadDirPlus: true, Flock: true,
	}
	testInstance = sandboxwire.ID{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
)

func TestGoldenFixtures(t *testing.T) {
	symlink := Attr{Ino: 0x102, Mode: ModeSymlink | 0o777, Nlink: 1, UID: 1000, GID: 1000, Size: 1, Blksize: 4096,
		Atime: Timestamp{0x65000000, 1}, Mtime: Timestamp{0x65000000, 2}, Ctime: Timestamp{0x65000000, 3}}
	for _, tc := range []struct {
		file string
		op   Op
		id   uint64
		req  Request
		resp message
		fail *Failure
	}{
		{file: "describe_response.hex", op: OpDescribe, id: 1, resp: &DescribeResponse{
			ServerInstanceID: testInstance, Identity: Identity{UID: 1000, GID: 1000}, Capabilities: testCaps, Exports: []sandboxlink.ExportID{"world"}}},
		{file: "walk_request.hex", op: OpWalk, id: 2, req: &WalkRequest{Parent: testNode, Names: [][]byte{[]byte("link"), []byte("x")}}},
		{file: "walk_response.hex", op: OpWalk, id: 2, resp: &WalkResponse{Entries: []Entry{{Node: NodeRef{ID: 2, Generation: 7}, Attr: symlink}}}},
		{file: "create_request.hex", op: OpCreate, id: 3, req: &CreateRequest{
			Handle: 9, Parent: testNode, Name: []byte("notes.md"), Mode: 0o644, Access: AccessReadWrite, Flags: OpenSync, Exclusive: true}},
		{file: "write_response.hex", op: OpWrite, id: 4, resp: &WriteResponse{
			Written: 4096, Failure: &Failure{Code: CodeErrno, Errno: ErrnoNoSpace, Effect: sandboxwire.EffectNone, Message: "disk full"}}},
		{file: "readdir_request.hex", op: OpReadDir, id: 5, req: &ReadDirRequest{Handle: 0x42, Cookie: 0x1c6a3e5f0b9d2471, Limit: 65536}},
		{file: "readdir_response.hex", op: OpReadDir, id: 5, resp: &ReadDirResponse{Entries: []DirEntry{
			{Name: []byte("a.txt"), Ino: 0x103, Type: ModeRegular, Cookie: 0x2f0e4b6c7d8a9b10},
			{Name: []byte("src"), Ino: 0x104, Type: ModeDirectory, Cookie: 0x3a5c7e9f1b2d4f60},
		}}},
		{file: "rename_request.hex", op: OpRename, id: 6, req: &RenameRequest{
			Parent: testNode, Name: []byte("old"), NewParent: testNode2, NewName: []byte("new"), Mode: RenameExchange}},
		{file: "failure_response.hex", op: OpLookup, id: 7, fail: &Failure{
			Code: CodeErrno, Errno: ErrnoNotFound, Effect: sandboxwire.EffectNone, Message: "missing"}},
		{file: "write_request.hex", op: OpWrite, id: 8, req: &WriteRequest{Handle: 9, Append: true, Data: []byte("log\n")}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			want := readHexFixture(t, tc.file)
			typ := uint16(tc.op)
			var payload []byte
			var err error
			if tc.req != nil {
				payload, err = encodeRequest(tc.req)
			} else {
				typ = sandboxwire.ResponseType(typ)
				payload, err = encodeResponse(tc.resp, tc.fail)
			}
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			if err := sandboxwire.WriteFrame(&got, sandboxwire.Frame{Type: typ, RequestID: tc.id, Payload: payload}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("encoded\n got %x\nwant %x", got.Bytes(), want)
			}

			f, err := sandboxwire.ReadFrame(bytes.NewReader(want), sandboxwire.MaxPayload)
			if err != nil || f.Type != typ || f.RequestID != tc.id {
				t.Fatalf("frame %+v, %v", f, err)
			}
			if tc.req != nil {
				req, err := decodeRequest(tc.op, f.Payload)
				if err != nil || !reflect.DeepEqual(req, tc.req) {
					t.Fatalf("decoded %+v, %v", req, err)
				}
				return
			}
			resp, fail, err := decodeResponse(tc.op, f.Payload)
			if err != nil || !reflect.DeepEqual(fail, tc.fail) || (tc.resp != nil && !reflect.DeepEqual(resp, tc.resp)) {
				t.Fatalf("decoded %+v %+v, %v", resp, fail, err)
			}
		})
	}
}

// samples holds one valid request and response per operation, in tag order.
func samples() []struct {
	req  Request
	resp message
} {
	flock := Lock{Mode: LockWrite, Start: 0, End: math.MaxInt64}
	return []struct {
		req  Request
		resp message
	}{
		{&DescribeRequest{}, &DescribeResponse{ServerInstanceID: testInstance, Identity: Identity{UID: 1, GID: 2}, Capabilities: testCaps, Exports: []sandboxlink.ExportID{"world", "home"}}},
		{&AttachRequest{Export: "world", ReadOnly: true}, &AttachResponse{Root: Entry{Node: testNode, Attr: testDirAttr}}},
		{&DetachRequest{}, &DetachResponse{}},
		{&LookupRequest{Parent: testNode, Name: []byte("a\xff")}, &LookupResponse{Entry: testEntry}},
		{&WalkRequest{Parent: testNode, Names: [][]byte{[]byte("a"), []byte("b")}}, &WalkResponse{Entries: []Entry{testEntry}, Failure: NewErrnoFailure(ErrnoNotFound, sandboxwire.EffectNone, "b")}},
		{&GetAttrRequest{Target: Target{Kind: TargetHandle, Handle: 3}}, &GetAttrResponse{Attr: testAttr}},
		{&SetAttrRequest{Target: Target{Kind: TargetNode, Node: testNode}, Set: AttrSize | AttrMode | AttrUID | AttrGID | AttrAtime | AttrMtimeNow, Size: 9, Mode: 0o4755, UID: 5, GID: 6, Atime: Timestamp{7, 8}}, &SetAttrResponse{Attr: testAttr}},
		{&AccessRequest{Node: testNode, Mask: MayRead | MayExecute}, &AccessResponse{}},
		{&OpenRequest{Handle: 4, Node: testNode, Access: AccessWrite, Flags: OpenTruncate | OpenSync}, &OpenResponse{}},
		{&CreateRequest{Handle: 5, Parent: testNode, Name: []byte("n"), Mode: 0o600, Access: AccessRead, Flags: OpenDataSync}, &CreateResponse{Entry: testEntry}},
		{&ReadRequest{Handle: 4, Offset: 10, Size: 65536}, &ReadResponse{Data: []byte("data")}},
		{&WriteRequest{Handle: 4, Offset: 10, Append: true, Data: []byte("data")}, &WriteResponse{Written: 4}},
		{&FlushRequest{Handle: 4, Owner: 11}, &FlushResponse{}},
		{&FsyncRequest{Handle: 4, DataOnly: true}, &FsyncResponse{}},
		{&ReleaseRequest{Handle: 4}, &ReleaseResponse{}},
		{&OpenDirRequest{Handle: 6, Node: testNode}, &OpenDirResponse{}},
		{&ReadDirRequest{Handle: 6, Cookie: 1, Limit: 4096, WithAttrs: true}, &ReadDirResponse{Entries: []DirEntry{{Name: []byte("f"), Ino: testAttr.Ino, Type: ModeRegular, Cookie: 2, Entry: &testEntry}}, End: true}},
		{&ReleaseDirRequest{Handle: 6}, &ReleaseDirResponse{}},
		{&MkdirRequest{Parent: testNode, Name: []byte("d"), Mode: 0o1777}, &MkdirResponse{Entry: testEntry}},
		{&UnlinkRequest{Parent: testNode, Name: []byte("f")}, &UnlinkResponse{}},
		{&RmdirRequest{Parent: testNode, Name: []byte("d")}, &RmdirResponse{}},
		{&RenameRequest{Parent: testNode, Name: []byte("a"), NewParent: testNode2, NewName: []byte("b"), Mode: RenameNoReplace}, &RenameResponse{}},
		{&LinkRequest{Node: testNode2, NewParent: testNode, NewName: []byte("h")}, &LinkResponse{Entry: testEntry}},
		{&SymlinkRequest{Parent: testNode, Name: []byte("s"), Target: []byte("../t")}, &SymlinkResponse{Entry: testEntry}},
		{&ReadlinkRequest{Node: testNode2}, &ReadlinkResponse{Target: []byte("../t")}},
		{&StatFSRequest{Node: testNode}, &StatFSResponse{Blocks: 1, BlocksFree: 2, BlocksAvailable: 3, Files: 4, FilesFree: 5, BlockSize: 4096, FragmentSize: 4096, NameMax: 255}},
		{&ForgetRequest{Entries: []ForgetEntry{{Node: testNode, Count: 1}, {Node: testNode2, Count: 3}}}, &ForgetResponse{}},
		{&GetLockRequest{Handle: 4, Owner: 12, Lock: Lock{Mode: LockRead, Start: 0, End: 99}}, &GetLockResponse{Conflict: &Lock{Mode: LockWrite, Start: 50, End: 60}}},
		{&SetLockRequest{Handle: 4, Kind: LockFlock, Owner: 12, Lock: flock, Wait: true}, &SetLockResponse{}},
		{&CancelRequestRequest{Target: 9}, &CancelRequestResponse{}},
	}
}

func TestRoundTripEveryMessage(t *testing.T) {
	all := samples()
	if len(all) != int(OpCancelRequest) {
		t.Fatalf("%d samples for %d operations", len(all), OpCancelRequest)
	}
	for i, s := range all {
		op := Op(i + 1)
		if s.req.Op() != op {
			t.Fatalf("sample %d is %s", i, s.req.Op())
		}
		payload, err := encodeRequest(s.req)
		if err != nil {
			t.Fatalf("%s request: %v", op, err)
		}
		if req, err := decodeRequest(op, payload); err != nil || !reflect.DeepEqual(req, s.req) {
			t.Fatalf("%s request decoded %+v, %v", op, req, err)
		}
		if payload, err = encodeResponse(s.resp, nil); err != nil {
			t.Fatalf("%s response: %v", op, err)
		}
		if resp, fail, err := decodeResponse(op, payload); err != nil || fail != nil || !reflect.DeepEqual(resp, s.resp) {
			t.Fatalf("%s response decoded %+v %v, %v", op, resp, fail, err)
		}
	}
}

func TestRetryableCodes(t *testing.T) {
	for c := CodeInvalidArgument; c.Valid(); c++ {
		if c.Retryable() != (c == CodeResourceExhausted) {
			t.Errorf("%s.Retryable() = %v", c, c.Retryable())
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	encode := func(m message) []byte {
		var e sandboxwire.Encoder
		m.encode(&e)
		return e.Payload()
	}
	for name, tc := range map[string]struct {
		op      Op
		payload []byte
	}{
		"dot-dot name":        {OpLookup, encode(&LookupRequest{Parent: testNode, Name: []byte("..")})},
		"slash in name":       {OpMkdir, encode(&MkdirRequest{Parent: testNode, Name: []byte("a/b")})},
		"zero generation":     {OpOpenDir, encode(&OpenDirRequest{Handle: 1, Node: NodeRef{ID: 1}})},
		"zero handle ID":      {OpOpen, encode(&OpenRequest{Node: testNode, Access: AccessRead})},
		"unknown open flag":   {OpOpen, encode(&OpenRequest{Handle: 1, Node: testNode, Access: AccessRead, Flags: 1 << 4})},
		"zero access mode":    {OpOpen, encode(&OpenRequest{Handle: 1, Node: testNode})},
		"duplicate forget":    {OpForget, encode(&ForgetRequest{Entries: []ForgetEntry{{testNode, 1}, {testNode, 1}}})},
		"partial flock range": {OpSetLock, encode(&SetLockRequest{Handle: 1, Kind: LockFlock, Lock: Lock{Mode: LockRead, End: 10}})},
		"trailing byte":       {OpReleaseDir, append(encode(&ReleaseDirRequest{Handle: 1}), 0)},
		"owner sentinel":      {OpSetAttr, encode(&SetAttrRequest{Target: Target{Kind: TargetHandle, Handle: 1}, Set: AttrUID, UID: math.MaxUint32})},
		"uppercase export":    {OpAttach, encode(&AttachRequest{Export: "World"})},
		"dot in export":       {OpAttach, encode(&AttachRequest{Export: "a.b"})},
	} {
		if _, err := decodeRequest(tc.op, tc.payload); !errors.Is(err, sandboxwire.ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	entry := func(name string, cookie uint64) DirEntry {
		return DirEntry{Name: []byte(name), Ino: 1, Type: ModeRegular, Cookie: cookie}
	}
	for name, page := range map[string]*ReadDirResponse{
		"repeated name":   {Entries: []DirEntry{entry("a", 1), entry("a", 2)}, End: true},
		"repeated cookie": {Entries: []DirEntry{entry("a", 1), entry("b", 1)}, End: true},
		"empty page":      {},
	} {
		var e sandboxwire.Encoder
		e.Enum(uint16(ResultSuccess))
		page.encode(&e)
		if _, _, err := decodeResponse(OpReadDir, e.Payload()); !errors.Is(err, sandboxwire.ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func FuzzDecode(f *testing.F) {
	entries, _ := os.ReadDir("testdata")
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".hex") {
			f.Add(readHexFixture(f, e.Name()))
		}
	}
	for i, s := range samples() {
		op := uint16(i + 1)
		for _, m := range []struct {
			typ uint16
			msg message
		}{{op, s.req}, {sandboxwire.ResponseType(op), s.resp}} {
			var e sandboxwire.Encoder
			if m.typ == op {
				m.msg.encode(&e)
			} else {
				e.Enum(uint16(ResultSuccess))
				m.msg.encode(&e)
			}
			var w bytes.Buffer
			sandboxwire.WriteFrame(&w, sandboxwire.Frame{Type: m.typ, RequestID: 1, Payload: e.Payload()})
			f.Add(w.Bytes())
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, err := sandboxwire.ReadFrame(bytes.NewReader(data), sandboxwire.MaxPayload)
		if err != nil {
			return
		}
		kind, err := tags.Classify(fr.Type)
		if err != nil {
			return
		}
		var again []byte
		switch kind {
		case sandboxwire.KindRequest:
			req, err := decodeRequest(Op(fr.Type), fr.Payload)
			if err != nil {
				if !errors.Is(err, sandboxwire.ErrMalformed) {
					t.Fatalf("request error %v is not ErrMalformed", err)
				}
				return
			}
			if again, err = encodeRequest(req); err != nil {
				t.Fatalf("decoded request does not encode: %v", err)
			}
		case sandboxwire.KindResponse:
			resp, fail, err := decodeResponse(Op(fr.Type^sandboxwire.ResponseType(0)), fr.Payload)
			if err != nil {
				if !errors.Is(err, sandboxwire.ErrMalformed) {
					t.Fatalf("response error %v is not ErrMalformed", err)
				}
				return
			}
			if again, err = encodeResponse(resp, fail); err != nil {
				t.Fatalf("decoded response does not encode: %v", err)
			}
		}
		if !bytes.Equal(again, fr.Payload) {
			t.Fatalf("round trip changed the payload:\n got %x\nwant %x", again, fr.Payload)
		}
	})
}
