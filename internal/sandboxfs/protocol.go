// Package sandboxfs is the File access protocol between a Runtime and a sandbox
// file service. This file is its one authored definition: the request tags,
// message types, wire layouts, validators and the Service interface a file
// service implements. client.go and server.go carry the protocol over one
// stream. docs/file-access-protocol.md describes it.
package sandboxfs

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Version is the protocol version. Link matches it exactly when it opens a
// file stream.
const Version uint16 = 1

// Op is a request tag. Each response carries its request's tag with
// sandboxwire.ResponseType. The protocol has no events.
type Op uint16

const (
	OpDescribe Op = iota + 1
	OpAttach
	OpDetach
	OpLookup
	OpWalk
	OpGetAttr
	OpSetAttr
	OpAccess
	OpOpen
	OpCreate
	OpRead
	OpWrite
	OpFlush
	OpFsync
	OpRelease
	OpOpenDir
	OpReadDir
	OpReleaseDir
	OpMkdir
	OpUnlink
	OpRmdir
	OpRename
	OpLink
	OpSymlink
	OpReadlink
	OpStatFS
	OpForget
	OpGetLock
	OpSetLock
	OpCancelRequest
)

var opNames = [...]string{
	OpDescribe: "Describe", OpAttach: "Attach", OpDetach: "Detach",
	OpLookup: "Lookup", OpWalk: "Walk", OpGetAttr: "GetAttr", OpSetAttr: "SetAttr", OpAccess: "Access",
	OpOpen: "Open", OpCreate: "Create", OpRead: "Read", OpWrite: "Write", OpFlush: "Flush", OpFsync: "Fsync", OpRelease: "Release",
	OpOpenDir: "OpenDir", OpReadDir: "ReadDir", OpReleaseDir: "ReleaseDir",
	OpMkdir: "Mkdir", OpUnlink: "Unlink", OpRmdir: "Rmdir", OpRename: "Rename", OpLink: "Link", OpSymlink: "Symlink", OpReadlink: "Readlink",
	OpStatFS: "StatFS", OpForget: "Forget", OpGetLock: "GetLock", OpSetLock: "SetLock", OpCancelRequest: "CancelRequest",
}

func (o Op) String() string {
	if o >= 1 && int(o) < len(opNames) {
		return opNames[o]
	}
	return fmt.Sprintf("Op(%d)", uint16(o))
}

var tags = sandboxwire.Tags{Requests: uint16(OpCancelRequest)}

// Hard limits. Capabilities may advertise smaller ones; nothing on the wire
// exceeds these.
const (
	maxNameBytes     = 1024 // an entry name, the FUSE name limit
	maxTargetBytes   = 4096 // a symlink target
	maxWalkNames     = 1024
	maxReadDirBytes  = 256 << 10
	maxForgetEntries = 4096
	maxMessageBytes  = 1024
	maxOffset        = math.MaxInt64
)

// Attachment is the authenticated context Link establishes for a stream. File
// requests never carry it: the server passes it to every Service call.
type Attachment struct {
	ID sandboxwire.ID
	// ServerInstanceID is the service incarnation the stream was opened
	// against. A service answers InstanceChanged when it differs from its own.
	ServerInstanceID sandboxwire.ID
	Lease            Lease
	// Exports are the exports the Link binding grants the attachment: at
	// least one, each ID once. Attach can select only these.
	Exports []sandboxlink.ExportGrant
}

// Grant returns the attachment's grant of export id.
func (a *Attachment) Grant(id sandboxlink.ExportID) (sandboxlink.ExportGrant, bool) {
	for _, g := range a.Exports {
		if g.ID == id {
			return g, true
		}
	}
	return sandboxlink.ExportGrant{}, false
}

func (a *Attachment) validate() error {
	if a.ID.IsZero() || a.ServerInstanceID.IsZero() || a.Lease == nil {
		return errors.New("sandboxfs: attachment without an ID, server instance or lease")
	}
	if len(a.Exports) == 0 {
		return errors.New("sandboxfs: attachment grants no export")
	}
	seen := make(map[sandboxlink.ExportID]bool, len(a.Exports))
	for _, g := range a.Exports {
		if !g.ID.Valid() || seen[g.ID] {
			return fmt.Errorf("sandboxfs: attachment grants export %q", string(g.ID))
		}
		seen[g.ID] = true
	}
	return nil
}

// Lease is an attachment's authority. Done closes when the lease lapses or is
// revoked and never reopens. Losing a transport does not end a lease, so
// every stream of one attachment shares one Lease. A context.Context
// satisfies it.
type Lease interface {
	Done() <-chan struct{}
}

// Service is implemented by a file service, one method per operation.
// CancelRequest is not a method: the server answers it by cancelling the
// target's context. A method returns a *Failure for a typed failure; the server
// reports any other error as Unknown with EffectPossible.
type Service interface {
	Describe(context.Context, Attachment, *DescribeRequest) (*DescribeResponse, error)
	Attach(context.Context, Attachment, *AttachRequest) (*AttachResponse, error)
	Detach(context.Context, Attachment, *DetachRequest) (*DetachResponse, error)
	Lookup(context.Context, Attachment, *LookupRequest) (*LookupResponse, error)
	Walk(context.Context, Attachment, *WalkRequest) (*WalkResponse, error)
	GetAttr(context.Context, Attachment, *GetAttrRequest) (*GetAttrResponse, error)
	SetAttr(context.Context, Attachment, *SetAttrRequest) (*SetAttrResponse, error)
	Access(context.Context, Attachment, *AccessRequest) (*AccessResponse, error)
	Open(context.Context, Attachment, *OpenRequest) (*OpenResponse, error)
	Create(context.Context, Attachment, *CreateRequest) (*CreateResponse, error)
	Read(context.Context, Attachment, *ReadRequest) (*ReadResponse, error)
	Write(context.Context, Attachment, *WriteRequest) (*WriteResponse, error)
	Flush(context.Context, Attachment, *FlushRequest) (*FlushResponse, error)
	Fsync(context.Context, Attachment, *FsyncRequest) (*FsyncResponse, error)
	Release(context.Context, Attachment, *ReleaseRequest) (*ReleaseResponse, error)
	OpenDir(context.Context, Attachment, *OpenDirRequest) (*OpenDirResponse, error)
	ReadDir(context.Context, Attachment, *ReadDirRequest) (*ReadDirResponse, error)
	ReleaseDir(context.Context, Attachment, *ReleaseDirRequest) (*ReleaseDirResponse, error)
	Mkdir(context.Context, Attachment, *MkdirRequest) (*MkdirResponse, error)
	Unlink(context.Context, Attachment, *UnlinkRequest) (*UnlinkResponse, error)
	Rmdir(context.Context, Attachment, *RmdirRequest) (*RmdirResponse, error)
	Rename(context.Context, Attachment, *RenameRequest) (*RenameResponse, error)
	Link(context.Context, Attachment, *LinkRequest) (*LinkResponse, error)
	Symlink(context.Context, Attachment, *SymlinkRequest) (*SymlinkResponse, error)
	Readlink(context.Context, Attachment, *ReadlinkRequest) (*ReadlinkResponse, error)
	StatFS(context.Context, Attachment, *StatFSRequest) (*StatFSResponse, error)
	Forget(context.Context, Attachment, *ForgetRequest) (*ForgetResponse, error)
	GetLock(context.Context, Attachment, *GetLockRequest) (*GetLockResponse, error)
	SetLock(context.Context, Attachment, *SetLockRequest) (*SetLockResponse, error)
}

// Enumerations. Each is a uint16 on the wire, and zero is never valid.

// Result is the discriminator every response payload begins with.
type Result uint16

const (
	ResultSuccess Result = 1
	ResultFailure Result = 2
)

type ErrorCode uint16

const (
	CodeInvalidArgument ErrorCode = iota + 1
	CodeUnsupported
	CodeUnauthorized
	CodeStaleAttachment
	CodeInstanceChanged
	CodeStaleNode
	CodeStaleHandle
	CodeResourceExhausted
	CodeCancelled
	CodeDeadlineExceeded
	CodeErrno
	CodeUnknown
)

var codeNames = [...]string{
	CodeInvalidArgument: "InvalidArgument", CodeUnsupported: "Unsupported", CodeUnauthorized: "Unauthorized",
	CodeStaleAttachment: "StaleAttachment", CodeInstanceChanged: "InstanceChanged", CodeStaleNode: "StaleNode",
	CodeStaleHandle: "StaleHandle", CodeResourceExhausted: "ResourceExhausted", CodeCancelled: "Cancelled",
	CodeDeadlineExceeded: "DeadlineExceeded", CodeErrno: "Errno", CodeUnknown: "Unknown",
}

func (c ErrorCode) Valid() bool    { return c >= 1 && int(c) < len(codeNames) }
func (c ErrorCode) String() string { return enumName(codeNames[:], uint16(c), "ErrorCode") }

// Errno is the semantic error of a failed file-system call. Each adapter
// converts its native errors; an unknown native error is ErrnoIO.
type Errno uint16

const (
	ErrnoPermissionDenied Errno = iota + 1
	ErrnoOperationNotPermitted
	ErrnoNotFound
	ErrnoExists
	ErrnoNotDirectory
	ErrnoIsDirectory
	ErrnoDirectoryNotEmpty
	ErrnoInvalidArgument
	ErrnoBadDescriptor
	ErrnoTooManyOpenFiles
	ErrnoNoSpace
	ErrnoQuotaExceeded
	ErrnoReadOnlyFilesystem
	ErrnoCrossDevice
	ErrnoNameTooLong
	ErrnoSymlinkLoop
	ErrnoFileTooLarge
	ErrnoOverflow
	ErrnoBusy
	ErrnoAgain
	ErrnoInterrupted
	ErrnoIO
	ErrnoNoDevice
	ErrnoNoSuchDeviceOrAddress
	ErrnoBrokenPipe
	ErrnoNotSupported
	ErrnoNoLocks
	ErrnoDeadlock
)

var errnoNames = [...]string{
	ErrnoPermissionDenied: "PermissionDenied", ErrnoOperationNotPermitted: "OperationNotPermitted",
	ErrnoNotFound: "NotFound", ErrnoExists: "Exists", ErrnoNotDirectory: "NotDirectory",
	ErrnoIsDirectory: "IsDirectory", ErrnoDirectoryNotEmpty: "DirectoryNotEmpty",
	ErrnoInvalidArgument: "InvalidArgument", ErrnoBadDescriptor: "BadDescriptor",
	ErrnoTooManyOpenFiles: "TooManyOpenFiles", ErrnoNoSpace: "NoSpace", ErrnoQuotaExceeded: "QuotaExceeded",
	ErrnoReadOnlyFilesystem: "ReadOnlyFilesystem", ErrnoCrossDevice: "CrossDevice",
	ErrnoNameTooLong: "NameTooLong", ErrnoSymlinkLoop: "SymlinkLoop", ErrnoFileTooLarge: "FileTooLarge",
	ErrnoOverflow: "Overflow", ErrnoBusy: "Busy", ErrnoAgain: "Again", ErrnoInterrupted: "Interrupted",
	ErrnoIO: "IO", ErrnoNoDevice: "NoDevice", ErrnoNoSuchDeviceOrAddress: "NoSuchDeviceOrAddress",
	ErrnoBrokenPipe: "BrokenPipe", ErrnoNotSupported: "NotSupported", ErrnoNoLocks: "NoLocks",
	ErrnoDeadlock: "Deadlock",
}

func (e Errno) Valid() bool    { return e >= 1 && int(e) < len(errnoNames) }
func (e Errno) String() string { return enumName(errnoNames[:], uint16(e), "Errno") }

func enumName(names []string, v uint16, kind string) string {
	if v >= 1 && int(v) < len(names) {
		return names[v]
	}
	return fmt.Sprintf("%s(%d)", kind, v)
}

// TargetKind selects what GetAttr and SetAttr address.
type TargetKind uint16

const (
	TargetNode   TargetKind = 1
	TargetHandle TargetKind = 2
)

type AccessMode uint16

const (
	AccessRead      AccessMode = 1
	AccessWrite     AccessMode = 2
	AccessReadWrite AccessMode = 3
)

// Writes reports whether the mode opens for writing.
func (m AccessMode) Writes() bool { return m == AccessWrite || m == AccessReadWrite }

type RenameMode uint16

const (
	RenameReplace   RenameMode = 1
	RenameNoReplace RenameMode = 2
	RenameExchange  RenameMode = 3
)

type LockKind uint16

const (
	LockPOSIX LockKind = 1
	LockFlock LockKind = 2
)

type LockMode uint16

const (
	LockRead   LockMode = 1
	LockWrite  LockMode = 2
	LockUnlock LockMode = 3
)

type PathProfile uint16

// PathProfileLinuxBytes: names and symlink targets are Linux byte strings.
const PathProfileLinuxBytes PathProfile = 1

type CacheProfile uint16

// CacheProfileUncached: zero entry, attribute and negative TTLs, direct I/O,
// no writeback cache and no retained directory listing on the client.
const CacheProfileUncached CacheProfile = 1

type Durability uint16

// DurabilityFsyncRequired: data is durable only after Fsync succeeds.
const DurabilityFsyncRequired Durability = 1

// Flag sets. Each is a uint32 on the wire; unknown bits are rejected.

type OpenFlags uint32

const (
	OpenAppend OpenFlags = 1 << iota
	OpenTruncate
	OpenNoFollow
	OpenSync
	OpenDataSync

	openFlagsAll = OpenAppend | OpenTruncate | OpenNoFollow | OpenSync | OpenDataSync
)

// AttrMask selects the attributes SetAttr changes. AttrAtimeNow and
// AttrMtimeNow set the time from the service's clock and exclude AttrAtime
// and AttrMtime respectively.
type AttrMask uint32

const (
	AttrSize AttrMask = 1 << iota
	AttrMode
	AttrUID
	AttrGID
	AttrAtime
	AttrMtime
	AttrAtimeNow
	AttrMtimeNow

	attrMaskAll = AttrMtimeNow<<1 - 1
)

// AccessMask selects the permissions Access checks. Zero checks existence.
type AccessMask uint32

const (
	MayExecute AccessMask = 1 << iota
	MayWrite
	MayRead

	accessMaskAll = MayExecute | MayWrite | MayRead
)

// Mode bits of Attr.Mode and DirEntry.Type, in the Linux st_mode layout.
const (
	ModeType        uint32 = 0o170000
	ModeSocket      uint32 = 0o140000
	ModeSymlink     uint32 = 0o120000
	ModeRegular     uint32 = 0o100000
	ModeBlockDevice uint32 = 0o060000
	ModeDirectory   uint32 = 0o040000
	ModeCharDevice  uint32 = 0o020000
	ModeFIFO        uint32 = 0o010000
	ModePerm        uint32 = 0o7777
)

func validFileType(t uint32) bool {
	switch t {
	case ModeSocket, ModeSymlink, ModeRegular, ModeBlockDevice, ModeDirectory, ModeCharDevice, ModeFIFO:
		return true
	}
	return false
}

// NodeRef names a file-system object the attachment holds lookup references
// on. An ID is reused only after its object is forgotten, and Generation then
// differs, so a stale NodeRef never names another object. Both are nonzero.
type NodeRef struct {
	ID         uint64
	Generation uint64
}

// HandleID names an open file or directory handle. It is opaque and nonzero.
type HandleID uint64

// LockOwner identifies a lock owner within an attachment.
type LockOwner uint64

type Timestamp struct {
	Sec  int64
	Nsec uint32 // below one billion
}

// Attr is a file's attributes. Ino identifies the file within its export.
type Attr struct {
	Ino     uint64
	Mode    uint32 // file type and permission bits
	Nlink   uint32
	UID     uint32
	GID     uint32
	Rdev    uint64
	Size    uint64
	Blocks  uint64 // 512-byte blocks
	Blksize uint32
	Atime   Timestamp
	Mtime   Timestamp
	Ctime   Timestamp
}

// Entry is a node the response acquired one lookup reference on, with its
// attributes.
type Entry struct {
	Node NodeRef
	Attr Attr
}

// Identity is the uid and gid the service acts as. Files it creates carry
// them. It is informational: requests never select an identity.
type Identity struct {
	UID uint32
	GID uint32
}

// Capabilities declares what a service supports. Every field is encoded.
type Capabilities struct {
	PathProfile       PathProfile
	CacheProfile      CacheProfile
	Durability        Durability
	MaxNameBytes      uint32
	MaxPathBytes      uint32 // longest symlink target
	MaxReadBytes      uint32
	MaxWriteBytes     uint32
	MaxWalkComponents uint32
	MaxReadDirBytes   uint32
	MaxOpenHandles    uint32 // per attachment
	ReadOnly          bool
	AtomicAppend      bool
	AtomicRename      bool
	RenameNoReplace   bool
	RenameExchange    bool
	HardLinks         bool
	Symlinks          bool
	SetMode           bool
	SetOwner          bool
	SetTimes          bool
	DirectoryFsync    bool
	ReadDirPlus       bool
	Flock             bool
	POSIXLocks        bool
}

// Target is the node or open handle GetAttr and SetAttr address. Only the
// field Kind selects is set.
type Target struct {
	Kind   TargetKind
	Node   NodeRef
	Handle HandleID
}

// DirEntry is one directory entry. Cookie is the position after it: ReadDir
// resumes there. Entry is set when ReadDir asked WithAttrs, and then carries
// one lookup reference.
type DirEntry struct {
	Name   []byte
	Ino    uint64
	Type   uint32 // one ModeType value
	Cookie uint64
	Entry  *Entry
}

const (
	attrWireSize     = 8 + 4 + 4 + 4 + 4 + 8 + 8 + 8 + 4 + 3*(8+4)
	entryWireSize    = 16 + attrWireSize
	dirEntryWireSize = 4 + 8 + 4 + 8 + 1
)

// WireSize is the entry's encoded length. ReadDir's Limit bounds the sum of
// the returned entries' sizes.
func (e *DirEntry) WireSize() int {
	n := dirEntryWireSize + len(e.Name)
	if e.Entry != nil {
		n += entryWireSize
	}
	return n
}

type ForgetEntry struct {
	Node  NodeRef
	Count uint64
}

// Lock is a byte range lock: Start through End inclusive, where End
// math.MaxInt64 extends to the end of the file.
type Lock struct {
	Mode  LockMode
	Start uint64
	End   uint64
}

// Failure is a typed failure. Errno is set exactly when Code is CodeErrno.
// Effect says whether the failed request may have taken effect.
type Failure struct {
	Code    ErrorCode
	Errno   Errno
	Effect  sandboxwire.Effect
	Message string

	cause error // set on failures the client produces; never encoded
}

// NewFailure returns a failure with code, effect and a message cut to the
// protocol limit.
func NewFailure(code ErrorCode, effect sandboxwire.Effect, message string) *Failure {
	return &Failure{Code: code, Effect: effect, Message: clip(message)}
}

// NewErrnoFailure returns a CodeErrno failure.
func NewErrnoFailure(errno Errno, effect sandboxwire.Effect, message string) *Failure {
	return &Failure{Code: CodeErrno, Errno: errno, Effect: effect, Message: clip(message)}
}

func clip(s string) string {
	if len(s) > maxMessageBytes {
		return s[:maxMessageBytes]
	}
	return s
}

func (f *Failure) Error() string {
	s := "sandboxfs: " + f.Code.String()
	if f.Code == CodeErrno {
		s += " " + f.Errno.String()
	}
	if f.Message != "" {
		s += ": " + f.Message
	}
	if f.Effect == sandboxwire.EffectPossible {
		s += " (effect possible)"
	}
	return s
}

func (f *Failure) Unwrap() error { return f.cause }

// Requests and responses, in tag order.

type DescribeRequest struct{}

type DescribeResponse struct {
	ServerInstanceID sandboxwire.ID
	Identity         Identity
	Capabilities     Capabilities
	Exports          []sandboxlink.ExportID
}

// AttachRequest selects a declared export that the attachment's Link grant
// includes. A read-only grant requires ReadOnly. An attachment attaches once
// until it detaches.
type AttachRequest struct {
	Export   sandboxlink.ExportID
	ReadOnly bool
}

type AttachResponse struct {
	Root Entry
}

// DetachRequest releases every node, handle and lock of the attachment.
type DetachRequest struct{}

type DetachResponse struct{}

type LookupRequest struct {
	Parent NodeRef
	Name   []byte
}

type LookupResponse struct {
	Entry Entry
}

// WalkRequest looks up Names in turn from Parent. The walk stops after a
// symlink, so fewer entries than names with no Failure means the last entry
// is a symlink.
type WalkRequest struct {
	Parent NodeRef
	Names  [][]byte
}

// WalkResponse holds the entries walked, at least one. Failure is the error
// that stopped the walk after them.
type WalkResponse struct {
	Entries []Entry
	Failure *Failure
}

type GetAttrRequest struct {
	Target Target
}

type GetAttrResponse struct {
	Attr Attr
}

// SetAttrRequest changes the attributes Set selects; the other fields are
// zero. On the wire each selected value follows the mask in field order.
type SetAttrRequest struct {
	Target Target
	Set    AttrMask
	Size   uint64
	Mode   uint32 // permission bits
	UID    uint32
	GID    uint32
	Atime  Timestamp
	Mtime  Timestamp
}

type SetAttrResponse struct {
	Attr Attr
}

type AccessRequest struct {
	Node NodeRef
	Mask AccessMask
}

type AccessResponse struct{}

type OpenRequest struct {
	Node   NodeRef
	Access AccessMode
	Flags  OpenFlags
}

type OpenResponse struct {
	Handle HandleID
}

type CreateRequest struct {
	Parent    NodeRef
	Name      []byte
	Mode      uint32 // permission bits, applied as given
	Access    AccessMode
	Flags     OpenFlags
	Exclusive bool
}

type CreateResponse struct {
	Entry  Entry
	Handle HandleID
}

type ReadRequest struct {
	Handle HandleID
	Offset uint64
	Size   uint32
}

// ReadResponse holds the bytes read; fewer than asked means end of file.
type ReadResponse struct {
	Data []byte
}

// WriteRequest writes Data at Offset. An append-opened handle appends
// atomically and ignores Offset.
type WriteRequest struct {
	Handle HandleID
	Offset uint64
	Data   []byte
}

// WriteResponse reports the bytes written. Failure is the error that stopped
// the write after that nonzero prefix.
type WriteResponse struct {
	Written uint32
	Failure *Failure
}

// FlushRequest runs on each close of a descriptor for the handle. Owner is the
// closing lock owner, whose POSIX locks on the file it releases.
type FlushRequest struct {
	Handle HandleID
	Owner  LockOwner
}

type FlushResponse struct{}

type FsyncRequest struct {
	Handle   HandleID
	DataOnly bool
}

type FsyncResponse struct{}

type ReleaseRequest struct {
	Handle HandleID
}

type ReleaseResponse struct{}

type OpenDirRequest struct {
	Node NodeRef
}

type OpenDirResponse struct {
	Handle HandleID
}

// ReadDirRequest reads entries after Cookie; cookie zero is the start. Limit
// bounds the entries' WireSize sum. "." and ".." are never returned.
type ReadDirRequest struct {
	Handle    HandleID
	Cookie    uint64
	Limit     uint32
	WithAttrs bool
}

type ReadDirResponse struct {
	Entries []DirEntry
	End     bool
}

type ReleaseDirRequest struct {
	Handle HandleID
}

type ReleaseDirResponse struct{}

type MkdirRequest struct {
	Parent NodeRef
	Name   []byte
	Mode   uint32 // permission bits, applied as given
}

type MkdirResponse struct {
	Entry Entry
}

type UnlinkRequest struct {
	Parent NodeRef
	Name   []byte
}

type UnlinkResponse struct{}

type RmdirRequest struct {
	Parent NodeRef
	Name   []byte
}

type RmdirResponse struct{}

type RenameRequest struct {
	Parent    NodeRef
	Name      []byte
	NewParent NodeRef
	NewName   []byte
	Mode      RenameMode
}

type RenameResponse struct{}

type LinkRequest struct {
	Node      NodeRef
	NewParent NodeRef
	NewName   []byte
}

type LinkResponse struct {
	Entry Entry
}

type SymlinkRequest struct {
	Parent NodeRef
	Name   []byte
	Target []byte
}

type SymlinkResponse struct {
	Entry Entry
}

type ReadlinkRequest struct {
	Node NodeRef
}

type ReadlinkResponse struct {
	Target []byte
}

type StatFSRequest struct {
	Node NodeRef
}

type StatFSResponse struct {
	Blocks          uint64
	BlocksFree      uint64
	BlocksAvailable uint64
	Files           uint64
	FilesFree       uint64
	BlockSize       uint32
	FragmentSize    uint32
	NameMax         uint32
}

// ForgetRequest releases lookup references. It applies entirely or not at
// all.
type ForgetRequest struct {
	Entries []ForgetEntry
}

type ForgetResponse struct{}

// GetLockRequest asks for a POSIX lock that would conflict with Lock.
type GetLockRequest struct {
	Handle HandleID
	Owner  LockOwner
	Lock   Lock
}

// GetLockResponse holds the conflicting lock, if any.
type GetLockResponse struct {
	Conflict *Lock
}

// SetLockRequest acquires or releases a lock. A LockFlock lock covers the
// whole file. Wait blocks until the lock is available or the request is
// cancelled.
type SetLockRequest struct {
	Handle HandleID
	Kind   LockKind
	Owner  LockOwner
	Lock   Lock
	Wait   bool
}

type SetLockResponse struct{}

// CancelRequestRequest asks to cancel the outstanding request Target. Its
// acknowledgement says nothing about the target's outcome.
type CancelRequestRequest struct {
	Target uint64
}

type CancelRequestResponse struct{}

func (*DescribeRequest) Op() Op      { return OpDescribe }
func (*AttachRequest) Op() Op        { return OpAttach }
func (*DetachRequest) Op() Op        { return OpDetach }
func (*LookupRequest) Op() Op        { return OpLookup }
func (*WalkRequest) Op() Op          { return OpWalk }
func (*GetAttrRequest) Op() Op       { return OpGetAttr }
func (*SetAttrRequest) Op() Op       { return OpSetAttr }
func (*AccessRequest) Op() Op        { return OpAccess }
func (*OpenRequest) Op() Op          { return OpOpen }
func (*CreateRequest) Op() Op        { return OpCreate }
func (*ReadRequest) Op() Op          { return OpRead }
func (*WriteRequest) Op() Op         { return OpWrite }
func (*FlushRequest) Op() Op         { return OpFlush }
func (*FsyncRequest) Op() Op         { return OpFsync }
func (*ReleaseRequest) Op() Op       { return OpRelease }
func (*OpenDirRequest) Op() Op       { return OpOpenDir }
func (*ReadDirRequest) Op() Op       { return OpReadDir }
func (*ReleaseDirRequest) Op() Op    { return OpReleaseDir }
func (*MkdirRequest) Op() Op         { return OpMkdir }
func (*UnlinkRequest) Op() Op        { return OpUnlink }
func (*RmdirRequest) Op() Op         { return OpRmdir }
func (*RenameRequest) Op() Op        { return OpRename }
func (*LinkRequest) Op() Op          { return OpLink }
func (*SymlinkRequest) Op() Op       { return OpSymlink }
func (*ReadlinkRequest) Op() Op      { return OpReadlink }
func (*StatFSRequest) Op() Op        { return OpStatFS }
func (*ForgetRequest) Op() Op        { return OpForget }
func (*GetLockRequest) Op() Op       { return OpGetLock }
func (*SetLockRequest) Op() Op       { return OpSetLock }
func (*CancelRequestRequest) Op() Op { return OpCancelRequest }

// Request is implemented by every request type.
type Request interface {
	message
	Op() Op
}

type message interface {
	encode(*sandboxwire.Encoder)
	decode(*decoder)
	validate() error
}

// opSpec binds an operation's request, response and Service method.
type opSpec struct {
	newRequest  func() Request
	newResponse func() message
	serve       func(context.Context, Service, Attachment, Request) (message, error)
}

var errNilResponse = errors.New("service returned no response")

func bind[QT, RT any, Q interface {
	*QT
	Request
}, R interface {
	*RT
	message
}](method func(Service, context.Context, Attachment, Q) (R, error)) opSpec {
	return opSpec{
		newRequest:  func() Request { return Q(new(QT)) },
		newResponse: func() message { return R(new(RT)) },
		serve: func(ctx context.Context, s Service, a Attachment, q Request) (message, error) {
			r, err := method(s, ctx, a, q.(Q))
			if err != nil {
				return nil, err
			}
			if r == nil {
				return nil, errNilResponse
			}
			return r, nil
		},
	}
}

// opSpecs is indexed by Op; each request type's Op method places its entry.
var opSpecs = func() (t [OpCancelRequest + 1]opSpec) {
	for _, s := range []opSpec{
		bind(Service.Describe), bind(Service.Attach), bind(Service.Detach),
		bind(Service.Lookup), bind(Service.Walk), bind(Service.GetAttr), bind(Service.SetAttr), bind(Service.Access),
		bind(Service.Open), bind(Service.Create), bind(Service.Read), bind(Service.Write),
		bind(Service.Flush), bind(Service.Fsync), bind(Service.Release),
		bind(Service.OpenDir), bind(Service.ReadDir), bind(Service.ReleaseDir),
		bind(Service.Mkdir), bind(Service.Unlink), bind(Service.Rmdir), bind(Service.Rename),
		bind(Service.Link), bind(Service.Symlink), bind(Service.Readlink),
		bind(Service.StatFS), bind(Service.Forget), bind(Service.GetLock), bind(Service.SetLock),
		{
			newRequest:  func() Request { return new(CancelRequestRequest) },
			newResponse: func() message { return new(CancelRequestResponse) },
		},
	} {
		t[s.newRequest().Op()] = s
	}
	return t
}()

// sideEffectFree reports whether r leaves no state behind, so abandoning it
// cannot have had an effect. Lookups and ReadDir WithAttrs acquire references.
func sideEffectFree(r Request) bool {
	switch r := r.(type) {
	case *DescribeRequest, *GetAttrRequest, *AccessRequest, *ReadRequest, *ReadlinkRequest, *StatFSRequest, *GetLockRequest:
		return true
	case *ReadDirRequest:
		return !r.WithAttrs
	}
	return false
}

// modifiesFiles reports whether r changes the file system, which a read-only
// attachment refuses.
func modifiesFiles(r Request) bool {
	switch r := r.(type) {
	case *SetAttrRequest, *CreateRequest, *WriteRequest, *MkdirRequest, *UnlinkRequest, *RmdirRequest,
		*RenameRequest, *LinkRequest, *SymlinkRequest:
		return true
	case *OpenRequest:
		return r.Access.Writes() || r.Flags&OpenTruncate != 0
	}
	return false
}

// Admit checks r against the declared capabilities and the attachment's
// read-only choice. A service calls it before running a request and returns
// the failure it reports. Limits a request can only be judged against service
// state, such as MaxOpenHandles, stay with the service.
func (c *Capabilities) Admit(r Request, readOnly bool) *Failure {
	unsupported := func(what string) *Failure {
		return NewFailure(CodeUnsupported, sandboxwire.EffectNone, what+" is not supported")
	}
	tooLong := func(n []byte, max uint32) bool { return uint64(len(n)) > uint64(max) }
	nameTooLong := NewErrnoFailure(ErrnoNameTooLong, sandboxwire.EffectNone, "name exceeds MaxNameBytes")
	if readOnly && modifiesFiles(r) {
		return NewErrnoFailure(ErrnoReadOnlyFilesystem, sandboxwire.EffectNone, "attachment is read-only")
	}
	switch r := r.(type) {
	case *AttachRequest:
		if !r.ReadOnly && c.ReadOnly {
			return unsupported("a writable attachment")
		}
	case *LookupRequest:
		if tooLong(r.Name, c.MaxNameBytes) {
			return nameTooLong
		}
	case *WalkRequest:
		if uint64(len(r.Names)) > uint64(c.MaxWalkComponents) {
			return NewFailure(CodeInvalidArgument, sandboxwire.EffectNone, "walk exceeds MaxWalkComponents")
		}
		for _, n := range r.Names {
			if tooLong(n, c.MaxNameBytes) {
				return nameTooLong
			}
		}
	case *SetAttrRequest:
		switch {
		case r.Set&AttrMode != 0 && !c.SetMode:
			return unsupported("setting the mode")
		case r.Set&(AttrUID|AttrGID) != 0 && !c.SetOwner:
			return unsupported("setting the owner")
		case r.Set&(AttrAtime|AttrMtime|AttrAtimeNow|AttrMtimeNow) != 0 && !c.SetTimes:
			return unsupported("setting times")
		}
	case *CreateRequest:
		if tooLong(r.Name, c.MaxNameBytes) {
			return nameTooLong
		}
	case *ReadRequest:
		if r.Size > c.MaxReadBytes {
			return NewFailure(CodeInvalidArgument, sandboxwire.EffectNone, "read exceeds MaxReadBytes")
		}
	case *WriteRequest:
		if tooLong(r.Data, c.MaxWriteBytes) {
			return NewFailure(CodeInvalidArgument, sandboxwire.EffectNone, "write exceeds MaxWriteBytes")
		}
	case *ReadDirRequest:
		if r.Limit > c.MaxReadDirBytes {
			return NewFailure(CodeInvalidArgument, sandboxwire.EffectNone, "limit exceeds MaxReadDirBytes")
		}
		if r.WithAttrs && !c.ReadDirPlus {
			return unsupported("ReadDir WithAttrs")
		}
	case *MkdirRequest:
		if tooLong(r.Name, c.MaxNameBytes) {
			return nameTooLong
		}
	case *UnlinkRequest:
		if tooLong(r.Name, c.MaxNameBytes) {
			return nameTooLong
		}
	case *RmdirRequest:
		if tooLong(r.Name, c.MaxNameBytes) {
			return nameTooLong
		}
	case *RenameRequest:
		switch {
		case tooLong(r.Name, c.MaxNameBytes) || tooLong(r.NewName, c.MaxNameBytes):
			return nameTooLong
		case r.Mode == RenameNoReplace && !c.RenameNoReplace:
			return unsupported("RenameNoReplace")
		case r.Mode == RenameExchange && !c.RenameExchange:
			return unsupported("RenameExchange")
		}
	case *LinkRequest:
		if !c.HardLinks {
			return unsupported("Link")
		}
		if tooLong(r.NewName, c.MaxNameBytes) {
			return nameTooLong
		}
	case *SymlinkRequest:
		switch {
		case !c.Symlinks:
			return unsupported("Symlink")
		case tooLong(r.Name, c.MaxNameBytes):
			return nameTooLong
		case tooLong(r.Target, c.MaxPathBytes):
			return NewErrnoFailure(ErrnoNameTooLong, sandboxwire.EffectNone, "target exceeds MaxPathBytes")
		}
	case *GetLockRequest:
		if !c.POSIXLocks {
			return unsupported("POSIX locks")
		}
	case *SetLockRequest:
		if r.Kind == LockPOSIX && !c.POSIXLocks {
			return unsupported("POSIX locks")
		}
		if r.Kind == LockFlock && !c.Flock {
			return unsupported("flock")
		}
	}
	return nil
}

// Codec. Every message has an ordered layout of sandboxwire primitives.
// Decoding is structural; validate then applies the protocol rules, and
// encoding validates first, so both directions enforce them.

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{sandboxwire.ErrMalformed}, args...)...)
}

// decoder keeps the first error and reads nothing after it.
type decoder struct {
	d   *sandboxwire.Decoder
	err error
}

func read[T any](d *decoder, f func() (T, error)) T {
	var v T
	if d.err == nil {
		v, d.err = f()
	}
	return v
}

func (d *decoder) u16() uint16        { return read(d, d.d.U16) }
func (d *decoder) u32() uint32        { return read(d, d.d.U32) }
func (d *decoder) u64() uint64        { return read(d, d.d.U64) }
func (d *decoder) i64() int64         { return read(d, d.d.I64) }
func (d *decoder) bool() bool         { return read(d, d.d.Bool) }
func (d *decoder) bytes() []byte      { return read(d, d.d.Bytes) }
func (d *decoder) present() bool      { return read(d, d.d.Present) }
func (d *decoder) id() sandboxwire.ID { return read(d, d.d.ID) }

func (d *decoder) count(max uint32) int {
	return read(d, func() (int, error) { return d.d.Count(max) })
}

func decodeMessage(m message, payload []byte) error {
	d := &decoder{d: sandboxwire.NewDecoder(payload)}
	m.decode(d)
	if d.err != nil {
		return d.err
	}
	if err := d.d.Finish(); err != nil {
		return err
	}
	return m.validate()
}

func encodeMessage(e *sandboxwire.Encoder, m message) error {
	if err := m.validate(); err != nil {
		return err
	}
	m.encode(e)
	return nil
}

func checkPayload(p []byte) ([]byte, error) {
	if len(p) > sandboxwire.MaxPayload {
		return nil, malformed("payload of %d bytes exceeds the frame limit", len(p))
	}
	return p, nil
}

func encodeRequest(r Request) ([]byte, error) {
	var e sandboxwire.Encoder
	if err := encodeMessage(&e, r); err != nil {
		return nil, err
	}
	return checkPayload(e.Payload())
}

func decodeRequest(op Op, payload []byte) (Request, error) {
	if op < 1 || op > OpCancelRequest {
		return nil, malformed("unknown request %d", uint16(op))
	}
	r := opSpecs[op].newRequest()
	return r, decodeMessage(r, payload)
}

// encodeResponse encodes a success carrying r, or the failure f when it is
// set.
func encodeResponse(r message, f *Failure) ([]byte, error) {
	var e sandboxwire.Encoder
	if f != nil {
		if err := f.validate(); err != nil {
			return nil, err
		}
		e.Enum(uint16(ResultFailure))
		f.encode(&e)
	} else {
		if err := r.validate(); err != nil {
			return nil, err
		}
		e.Enum(uint16(ResultSuccess))
		r.encode(&e)
	}
	return checkPayload(e.Payload())
}

func decodeResponse(op Op, payload []byte) (message, *Failure, error) {
	if op < 1 || op > OpCancelRequest {
		return nil, nil, malformed("unknown request %d", uint16(op))
	}
	d := &decoder{d: sandboxwire.NewDecoder(payload)}
	switch result := Result(d.u16()); {
	case d.err != nil:
		return nil, nil, d.err
	case result == ResultSuccess:
		r := opSpecs[op].newResponse()
		return r, nil, decodeRest(d, r)
	case result == ResultFailure:
		f := new(Failure)
		return nil, f, decodeRest(d, f)
	default:
		return nil, nil, malformed("result %d", result)
	}
}

func decodeRest(d *decoder, m message) error {
	m.decode(d)
	if d.err != nil {
		return d.err
	}
	if err := d.d.Finish(); err != nil {
		return err
	}
	return m.validate()
}

// Shared values.

func (r NodeRef) encode(e *sandboxwire.Encoder) { e.U64(r.ID); e.U64(r.Generation) }
func (r *NodeRef) decode(d *decoder)            { r.ID = d.u64(); r.Generation = d.u64() }
func (r NodeRef) validate() error {
	if r.ID == 0 || r.Generation == 0 {
		return malformed("node reference %d/%d", r.ID, r.Generation)
	}
	return nil
}

func (h HandleID) validate() error {
	if h == 0 {
		return malformed("zero handle")
	}
	return nil
}

func (t Timestamp) encode(e *sandboxwire.Encoder) { e.I64(t.Sec); e.U32(t.Nsec) }
func (t *Timestamp) decode(d *decoder)            { t.Sec = d.i64(); t.Nsec = d.u32() }
func (t Timestamp) validate() error {
	if t.Nsec >= 1e9 {
		return malformed("nanoseconds %d", t.Nsec)
	}
	return nil
}

func (a *Attr) encode(e *sandboxwire.Encoder) {
	e.U64(a.Ino)
	e.U32(a.Mode)
	e.U32(a.Nlink)
	e.U32(a.UID)
	e.U32(a.GID)
	e.U64(a.Rdev)
	e.U64(a.Size)
	e.U64(a.Blocks)
	e.U32(a.Blksize)
	a.Atime.encode(e)
	a.Mtime.encode(e)
	a.Ctime.encode(e)
}

func (a *Attr) decode(d *decoder) {
	a.Ino = d.u64()
	a.Mode = d.u32()
	a.Nlink = d.u32()
	a.UID = d.u32()
	a.GID = d.u32()
	a.Rdev = d.u64()
	a.Size = d.u64()
	a.Blocks = d.u64()
	a.Blksize = d.u32()
	a.Atime.decode(d)
	a.Mtime.decode(d)
	a.Ctime.decode(d)
}

func (a *Attr) validate() error {
	if !validFileType(a.Mode&ModeType) || a.Mode&^(ModeType|ModePerm) != 0 {
		return malformed("mode %#o", a.Mode)
	}
	if a.Size > maxOffset {
		return malformed("size %d", a.Size)
	}
	return errors.Join(a.Atime.validate(), a.Mtime.validate(), a.Ctime.validate())
}

func (en *Entry) encode(e *sandboxwire.Encoder) { en.Node.encode(e); en.Attr.encode(e) }
func (en *Entry) decode(d *decoder)             { en.Node.decode(d); en.Attr.decode(d) }
func (en *Entry) validate() error               { return errors.Join(en.Node.validate(), en.Attr.validate()) }

func (t *Target) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(t.Kind))
	if t.Kind == TargetNode {
		t.Node.encode(e)
	} else {
		e.U64(uint64(t.Handle))
	}
}

func (t *Target) decode(d *decoder) {
	switch t.Kind = TargetKind(d.u16()); t.Kind {
	case TargetNode:
		t.Node.decode(d)
	case TargetHandle:
		t.Handle = HandleID(d.u64())
	}
}

func (t *Target) validate() error {
	switch t.Kind {
	case TargetNode:
		if t.Handle != 0 {
			return malformed("node target with a handle")
		}
		return t.Node.validate()
	case TargetHandle:
		if t.Node != (NodeRef{}) {
			return malformed("handle target with a node")
		}
		return t.Handle.validate()
	}
	return malformed("target kind %d", t.Kind)
}

func (l *Lock) encode(e *sandboxwire.Encoder) { e.Enum(uint16(l.Mode)); e.U64(l.Start); e.U64(l.End) }
func (l *Lock) decode(d *decoder)             { l.Mode = LockMode(d.u16()); l.Start = d.u64(); l.End = d.u64() }
func (l *Lock) validate() error {
	if l.Mode < LockRead || l.Mode > LockUnlock {
		return malformed("lock mode %d", l.Mode)
	}
	if l.Start > l.End || l.End > maxOffset {
		return malformed("lock range %d-%d", l.Start, l.End)
	}
	return nil
}

func (f *Failure) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(f.Code))
	e.Present(f.Code == CodeErrno)
	if f.Code == CodeErrno {
		e.Enum(uint16(f.Errno))
	}
	e.Effect(f.Effect)
	e.Bytes([]byte(f.Message))
}

func (f *Failure) decode(d *decoder) {
	f.Code = ErrorCode(d.u16())
	if d.present() {
		if f.Errno = Errno(d.u16()); f.Errno == 0 && d.err == nil {
			d.err = malformed("zero errno")
		}
	}
	f.Effect = sandboxwire.Effect(d.u16())
	f.Message = string(d.bytes())
}

func (f *Failure) validate() error {
	switch {
	case !f.Code.Valid():
		return malformed("error code %d", f.Code)
	case (f.Code == CodeErrno) != (f.Errno != 0):
		return malformed("errno %d with code %s", f.Errno, f.Code)
	case f.Errno != 0 && !f.Errno.Valid():
		return malformed("errno %d", f.Errno)
	case !f.Effect.Valid():
		return malformed("effect %d", f.Effect)
	case len(f.Message) > maxMessageBytes:
		return malformed("message of %d bytes", len(f.Message))
	}
	return nil
}

func encodeOptionalFailure(e *sandboxwire.Encoder, f *Failure) {
	e.Present(f != nil)
	if f != nil {
		f.encode(e)
	}
}

func decodeOptionalFailure(d *decoder) *Failure {
	if !d.present() {
		return nil
	}
	f := new(Failure)
	f.decode(d)
	return f
}

func validName(n []byte) error {
	if len(n) == 0 || len(n) > maxNameBytes {
		return malformed("name of %d bytes", len(n))
	}
	if string(n) == "." || string(n) == ".." {
		return malformed("name %q", n)
	}
	for _, c := range n {
		if c == 0 || c == '/' {
			return malformed("name contains %q", c)
		}
	}
	return nil
}

func validTarget(t []byte) error {
	if len(t) == 0 || len(t) > maxTargetBytes {
		return malformed("symlink target of %d bytes", len(t))
	}
	for _, c := range t {
		if c == 0 {
			return malformed("symlink target contains NUL")
		}
	}
	return nil
}

func validPerm(m uint32) error {
	if m&^ModePerm != 0 {
		return malformed("permission bits %#o", m)
	}
	return nil
}

func validAccess(m AccessMode) error {
	if m < AccessRead || m > AccessReadWrite {
		return malformed("access mode %d", m)
	}
	return nil
}

func validOpenFlags(f OpenFlags) error {
	if f&^openFlagsAll != 0 {
		return malformed("open flags %#x", uint32(f))
	}
	return nil
}

func (c *Capabilities) encode(e *sandboxwire.Encoder) {
	e.Enum(uint16(c.PathProfile))
	e.Enum(uint16(c.CacheProfile))
	e.Enum(uint16(c.Durability))
	for _, v := range []uint32{c.MaxNameBytes, c.MaxPathBytes, c.MaxReadBytes, c.MaxWriteBytes, c.MaxWalkComponents, c.MaxReadDirBytes, c.MaxOpenHandles} {
		e.U32(v)
	}
	for _, v := range c.flags() {
		e.Bool(*v)
	}
}

func (c *Capabilities) decode(d *decoder) {
	c.PathProfile = PathProfile(d.u16())
	c.CacheProfile = CacheProfile(d.u16())
	c.Durability = Durability(d.u16())
	for _, v := range []*uint32{&c.MaxNameBytes, &c.MaxPathBytes, &c.MaxReadBytes, &c.MaxWriteBytes, &c.MaxWalkComponents, &c.MaxReadDirBytes, &c.MaxOpenHandles} {
		*v = d.u32()
	}
	for _, v := range c.flags() {
		*v = d.bool()
	}
}

// flags lists the boolean capabilities in wire order.
func (c *Capabilities) flags() []*bool {
	return []*bool{&c.ReadOnly, &c.AtomicAppend, &c.AtomicRename, &c.RenameNoReplace, &c.RenameExchange,
		&c.HardLinks, &c.Symlinks, &c.SetMode, &c.SetOwner, &c.SetTimes, &c.DirectoryFsync, &c.ReadDirPlus,
		&c.Flock, &c.POSIXLocks}
}

func (c *Capabilities) validate() error {
	within := func(v, max uint32) bool { return v >= 1 && v <= max }
	switch {
	case c.PathProfile != PathProfileLinuxBytes:
		return malformed("path profile %d", c.PathProfile)
	case c.CacheProfile != CacheProfileUncached:
		return malformed("cache profile %d", c.CacheProfile)
	case c.Durability != DurabilityFsyncRequired:
		return malformed("durability %d", c.Durability)
	case !within(c.MaxNameBytes, maxNameBytes), !within(c.MaxPathBytes, maxTargetBytes),
		!within(c.MaxReadBytes, sandboxwire.MaxChunk), !within(c.MaxWriteBytes, sandboxwire.MaxChunk),
		!within(c.MaxWalkComponents, maxWalkNames), !within(c.MaxReadDirBytes, maxReadDirBytes),
		c.MaxOpenHandles == 0:
		return malformed("capability limit out of range")
	case !c.ReadOnly && !(c.AtomicAppend && c.AtomicRename && c.HardLinks && c.Symlinks):
		return malformed("a writable service requires AtomicAppend, AtomicRename, HardLinks and Symlinks")
	}
	return nil
}

// Messages.

func (*DescribeRequest) encode(*sandboxwire.Encoder) {}
func (*DescribeRequest) decode(*decoder)             {}
func (*DescribeRequest) validate() error             { return nil }

func (r *DescribeResponse) encode(e *sandboxwire.Encoder) {
	e.ID(r.ServerInstanceID)
	e.U32(r.Identity.UID)
	e.U32(r.Identity.GID)
	r.Capabilities.encode(e)
	e.Count(len(r.Exports))
	for _, x := range r.Exports {
		e.Bytes([]byte(x))
	}
}

func (r *DescribeResponse) decode(d *decoder) {
	r.ServerInstanceID = d.id()
	r.Identity.UID = d.u32()
	r.Identity.GID = d.u32()
	r.Capabilities.decode(d)
	r.Exports = make([]sandboxlink.ExportID, d.count(sandboxlink.MaxExports))
	for i := range r.Exports {
		r.Exports[i] = sandboxlink.ExportID(d.bytes())
	}
}

func (r *DescribeResponse) validate() error {
	if r.ServerInstanceID.IsZero() {
		return malformed("zero server instance")
	}
	if len(r.Exports) > sandboxlink.MaxExports {
		return malformed("%d exports", len(r.Exports))
	}
	seen := make(map[sandboxlink.ExportID]bool, len(r.Exports))
	for _, x := range r.Exports {
		if !x.Valid() || seen[x] {
			return malformed("export %q", string(x))
		}
		seen[x] = true
	}
	return r.Capabilities.validate()
}

func (r *AttachRequest) encode(e *sandboxwire.Encoder) { e.Bytes([]byte(r.Export)); e.Bool(r.ReadOnly) }
func (r *AttachRequest) decode(d *decoder) {
	r.Export = sandboxlink.ExportID(d.bytes())
	r.ReadOnly = d.bool()
}
func (r *AttachRequest) validate() error {
	if !r.Export.Valid() {
		return malformed("export %q", string(r.Export))
	}
	return nil
}

func (r *AttachResponse) encode(e *sandboxwire.Encoder) { r.Root.encode(e) }
func (r *AttachResponse) decode(d *decoder)             { r.Root.decode(d) }
func (r *AttachResponse) validate() error {
	if r.Root.Attr.Mode&ModeType != ModeDirectory {
		return malformed("export root is not a directory")
	}
	return r.Root.validate()
}

func (*DetachRequest) encode(*sandboxwire.Encoder)  {}
func (*DetachRequest) decode(*decoder)              {}
func (*DetachRequest) validate() error              { return nil }
func (*DetachResponse) encode(*sandboxwire.Encoder) {}
func (*DetachResponse) decode(*decoder)             {}
func (*DetachResponse) validate() error             { return nil }

func (r *LookupRequest) encode(e *sandboxwire.Encoder) { r.Parent.encode(e); e.Bytes(r.Name) }
func (r *LookupRequest) decode(d *decoder)             { r.Parent.decode(d); r.Name = d.bytes() }
func (r *LookupRequest) validate() error               { return errors.Join(r.Parent.validate(), validName(r.Name)) }

func (r *LookupResponse) encode(e *sandboxwire.Encoder) { r.Entry.encode(e) }
func (r *LookupResponse) decode(d *decoder)             { r.Entry.decode(d) }
func (r *LookupResponse) validate() error               { return r.Entry.validate() }

func (r *WalkRequest) encode(e *sandboxwire.Encoder) {
	r.Parent.encode(e)
	e.Count(len(r.Names))
	for _, n := range r.Names {
		e.Bytes(n)
	}
}

func (r *WalkRequest) decode(d *decoder) {
	r.Parent.decode(d)
	r.Names = make([][]byte, d.count(maxWalkNames))
	for i := range r.Names {
		r.Names[i] = d.bytes()
	}
}

func (r *WalkRequest) validate() error {
	if len(r.Names) == 0 || len(r.Names) > maxWalkNames {
		return malformed("walk of %d names", len(r.Names))
	}
	for _, n := range r.Names {
		if err := validName(n); err != nil {
			return err
		}
	}
	return r.Parent.validate()
}

func (r *WalkResponse) encode(e *sandboxwire.Encoder) {
	e.Count(len(r.Entries))
	for i := range r.Entries {
		r.Entries[i].encode(e)
	}
	encodeOptionalFailure(e, r.Failure)
}

func (r *WalkResponse) decode(d *decoder) {
	r.Entries = make([]Entry, d.count(maxWalkNames))
	for i := range r.Entries {
		r.Entries[i].decode(d)
	}
	r.Failure = decodeOptionalFailure(d)
}

func (r *WalkResponse) validate() error {
	if len(r.Entries) == 0 || len(r.Entries) > maxWalkNames {
		return malformed("walk of %d entries", len(r.Entries))
	}
	for i := range r.Entries {
		if err := r.Entries[i].validate(); err != nil {
			return err
		}
	}
	if r.Failure != nil {
		return r.Failure.validate()
	}
	return nil
}

func (r *GetAttrRequest) encode(e *sandboxwire.Encoder) { r.Target.encode(e) }
func (r *GetAttrRequest) decode(d *decoder)             { r.Target.decode(d) }
func (r *GetAttrRequest) validate() error               { return r.Target.validate() }

func (r *GetAttrResponse) encode(e *sandboxwire.Encoder) { r.Attr.encode(e) }
func (r *GetAttrResponse) decode(d *decoder)             { r.Attr.decode(d) }
func (r *GetAttrResponse) validate() error               { return r.Attr.validate() }

func (r *SetAttrRequest) encode(e *sandboxwire.Encoder) {
	r.Target.encode(e)
	e.U32(uint32(r.Set))
	if r.Set&AttrSize != 0 {
		e.U64(r.Size)
	}
	if r.Set&AttrMode != 0 {
		e.U32(r.Mode)
	}
	if r.Set&AttrUID != 0 {
		e.U32(r.UID)
	}
	if r.Set&AttrGID != 0 {
		e.U32(r.GID)
	}
	if r.Set&AttrAtime != 0 {
		r.Atime.encode(e)
	}
	if r.Set&AttrMtime != 0 {
		r.Mtime.encode(e)
	}
}

func (r *SetAttrRequest) decode(d *decoder) {
	r.Target.decode(d)
	r.Set = AttrMask(d.u32())
	if r.Set&AttrSize != 0 {
		r.Size = d.u64()
	}
	if r.Set&AttrMode != 0 {
		r.Mode = d.u32()
	}
	if r.Set&AttrUID != 0 {
		r.UID = d.u32()
	}
	if r.Set&AttrGID != 0 {
		r.GID = d.u32()
	}
	if r.Set&AttrAtime != 0 {
		r.Atime.decode(d)
	}
	if r.Set&AttrMtime != 0 {
		r.Mtime.decode(d)
	}
}

func (r *SetAttrRequest) validate() error {
	unset := func(bit AttrMask, zero bool) bool { return r.Set&bit == 0 && !zero }
	switch {
	case r.Set&^attrMaskAll != 0:
		return malformed("attribute mask %#x", uint32(r.Set))
	case r.Set&AttrAtime != 0 && r.Set&AttrAtimeNow != 0, r.Set&AttrMtime != 0 && r.Set&AttrMtimeNow != 0:
		return malformed("a time is both given and now")
	case unset(AttrSize, r.Size == 0), unset(AttrMode, r.Mode == 0), unset(AttrUID, r.UID == 0),
		unset(AttrGID, r.GID == 0), unset(AttrAtime, r.Atime == Timestamp{}), unset(AttrMtime, r.Mtime == Timestamp{}):
		return malformed("an unselected attribute is set")
	case r.Size > maxOffset:
		return malformed("size %d", r.Size)
	case r.Set&AttrUID != 0 && r.UID == math.MaxUint32, r.Set&AttrGID != 0 && r.GID == math.MaxUint32:
		return malformed("owner 4294967295 means no change")
	}
	return errors.Join(r.Target.validate(), validPerm(r.Mode), r.Atime.validate(), r.Mtime.validate())
}

func (r *SetAttrResponse) encode(e *sandboxwire.Encoder) { r.Attr.encode(e) }
func (r *SetAttrResponse) decode(d *decoder)             { r.Attr.decode(d) }
func (r *SetAttrResponse) validate() error               { return r.Attr.validate() }

func (r *AccessRequest) encode(e *sandboxwire.Encoder) { r.Node.encode(e); e.U32(uint32(r.Mask)) }
func (r *AccessRequest) decode(d *decoder)             { r.Node.decode(d); r.Mask = AccessMask(d.u32()) }
func (r *AccessRequest) validate() error {
	if r.Mask&^accessMaskAll != 0 {
		return malformed("access mask %#x", uint32(r.Mask))
	}
	return r.Node.validate()
}

func (*AccessResponse) encode(*sandboxwire.Encoder) {}
func (*AccessResponse) decode(*decoder)             {}
func (*AccessResponse) validate() error             { return nil }

func (r *OpenRequest) encode(e *sandboxwire.Encoder) {
	r.Node.encode(e)
	e.Enum(uint16(r.Access))
	e.U32(uint32(r.Flags))
}

func (r *OpenRequest) decode(d *decoder) {
	r.Node.decode(d)
	r.Access = AccessMode(d.u16())
	r.Flags = OpenFlags(d.u32())
}

func (r *OpenRequest) validate() error {
	return errors.Join(r.Node.validate(), validAccess(r.Access), validOpenFlags(r.Flags))
}

func (r *OpenResponse) encode(e *sandboxwire.Encoder) { e.U64(uint64(r.Handle)) }
func (r *OpenResponse) decode(d *decoder)             { r.Handle = HandleID(d.u64()) }
func (r *OpenResponse) validate() error               { return r.Handle.validate() }

func (r *CreateRequest) encode(e *sandboxwire.Encoder) {
	r.Parent.encode(e)
	e.Bytes(r.Name)
	e.U32(r.Mode)
	e.Enum(uint16(r.Access))
	e.U32(uint32(r.Flags))
	e.Bool(r.Exclusive)
}

func (r *CreateRequest) decode(d *decoder) {
	r.Parent.decode(d)
	r.Name = d.bytes()
	r.Mode = d.u32()
	r.Access = AccessMode(d.u16())
	r.Flags = OpenFlags(d.u32())
	r.Exclusive = d.bool()
}

func (r *CreateRequest) validate() error {
	return errors.Join(r.Parent.validate(), validName(r.Name), validPerm(r.Mode), validAccess(r.Access), validOpenFlags(r.Flags))
}

func (r *CreateResponse) encode(e *sandboxwire.Encoder) { r.Entry.encode(e); e.U64(uint64(r.Handle)) }
func (r *CreateResponse) decode(d *decoder)             { r.Entry.decode(d); r.Handle = HandleID(d.u64()) }
func (r *CreateResponse) validate() error {
	return errors.Join(r.Entry.validate(), r.Handle.validate())
}

func (r *ReadRequest) encode(e *sandboxwire.Encoder) {
	e.U64(uint64(r.Handle))
	e.U64(r.Offset)
	e.U32(r.Size)
}
func (r *ReadRequest) decode(d *decoder) {
	r.Handle = HandleID(d.u64())
	r.Offset = d.u64()
	r.Size = d.u32()
}

func (r *ReadRequest) validate() error {
	if r.Offset > maxOffset || r.Size > sandboxwire.MaxChunk {
		return malformed("read of %d bytes at %d", r.Size, r.Offset)
	}
	return r.Handle.validate()
}

func (r *ReadResponse) encode(e *sandboxwire.Encoder) { e.Bytes(r.Data) }
func (r *ReadResponse) decode(d *decoder)             { r.Data = d.bytes() }
func (r *ReadResponse) validate() error {
	if len(r.Data) > sandboxwire.MaxChunk {
		return malformed("read of %d bytes", len(r.Data))
	}
	return nil
}

func (r *WriteRequest) encode(e *sandboxwire.Encoder) {
	e.U64(uint64(r.Handle))
	e.U64(r.Offset)
	e.Bytes(r.Data)
}
func (r *WriteRequest) decode(d *decoder) {
	r.Handle = HandleID(d.u64())
	r.Offset = d.u64()
	r.Data = d.bytes()
}

func (r *WriteRequest) validate() error {
	if len(r.Data) > sandboxwire.MaxChunk {
		return malformed("write of %d bytes", len(r.Data))
	}
	return r.Handle.validate()
}

func (r *WriteResponse) encode(e *sandboxwire.Encoder) {
	e.U32(r.Written)
	encodeOptionalFailure(e, r.Failure)
}
func (r *WriteResponse) decode(d *decoder) { r.Written = d.u32(); r.Failure = decodeOptionalFailure(d) }
func (r *WriteResponse) validate() error {
	if r.Written > sandboxwire.MaxChunk {
		return malformed("wrote %d bytes", r.Written)
	}
	if r.Failure != nil {
		if r.Written == 0 {
			return malformed("a write failure after no bytes belongs in a failure response")
		}
		return r.Failure.validate()
	}
	return nil
}

func (r *FlushRequest) encode(e *sandboxwire.Encoder) {
	e.U64(uint64(r.Handle))
	e.U64(uint64(r.Owner))
}
func (r *FlushRequest) decode(d *decoder) { r.Handle = HandleID(d.u64()); r.Owner = LockOwner(d.u64()) }
func (r *FlushRequest) validate() error   { return r.Handle.validate() }

func (*FlushResponse) encode(*sandboxwire.Encoder) {}
func (*FlushResponse) decode(*decoder)             {}
func (*FlushResponse) validate() error             { return nil }

func (r *FsyncRequest) encode(e *sandboxwire.Encoder) { e.U64(uint64(r.Handle)); e.Bool(r.DataOnly) }
func (r *FsyncRequest) decode(d *decoder)             { r.Handle = HandleID(d.u64()); r.DataOnly = d.bool() }
func (r *FsyncRequest) validate() error               { return r.Handle.validate() }

func (*FsyncResponse) encode(*sandboxwire.Encoder) {}
func (*FsyncResponse) decode(*decoder)             {}
func (*FsyncResponse) validate() error             { return nil }

func (r *ReleaseRequest) encode(e *sandboxwire.Encoder) { e.U64(uint64(r.Handle)) }
func (r *ReleaseRequest) decode(d *decoder)             { r.Handle = HandleID(d.u64()) }
func (r *ReleaseRequest) validate() error               { return r.Handle.validate() }

func (*ReleaseResponse) encode(*sandboxwire.Encoder) {}
func (*ReleaseResponse) decode(*decoder)             {}
func (*ReleaseResponse) validate() error             { return nil }

func (r *OpenDirRequest) encode(e *sandboxwire.Encoder) { r.Node.encode(e) }
func (r *OpenDirRequest) decode(d *decoder)             { r.Node.decode(d) }
func (r *OpenDirRequest) validate() error               { return r.Node.validate() }

func (r *OpenDirResponse) encode(e *sandboxwire.Encoder) { e.U64(uint64(r.Handle)) }
func (r *OpenDirResponse) decode(d *decoder)             { r.Handle = HandleID(d.u64()) }
func (r *OpenDirResponse) validate() error               { return r.Handle.validate() }

func (r *ReadDirRequest) encode(e *sandboxwire.Encoder) {
	e.U64(uint64(r.Handle))
	e.U64(r.Cookie)
	e.U32(r.Limit)
	e.Bool(r.WithAttrs)
}

func (r *ReadDirRequest) decode(d *decoder) {
	r.Handle = HandleID(d.u64())
	r.Cookie = d.u64()
	r.Limit = d.u32()
	r.WithAttrs = d.bool()
}

func (r *ReadDirRequest) validate() error {
	if r.Limit == 0 || r.Limit > maxReadDirBytes {
		return malformed("limit %d", r.Limit)
	}
	return r.Handle.validate()
}

func (r *ReadDirResponse) encode(e *sandboxwire.Encoder) {
	e.Count(len(r.Entries))
	for i := range r.Entries {
		x := &r.Entries[i]
		e.Bytes(x.Name)
		e.U64(x.Ino)
		e.U32(x.Type)
		e.U64(x.Cookie)
		e.Present(x.Entry != nil)
		if x.Entry != nil {
			x.Entry.encode(e)
		}
	}
	e.Bool(r.End)
}

func (r *ReadDirResponse) decode(d *decoder) {
	r.Entries = make([]DirEntry, d.count(maxReadDirBytes/dirEntryWireSize))
	for i := range r.Entries {
		x := &r.Entries[i]
		x.Name = d.bytes()
		x.Ino = d.u64()
		x.Type = d.u32()
		x.Cookie = d.u64()
		if d.present() {
			x.Entry = new(Entry)
			x.Entry.decode(d)
		}
	}
	r.End = d.bool()
}

func (r *ReadDirResponse) validate() error {
	if len(r.Entries) == 0 && !r.End {
		return malformed("an empty page before the end")
	}
	size := 0
	names := make(map[string]bool, len(r.Entries))
	cookies := make(map[uint64]bool, len(r.Entries))
	for i := range r.Entries {
		x := &r.Entries[i]
		if err := validName(x.Name); err != nil {
			return err
		}
		if names[string(x.Name)] || cookies[x.Cookie] {
			return malformed("entry %q repeats a name or cookie in the page", x.Name)
		}
		names[string(x.Name)], cookies[x.Cookie] = true, true
		if !validFileType(x.Type) {
			return malformed("entry type %#o", x.Type)
		}
		if x.Entry != nil {
			if err := x.Entry.validate(); err != nil {
				return err
			}
			if x.Entry.Attr.Ino != x.Ino || x.Entry.Attr.Mode&ModeType != x.Type {
				return malformed("entry attributes disagree with the entry")
			}
		}
		size += x.WireSize()
	}
	if size > maxReadDirBytes {
		return malformed("entries of %d bytes", size)
	}
	return nil
}

func (r *ReleaseDirRequest) encode(e *sandboxwire.Encoder) { e.U64(uint64(r.Handle)) }
func (r *ReleaseDirRequest) decode(d *decoder)             { r.Handle = HandleID(d.u64()) }
func (r *ReleaseDirRequest) validate() error               { return r.Handle.validate() }

func (*ReleaseDirResponse) encode(*sandboxwire.Encoder) {}
func (*ReleaseDirResponse) decode(*decoder)             {}
func (*ReleaseDirResponse) validate() error             { return nil }

func (r *MkdirRequest) encode(e *sandboxwire.Encoder) {
	r.Parent.encode(e)
	e.Bytes(r.Name)
	e.U32(r.Mode)
}
func (r *MkdirRequest) decode(d *decoder) { r.Parent.decode(d); r.Name = d.bytes(); r.Mode = d.u32() }
func (r *MkdirRequest) validate() error {
	return errors.Join(r.Parent.validate(), validName(r.Name), validPerm(r.Mode))
}

func (r *MkdirResponse) encode(e *sandboxwire.Encoder) { r.Entry.encode(e) }
func (r *MkdirResponse) decode(d *decoder)             { r.Entry.decode(d) }
func (r *MkdirResponse) validate() error               { return r.Entry.validate() }

func (r *UnlinkRequest) encode(e *sandboxwire.Encoder) { r.Parent.encode(e); e.Bytes(r.Name) }
func (r *UnlinkRequest) decode(d *decoder)             { r.Parent.decode(d); r.Name = d.bytes() }
func (r *UnlinkRequest) validate() error               { return errors.Join(r.Parent.validate(), validName(r.Name)) }

func (*UnlinkResponse) encode(*sandboxwire.Encoder) {}
func (*UnlinkResponse) decode(*decoder)             {}
func (*UnlinkResponse) validate() error             { return nil }

func (r *RmdirRequest) encode(e *sandboxwire.Encoder) { r.Parent.encode(e); e.Bytes(r.Name) }
func (r *RmdirRequest) decode(d *decoder)             { r.Parent.decode(d); r.Name = d.bytes() }
func (r *RmdirRequest) validate() error               { return errors.Join(r.Parent.validate(), validName(r.Name)) }

func (*RmdirResponse) encode(*sandboxwire.Encoder) {}
func (*RmdirResponse) decode(*decoder)             {}
func (*RmdirResponse) validate() error             { return nil }

func (r *RenameRequest) encode(e *sandboxwire.Encoder) {
	r.Parent.encode(e)
	e.Bytes(r.Name)
	r.NewParent.encode(e)
	e.Bytes(r.NewName)
	e.Enum(uint16(r.Mode))
}

func (r *RenameRequest) decode(d *decoder) {
	r.Parent.decode(d)
	r.Name = d.bytes()
	r.NewParent.decode(d)
	r.NewName = d.bytes()
	r.Mode = RenameMode(d.u16())
}

func (r *RenameRequest) validate() error {
	if r.Mode < RenameReplace || r.Mode > RenameExchange {
		return malformed("rename mode %d", r.Mode)
	}
	return errors.Join(r.Parent.validate(), validName(r.Name), r.NewParent.validate(), validName(r.NewName))
}

func (*RenameResponse) encode(*sandboxwire.Encoder) {}
func (*RenameResponse) decode(*decoder)             {}
func (*RenameResponse) validate() error             { return nil }

func (r *LinkRequest) encode(e *sandboxwire.Encoder) {
	r.Node.encode(e)
	r.NewParent.encode(e)
	e.Bytes(r.NewName)
}
func (r *LinkRequest) decode(d *decoder) {
	r.Node.decode(d)
	r.NewParent.decode(d)
	r.NewName = d.bytes()
}
func (r *LinkRequest) validate() error {
	return errors.Join(r.Node.validate(), r.NewParent.validate(), validName(r.NewName))
}

func (r *LinkResponse) encode(e *sandboxwire.Encoder) { r.Entry.encode(e) }
func (r *LinkResponse) decode(d *decoder)             { r.Entry.decode(d) }
func (r *LinkResponse) validate() error               { return r.Entry.validate() }

func (r *SymlinkRequest) encode(e *sandboxwire.Encoder) {
	r.Parent.encode(e)
	e.Bytes(r.Name)
	e.Bytes(r.Target)
}
func (r *SymlinkRequest) decode(d *decoder) {
	r.Parent.decode(d)
	r.Name = d.bytes()
	r.Target = d.bytes()
}
func (r *SymlinkRequest) validate() error {
	return errors.Join(r.Parent.validate(), validName(r.Name), validTarget(r.Target))
}

func (r *SymlinkResponse) encode(e *sandboxwire.Encoder) { r.Entry.encode(e) }
func (r *SymlinkResponse) decode(d *decoder)             { r.Entry.decode(d) }
func (r *SymlinkResponse) validate() error               { return r.Entry.validate() }

func (r *ReadlinkRequest) encode(e *sandboxwire.Encoder) { r.Node.encode(e) }
func (r *ReadlinkRequest) decode(d *decoder)             { r.Node.decode(d) }
func (r *ReadlinkRequest) validate() error               { return r.Node.validate() }

func (r *ReadlinkResponse) encode(e *sandboxwire.Encoder) { e.Bytes(r.Target) }
func (r *ReadlinkResponse) decode(d *decoder)             { r.Target = d.bytes() }
func (r *ReadlinkResponse) validate() error               { return validTarget(r.Target) }

func (r *StatFSRequest) encode(e *sandboxwire.Encoder) { r.Node.encode(e) }
func (r *StatFSRequest) decode(d *decoder)             { r.Node.decode(d) }
func (r *StatFSRequest) validate() error               { return r.Node.validate() }

func (r *StatFSResponse) encode(e *sandboxwire.Encoder) {
	for _, v := range []uint64{r.Blocks, r.BlocksFree, r.BlocksAvailable, r.Files, r.FilesFree} {
		e.U64(v)
	}
	e.U32(r.BlockSize)
	e.U32(r.FragmentSize)
	e.U32(r.NameMax)
}

func (r *StatFSResponse) decode(d *decoder) {
	for _, v := range []*uint64{&r.Blocks, &r.BlocksFree, &r.BlocksAvailable, &r.Files, &r.FilesFree} {
		*v = d.u64()
	}
	r.BlockSize = d.u32()
	r.FragmentSize = d.u32()
	r.NameMax = d.u32()
}

func (*StatFSResponse) validate() error { return nil }

func (r *ForgetRequest) encode(e *sandboxwire.Encoder) {
	e.Count(len(r.Entries))
	for _, f := range r.Entries {
		f.Node.encode(e)
		e.U64(f.Count)
	}
}

func (r *ForgetRequest) decode(d *decoder) {
	r.Entries = make([]ForgetEntry, d.count(maxForgetEntries))
	for i := range r.Entries {
		r.Entries[i].Node.decode(d)
		r.Entries[i].Count = d.u64()
	}
}

func (r *ForgetRequest) validate() error {
	if len(r.Entries) == 0 || len(r.Entries) > maxForgetEntries {
		return malformed("forget of %d entries", len(r.Entries))
	}
	seen := make(map[uint64]bool, len(r.Entries))
	for _, f := range r.Entries {
		if err := f.Node.validate(); err != nil {
			return err
		}
		if f.Count == 0 || seen[f.Node.ID] {
			return malformed("forget entry %d", f.Node.ID)
		}
		seen[f.Node.ID] = true
	}
	return nil
}

func (*ForgetResponse) encode(*sandboxwire.Encoder) {}
func (*ForgetResponse) decode(*decoder)             {}
func (*ForgetResponse) validate() error             { return nil }

func (r *GetLockRequest) encode(e *sandboxwire.Encoder) {
	e.U64(uint64(r.Handle))
	e.U64(uint64(r.Owner))
	r.Lock.encode(e)
}

func (r *GetLockRequest) decode(d *decoder) {
	r.Handle = HandleID(d.u64())
	r.Owner = LockOwner(d.u64())
	r.Lock.decode(d)
}

func (r *GetLockRequest) validate() error {
	if r.Lock.Mode == LockUnlock {
		return malformed("GetLock of an unlock")
	}
	return errors.Join(r.Handle.validate(), r.Lock.validate())
}

func (r *GetLockResponse) encode(e *sandboxwire.Encoder) {
	e.Present(r.Conflict != nil)
	if r.Conflict != nil {
		r.Conflict.encode(e)
	}
}

func (r *GetLockResponse) decode(d *decoder) {
	if d.present() {
		r.Conflict = new(Lock)
		r.Conflict.decode(d)
	}
}

func (r *GetLockResponse) validate() error {
	if r.Conflict == nil {
		return nil
	}
	if r.Conflict.Mode == LockUnlock {
		return malformed("conflict with an unlock")
	}
	return r.Conflict.validate()
}

func (r *SetLockRequest) encode(e *sandboxwire.Encoder) {
	e.U64(uint64(r.Handle))
	e.Enum(uint16(r.Kind))
	e.U64(uint64(r.Owner))
	r.Lock.encode(e)
	e.Bool(r.Wait)
}

func (r *SetLockRequest) decode(d *decoder) {
	r.Handle = HandleID(d.u64())
	r.Kind = LockKind(d.u16())
	r.Owner = LockOwner(d.u64())
	r.Lock.decode(d)
	r.Wait = d.bool()
}

func (r *SetLockRequest) validate() error {
	switch r.Kind {
	case LockPOSIX:
	case LockFlock:
		if r.Lock.Start != 0 || r.Lock.End != maxOffset {
			return malformed("a flock lock covers the whole file")
		}
	default:
		return malformed("lock kind %d", r.Kind)
	}
	return errors.Join(r.Handle.validate(), r.Lock.validate())
}

func (*SetLockResponse) encode(*sandboxwire.Encoder) {}
func (*SetLockResponse) decode(*decoder)             {}
func (*SetLockResponse) validate() error             { return nil }

func (r *CancelRequestRequest) encode(e *sandboxwire.Encoder) { e.U64(r.Target) }
func (r *CancelRequestRequest) decode(d *decoder)             { r.Target = d.u64() }
func (r *CancelRequestRequest) validate() error {
	if !sandboxwire.ValidRequestID(r.Target) {
		return malformed("cancel of request 0")
	}
	return nil
}

func (*CancelRequestResponse) encode(*sandboxwire.Encoder) {}
func (*CancelRequestResponse) decode(*decoder)             {}
func (*CancelRequestResponse) validate() error             { return nil }
