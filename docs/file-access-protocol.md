# File access protocol

The File access protocol is how a Runtime reads and changes the files of a sandbox. The Sandbox I/O service in the sandbox serves it, and the Runtime is its client. It is a node and handle protocol shaped like the FUSE low-level operations: lookups acquire node references, opens create handles under IDs the client chooses, reads and writes take offsets, directory reads resume at cookies, and locks work against the sandbox's own processes. Phase 1 serves the [Uncached](#uncached-profile) profile only, with no change stream.

[`internal/sandboxfs/protocol.go`](../internal/sandboxfs/protocol.go) is the authored definition: message tags, payload layouts, validators and the `Service` interface. The same package holds the generic client and server. [`apps/sandboxio/internal/fileservice`](../apps/sandboxio/internal/fileservice) is the Linux service. Frames use the shared [framing](sandbox-link-protocol.md#framing), and the Link layer supplies the authenticated attachment of each stream.

## Streams and attachments

- A stream belongs to one attachment, which Link authenticates and hands to the server as a `sandboxfs.Attachment`: its ID, the `ServerInstanceID` the stream was bound to, its lease and the exports it is granted. No request names an attachment, an OS user or a credential.
- Request IDs follow the [framing](sandbox-link-protocol.md#framing) rule, and a response carries its request's RequestID. Requests on one stream run concurrently, so responses can arrive in any order. The protocol has no events.
- An attachment attaches to one export. Its node references, handles and locks live in the service until it detaches, its lease ends or the service restarts. A new stream of the same attachment in the same incarnation continues with them, after the [succession fence](#stream-succession).
- A service incarnation is named by `ServerInstanceID`. A request on a stream bound to another incarnation fails with `InstanceChanged`, and nothing is reopened automatically.

## Implement a client

The Go client is `sandboxfs.NewClient(stream)`. It has one method per operation, is safe for concurrent use, and returns a `*sandboxfs.Failure` for every failure. Its methods map one to one onto the go-fuse node operations, so a FUSE frontend turns each kernel request into one call.

1. Call `Describe`. Keep `ServerInstanceID`, and check each request against `Capabilities` before sending it; the service rejects anything the capabilities do not declare.
2. `Attach` an export and keep the root `NodeRef`.
3. `Lookup`, `Walk`, `Create`, `Mkdir`, `Symlink`, `Link` and `ReadDir` with `WithAttrs` each acquire one reference on every node they return. Release references with `Forget` when the kernel forgets them.
4. `Walk` stops after a symlink. Resolve the link yourself, relative to the view, with `Readlink` and further walks.
5. `Open`, `Create` and `OpenDir` open a handle under a [handle ID](#handles) the client chooses. Keep one `sandboxfs.HandleIDs` per attachment, across all of its streams, and take each ID from it. Send `Flush` on each close of a descriptor for the handle, and `Release` or `ReleaseDir` when the last one closes.
6. Set `Append` on each `Write` made while the descriptor is in append mode. Append is a property of the write, not of the handle.
7. Read the `Effect` of every failure. After `EffectPossible`, the request may have taken effect: never replay a mutation automatically. Report the failure, or inspect the state with `GetAttr` or `Lookup` first. A failure with a [retryable](#failures) code and `EffectNone` may be resent unchanged.
8. Clean up an acquisition whose outcome is unknown, because its reply was lost or its call was cancelled, with `Release` or `ReleaseDir` of its ID. After a stream fails, resume on a new stream of the same attachment, which the service serves only after the failed stream's requests have finished, and clean up there: an uncertain `Attach` with `Detach`, and each uncertain acquisition with `Release` or `ReleaseDir`. [Handles](#handles) says what the cleanup proves.

Cancelling a call's context returns at once with `Cancelled` or `DeadlineExceeded`. A call cancelled before its request is written fails with `EffectNone`. After the request is written, the client sends `CancelRequest` for it and discards the late response, and the failure is `EffectNone` for a [side-effect-free request](#effects-and-cancellation) and `EffectPossible` for every other one. Cancelling a call while its request is being written fails the stream instead, because a partial frame cannot be withdrawn. A cancel that races the completion of a write may still fail the stream, and the requests in flight then fail with `EffectPossible`.

To learn what a cancelled request did, interrupt it instead: a call whose context comes from `sandboxfs.WithInterrupt` sends `CancelRequest` when the interrupt channel closes and keeps waiting for the request's own response, so it returns the request's result or the service's failure with its effect. A FUSE frontend uses this for a waiting lock, which may be acquired just before the cancellation arrives.

When the stream fails, every request in flight fails with `Unknown` and `EffectPossible`, and later calls fail with `EffectNone`; `errors.Is(err, sandboxfs.ErrTransport)` matches both. `Client.Done` closes and `Client.Err` returns the cause. To continue, open a new stream for the same attachment with `ExpectedServerInstanceID` set, as the [Sandbox link protocol](sandbox-link-protocol.md) describes. `InstanceChanged` then means every node, handle and lock of the attachment is gone.

## Implement a service

Implement `sandboxfs.Service`, create one `sandboxfs.NewServer(service)`, and serve every stream with `server.Serve(ctx, stream, attachment, seq)`, where `seq` is the stream's [Link bind sequence](sandbox-link-protocol.md#implement-a-serve-peer). One `Server` serves all of a service's streams, because the [succession fence](#stream-succession) spans them. `Serve`:

- refuses an attachment without an ID, a `ServerInstanceID`, a lease or at least one valid export grant with unique IDs;
- admits one stream per `(ServerInstanceID, AttachmentID)` at a time, in bind order: a successor stream waits, without a deadline, until its predecessor has drained, the superseded `Serve` returns `sandboxfs.ErrSuperseded`, and so does the `Serve` of a stream bound before one already admitted, without dispatching any of its requests;
- decodes and validates each request, and answers a malformed payload with `InvalidArgument` and `EffectNone`;
- ends the stream on a framing violation: an unknown tag, a frame that is not a request, or a RequestID that does not increase;
- holds up to `sandboxfs.MaxInFlight` (256) requests, each from admission until its response is written, and answers any more with `ResourceExhausted` and `EffectNone`, so a `CancelRequest` arrives while the client reads responses;
- answers `CancelRequest` itself by cancelling the target's context;
- refuses an `Open`, `Create` or `OpenDir` whose handle ID another acquisition on the stream is still using, with `InvalidArgument` and `EffectNone`, and runs a `Release` or `ReleaseDir` of an ID only after the running acquisition of that ID has finished. Together with the succession fence, a service never runs two acquisitions of one ID at once, or a release concurrently with the acquisition of its ID;
- returns a method's `*Failure` as the typed failure, and reports any other error, or a response that fails validation, as `Unknown` with `EffectPossible`;
- cancels every request's context when the stream ends.

A service must:

- generate a new `ServerInstanceID` whenever it loses its node and handle tables, and answer a request whose `Attachment.ServerInstanceID` is not its own with `InstanceChanged`;
- answer `StaleAttachment` while the attachment is not attached or after its lease ends, and `StaleNode` or `StaleHandle` for a reference or handle the attachment does not hold. A node ID is reused only with a new generation;
- reserve the handle ID of an `Open`, `Create` or `OpenDir` atomically before any file-system effect, as [Handles](#handles) describes, and publish every state a request creates before its method returns;
- call `Capabilities.Admit(request, readOnly)` before running a request and return the failure it reports. Limits that depend on service state, such as `MaxOpenHandles`, stay with the service;
- advertise only what it enforces, and advertise locks only when they interoperate with native processes in the sandbox;
- act as its own process identity, never as an identity a request supplies, and apply requested permission bits exactly;
- resolve each name as one entry of a directory node and never traverse a symlink;
- report `EffectPossible` once any part of a mutation may have applied.

### The Linux service

`fileservice.New(root)` serves the absolute directory `root` as the one export `world`, and `Describe` lists `world` only to an attachment granted it. `oac-sandbox-io` passes `/`; the Provider's sandbox setup owns the isolation of everything under it, as the [Sandbox bootstrap](sandbox-bootstrap.md#responsibilities-and-readiness) states, and the service enforces no boundary inside the export. `New` sets the process umask to zero and reports its effective UID and GID as `Identity`. `InstanceID` returns the `ServerInstanceID` to give Link, and `Close` releases every attachment.

- Each node holds an `O_PATH|O_NOFOLLOW` descriptor. In an attachment a node is one mount ID, device and inode, so hard links share a node while a bind mount and its source stay two. The mount ID comes from `statx` with `STATX_MNT_ID`, or from the `mnt_id` line of `/proc/self/fdinfo/<fd>` on kernels older than 5.8.
- A lookup opens one component with `openat` and `O_NOFOLLOW` on its parent's descriptor. A symlink, including a proc magic link such as `/proc/<pid>/cwd`, is a node of its own and is never traversed: `Lookup` and `Readlink` return the link itself, a directory operation on it fails with `Errno` `NotDirectory`, and `Open` fails with `SymlinkLoop`.
- Operations on a node, such as opening, truncating, changing its mode or times and linking it, go through the `/proc/self/fd` name of the descriptor the service holds for it. That name resolves to the descriptor's own object, never to a symlink's target, and an open through it first checks the object's file type.
- An open handle holds its own descriptor, so it keeps working after its file is unlinked or renamed. Files are opened without `O_APPEND`. A handle's writes run one at a time, and each first sets or clears the descriptor's `O_APPEND` to match its `Append`; an append is then one `write` call. The service does not use `pwritev2` with `RWF_APPEND`, which overlayfs on some kernels drops, writing at the offset instead. A short append is reported as it is and never continued by another append.
- `Rename` uses `renameat2`. The service declares `RenameNoReplace` and `RenameExchange` only when a probe at start succeeds.
- `LockFlock` locks the handle's descriptor, so it interoperates with native `flock`. `POSIXLocks` is false, and `GetLock` and `LockPOSIX` return `Unsupported`.
- `ReadDir` cookies are the kernel's directory offsets. `Attr.Ino` combines the device and the inode number as go-fuse's loopback does.
- It declares `MaxNameBytes` 255, `MaxPathBytes` 4095, `MaxReadBytes` and `MaxWriteBytes` 64 KiB, `MaxWalkComponents` 256, `MaxReadDirBytes` 64 KiB and `MaxOpenHandles` 4096, and every flag except `ReadOnly` and `POSIXLocks`, with the rename modes as probed.

## Reference

### Messages

Requests use tags 1 to 30; the response to tag `t` uses `t | 0x8000`.

| Tag | Request | Fields | Response | Meaning |
| --- | --- | --- | --- | --- |
| 1 | `Describe` | – | `ServerInstanceID`, `Identity`, `Capabilities`, `Exports` | The incarnation, identity, capabilities and granted exports |
| 2 | `Attach` | `Export`, `ReadOnly` | `Root` (`Entry`) | Attach to a granted export. The root is a directory |
| 3 | `Detach` | – | – | Release every node, handle and lock of the attachment |
| 4 | `Lookup` | `Parent`, `Name` | `Entry` | Look up one entry |
| 5 | `Walk` | `Parent`, `Names` | `Entries`, optional `Failure` | Look up components in turn. Stops after a symlink, or at a failure that `Failure` reports; a failure at the first name fails the request |
| 6 | `GetAttr` | `Target` | `Attr` | Attributes of a node or open handle |
| 7 | `SetAttr` | `Target`, `Set`, selected values | `Attr` | Change the selected attributes |
| 8 | `Access` | `Node`, `Mask` | – | Check permissions as the service's identity |
| 9 | `Open` | `Handle`, `Node`, `Access`, `Flags` | – | Open a regular file as `Handle` |
| 10 | `Create` | `Handle`, `Parent`, `Name`, `Mode`, `Access`, `Flags`, `Exclusive` | `Entry` | Create and open a regular file as `Handle` |
| 11 | `Read` | `Handle`, `Offset`, `Size` | `Data` | Fewer bytes than asked means end of file |
| 12 | `Write` | `Handle`, `Offset`, `Append`, `Data` | `Written`, optional `Failure` | Write at `Offset`, or at the end of the file when `Append` is set |
| 13 | `Flush` | `Handle`, `Owner` | – | One descriptor for the handle closed |
| 14 | `Fsync` | `Handle`, `DataOnly` | – | Make the file's data, and its metadata unless `DataOnly`, durable |
| 15 | `Release` | `Handle` | – | Close a file handle |
| 16 | `OpenDir` | `Handle`, `Node` | – | Open a directory as `Handle` |
| 17 | `ReadDir` | `Handle`, `Cookie`, `Limit`, `WithAttrs` | `Entries`, `End` | Read entries after `Cookie` |
| 18 | `ReleaseDir` | `Handle` | – | Close a directory handle |
| 19 | `Mkdir` | `Parent`, `Name`, `Mode` | `Entry` | Create a directory |
| 20 | `Unlink` | `Parent`, `Name` | – | Remove a non-directory entry |
| 21 | `Rmdir` | `Parent`, `Name` | – | Remove an empty directory |
| 22 | `Rename` | `Parent`, `Name`, `NewParent`, `NewName`, `Mode` | – | Rename in one [mode](#rename) |
| 23 | `Link` | `Node`, `NewParent`, `NewName` | `Entry` | Create a hard link |
| 24 | `Symlink` | `Parent`, `Name`, `Target` | `Entry` | Create a symlink |
| 25 | `Readlink` | `Node` | `Target` | Read a symlink |
| 26 | `StatFS` | `Node` | File-system statistics | Statistics of the file system holding the node |
| 27 | `Forget` | `Entries` | – | Release lookup references, all or none |
| 28 | `GetLock` | `Handle`, `Owner`, `Lock` | optional `Conflict` | A POSIX lock that would conflict |
| 29 | `SetLock` | `Handle`, `Kind`, `Owner`, `Lock`, `Wait` | – | Acquire, convert or release a lock |
| 30 | `CancelRequest` | `Target` (a RequestID) | – | Ask to cancel an outstanding request |

A response payload begins with a uint16 result: 1 for success, followed by the response's fields, or 2 for failure, followed by a [`Failure`](#failures). Payloads list their fields in this order:

```text
Describe            (no fields)
DescribeResponse    ServerInstanceID ID, Identity {UID u32, GID u32}, Capabilities, Exports count 0..64 of bytes
Attach              Export bytes, ReadOnly bool
AttachResponse      Root Entry
Detach              (no fields); response (no fields)
Lookup              Parent NodeRef, Name bytes
LookupResponse      Entry
Walk                Parent NodeRef, Names count 1..1024 of bytes
WalkResponse        Entries count 1..1024 of Entry, Failure optional Failure
GetAttr             Target
GetAttrResponse     Attr
SetAttr             Target, Set u32, then for each selected bit in order: Size u64, Mode u32, UID u32, GID u32, Atime Timestamp, Mtime Timestamp
SetAttrResponse     Attr
Access              Node NodeRef, Mask u32; response (no fields)
Open                Handle u64, Node NodeRef, Access enum, Flags u32; response (no fields)
Create              Handle u64, Parent NodeRef, Name bytes, Mode u32, Access enum, Flags u32, Exclusive bool
CreateResponse      Entry
Read                Handle u64, Offset u64, Size u32
ReadResponse        Data bytes
Write               Handle u64, Offset u64, Append bool, Data bytes
WriteResponse       Written u32, Failure optional Failure
Flush               Handle u64, Owner u64; response (no fields)
Fsync               Handle u64, DataOnly bool; response (no fields)
Release             Handle u64; response (no fields)
OpenDir             Handle u64, Node NodeRef; response (no fields)
ReadDir             Handle u64, Cookie u64, Limit u32, WithAttrs bool
ReadDirResponse     Entries count of DirEntry, End bool
ReleaseDir          Handle u64; response (no fields)
Mkdir               Parent NodeRef, Name bytes, Mode u32
MkdirResponse       Entry
Unlink, Rmdir       Parent NodeRef, Name bytes; response (no fields)
Rename              Parent NodeRef, Name bytes, NewParent NodeRef, NewName bytes, Mode enum; response (no fields)
Link                Node NodeRef, NewParent NodeRef, NewName bytes
LinkResponse        Entry
Symlink             Parent NodeRef, Name bytes, Target bytes
SymlinkResponse     Entry
Readlink            Node NodeRef
ReadlinkResponse    Target bytes
StatFS              Node NodeRef
StatFSResponse      Blocks u64, BlocksFree u64, BlocksAvailable u64, Files u64, FilesFree u64, BlockSize u32, FragmentSize u32, NameMax u32
Forget              Entries count 1..4096 of {Node NodeRef, Count u64}; response (no fields)
GetLock             Handle u64, Owner u64, Lock
GetLockResponse     Conflict optional Lock
SetLock             Handle u64, Kind enum, Owner u64, Lock, Wait bool; response (no fields)
CancelRequest       Target u64; response (no fields)
```

[`testdata`](../internal/sandboxfs/testdata) holds annotated golden frames of `Describe`, `Walk`, `Create`, an append `Write`, a short `Write`, `ReadDir` with a cookie, `Rename` and a failure.

### Shared types

```text
NodeRef       ID u64, Generation u64                  // both nonzero
HandleID      u64                                     // nonzero, chosen by the client
Timestamp     Sec i64, Nsec u32                       // Nsec below one billion
Attr          Ino u64, Mode u32, Nlink u32, UID u32, GID u32, Rdev u64, Size u64, Blocks u64, Blksize u32, Atime Timestamp, Mtime Timestamp, Ctime Timestamp
Entry         Node NodeRef, Attr
Target        Kind enum (TargetNode = 1, TargetHandle = 2), then Node NodeRef or Handle u64
DirEntry      Name bytes, Ino u64, Type u32, Cookie u64, Entry optional Entry
Lock          Mode enum (LockRead = 1, LockWrite = 2, LockUnlock = 3), Start u64, End u64
```

- A `NodeRef` and a `HandleID` are scoped to the attachment and the service incarnation. A node ID is reused only after its object is forgotten, and then with another generation, so a stale `NodeRef` never names another object.
- `Attr.Mode` holds exactly one file type and the permission bits (`0o7777`) in the Linux `st_mode` layout. `Size` is at most 2^63−1, and `Blocks` counts 512-byte blocks. `Ino` identifies the file within the export.
- A `DirEntry`'s `Type` is one file type in the same layout. When `Entry` is present, its `Attr.Ino` and file type equal the entry's.
- `Blocks`, `Files` and the other `StatFSResponse` fields follow `statfs`: `BlocksAvailable` is what an unprivileged process may use.

### Describe and capabilities

`DescribeResponse` carries the incarnation, the identity, the capabilities and the declared exports the attachment is granted: 0 to 64 unique `ExportID`s, with the grammar of Link's [export grants](sandbox-link-protocol.md#opening-a-stream). `Identity` is the effective UID and GID the service runs as, which own the files it creates; it is informational, and no request carries or selects an identity.

`Capabilities` encodes every field, in this order:

| Field | Type | Meaning |
| --- | --- | --- |
| `PathProfile` | enum | `LinuxBytes` (1): names and symlink targets are Linux byte strings |
| `CacheProfile` | enum | `Uncached` (1), the [Uncached profile](#uncached-profile) |
| `Durability` | enum | `FsyncRequired` (1): data is durable only after `Fsync` succeeds |
| `MaxNameBytes` | u32 | Longest entry name, 1 to 1024 |
| `MaxPathBytes` | u32 | Longest symlink target, 1 to 4096 |
| `MaxReadBytes`, `MaxWriteBytes` | u32 | Largest `Read` size and `Write` data, 1 to 64 KiB |
| `MaxWalkComponents` | u32 | Most `Walk` names, 1 to 1024 |
| `MaxReadDirBytes` | u32 | Largest `ReadDir` limit, 1 to 256 KiB |
| `MaxOpenHandles` | u32 | Most open handles per attachment, at least 1 |
| `ReadOnly` | bool | The service accepts only read-only attachments |
| `AtomicAppend` | bool | `Write` supports `Append`, and appends from several handles never interleave within a write |
| `AtomicRename` | bool | `RenameReplace` replaces the destination atomically |
| `RenameNoReplace`, `RenameExchange` | bool | The rename mode is supported |
| `HardLinks`, `Symlinks` | bool | `Link` and `Symlink` are supported |
| `SetMode`, `SetOwner`, `SetTimes` | bool | `SetAttr` may set the mode, the owner and the times |
| `DirectoryFsync` | bool | `Fsync` of a directory handle makes its entries durable |
| `ReadDirPlus` | bool | `ReadDir` supports `WithAttrs` |
| `Flock` | bool | `SetLock` supports `LockFlock` |
| `POSIXLocks` | bool | `GetLock` and `SetLock` support `LockPOSIX` |

A writable service, one without `ReadOnly`, declares `AtomicAppend`, `AtomicRename`, `HardLinks` and `Symlinks`. A request beyond a declared limit fails with `InvalidArgument`, or `Errno` `NameTooLong` for a name or target, and a request for an undeclared feature fails with `Unsupported`.

### Attach

`Attach` selects one export by `ExportID`; it never takes a server path. The export must be one that the attachment's Link binding grants in `Attachment.Exports` (see the [Sandbox link protocol](sandbox-link-protocol.md)), and a read-only grant allows only `ReadOnly` attaches; otherwise `Attach` fails with `Unauthorized`. A granted export the service does not declare fails with `InvalidArgument`, as does a second `Attach` before `Detach`. Every request that changes files on a read-only attachment fails with `Errno` `ReadOnlyFilesystem`: `SetAttr`, `Create`, `Write`, `Mkdir`, `Unlink`, `Rmdir`, `Rename`, `Link`, `Symlink`, and `Open` for writing or with `OpenTruncate`.

### Names and paths

- Names and symlink targets are bytes, not UTF-8 strings.
- An entry name is 1 to 1024 bytes without NUL or `/`, and is never `.` or `..`. `ReadDir` never returns `.` or `..`.
- A symlink target is 1 to 4096 bytes without NUL. The service stores and returns it unchanged; the client resolves it.
- No request resolves a path or traverses a symlink. Every lookup names one entry of a directory node, and a request that needs a directory fails with `Errno` `NotDirectory` on any other node.

### Attributes

`SetAttr` addresses a node or an open handle and changes only the attributes its `Set` mask selects:

| Bit | Name | Value |
| --- | --- | --- |
| `0x01` | `AttrSize` | `Size`: truncate or extend a regular file |
| `0x02` | `AttrMode` | `Mode`: permission bits only |
| `0x04` | `AttrUID` | `UID` |
| `0x08` | `AttrGID` | `GID` |
| `0x10` | `AttrAtime` | `Atime` |
| `0x20` | `AttrMtime` | `Mtime` |
| `0x40` | `AttrAtimeNow` | None: the access time becomes the service's current time |
| `0x80` | `AttrMtimeNow` | None: the modification time becomes the service's current time |

Unknown bits are rejected, `AttrAtime` excludes `AttrAtimeNow` and `AttrMtime` excludes `AttrMtimeNow`, and an unselected value must be zero. A selected `UID` or `GID` of 4294967295, which `chown` reads as no change, is rejected. Setting the mode of a symlink fails with `Errno` `NotSupported`; times set on a symlink apply to the link itself. The response carries the attributes after the change.

`Access` checks the `Mask` bits `MayExecute` (1), `MayWrite` (2) and `MayRead` (4) as the service's identity; a zero mask checks existence.

### Files

- `AccessMode` is `AccessRead` (1), `AccessWrite` (2) or `AccessReadWrite` (3).
- `OpenFlags` are `OpenTruncate` (1), `OpenNoFollow` (2), `OpenSync` (4) and `OpenDataSync` (8); unknown flags are rejected.
- `Open` opens a regular file node. A node is an object, not a path, so `Open` never follows a symlink: a directory fails with `Errno` `IsDirectory`, a symlink with `SymlinkLoop`, and a special file with `Unsupported`.
- `Create` creates a regular file with exactly the requested permission bits; no umask applies. With `Exclusive`, an existing entry fails with `Errno` `Exists`. Without it, an existing regular file is opened, and `OpenTruncate` truncates it.
- `Read` takes an offset up to 2^63−1. A positioned `Write` must end at or before 2^63−1, or it fails with `InvalidArgument`.
- A `Write` with `Append` ignores `Offset` and writes its data atomically at the end of the file. Any handle that may write takes both kinds of write, in any order, as a native descriptor does when `fcntl` sets or clears `O_APPEND`. A service without `AtomicAppend` refuses `Append` with `Unsupported`.
- A successful `WriteResponse` carries the exact number of bytes written. When a write stops after a nonzero prefix, `Written` counts the prefix and `Failure` says why it stopped; a write that wrote nothing is a failure response.
- `Flush` is neither `Fsync` nor `Release`. It reports the errors of closing one descriptor for the handle, and `Owner` names the closing lock owner.
- A file operation on a directory handle, or a directory operation on a file handle, fails with `Errno` `IsDirectory`, `NotDirectory` or `BadDescriptor`.

### Handles

- The client chooses the `HandleID` of each `Open`, `Create` and `OpenDir`: nonzero, and never used before in the attachment, on any of its streams. `sandboxfs.HandleIDs` allocates IDs in increasing order.
- The service reserves the ID before the request has any file-system effect. A reserved ID counts toward `MaxOpenHandles` while its acquisition runs. An acquisition whose ID is reserved or open fails with `InvalidArgument` and `EffectNone`.
- Other requests that name a reserved ID fail with `StaleHandle`, except `Release` and `ReleaseDir`: the server runs them only after the acquisition of that ID has finished, so they close the handle it opened.
- `Release` or `ReleaseDir` of an ID settles an acquisition whose outcome is unknown, whether its reply was lost with the stream or its call was cancelled. Success or `StaleHandle` proves that no handle with that ID remains. It does not prove that the acquisition changed nothing: a `Create` may have created its file, and a truncating `Open` may have truncated it.
- A node reference acquired by a `Create`, `Lookup`, `Walk`, `Mkdir`, `Symlink`, `Link` or `ReadDir` with `WithAttrs` whose reply was lost cannot be forgotten, because the client never learned the `NodeRef`. It stays held, with the descriptor the service keeps for its node, until the attachment detaches or its lease ends. Only the requests in flight when a stream fails, at most `MaxInFlight`, leave such references. To cancel one of these requests without losing its reply, interrupt it with `sandboxfs.WithInterrupt` and forget what it returns.

### Stream succession

For each `(ServerInstanceID, AttachmentID)` the server admits one File stream at a time. Before it dispatches any request of a successor stream, it stops admission on the predecessor, closes and cancels the predecessor's requests, and waits for every admitted handler and state-publication task to finish. The predecessor's replies are discarded. The gate exists before `Attach` creates any state.

Succession follows Link's bind order, not the order in which streams reach the server: a stream bound before one already admitted is refused before it dispatches anything. No timeout ends the wait, because a handler may still change state after its cancellation. Effects that completed stay. A request the successor sends therefore sees everything its predecessor's requests did: a `Detach` cleans up an `Attach` whose reply was lost, and a `Release` cleans up a lost acquisition.

### Directories

- `OpenDir` opens a directory node, and `ReadDir` reads its entries after `Cookie`; cookie 0 is the start. Each `DirEntry.Cookie` is the position after that entry and is otherwise opaque.
- `Limit` bounds the sum of the returned entries' encoded sizes: 25 bytes plus the name for each entry, plus 104 when it carries an `Entry`. A limit too small for the first entry fails with `Errno` `InvalidArgument`.
- With `WithAttrs`, each returned entry carries an `Entry` with one lookup reference.
- `End` is true when no entries remain, and a page without entries always has it set. No name or cookie repeats within a page. `ReadDir` promises no snapshot of a directory that changes while it is read.

### Rename

`Rename` takes exactly one mode: `RenameReplace` (1) replaces an existing destination, `RenameNoReplace` (2) fails with `Errno` `Exists` when the destination exists, and `RenameExchange` (3) swaps two existing entries.

### Locks

- A `Lock` covers `Start` through `End` inclusive; `End` 2^63−1 extends to the end of the file.
- `SetLock` takes `LockPOSIX` (1) or `LockFlock` (2), an attachment-scoped `LockOwner`, a range, a mode and `Wait`. A flock lock covers the whole file, so its range is 0 to 2^63−1. Without `Wait`, a conflicting lock fails with `Errno` `Again`; with it, the request waits until the lock is free or the request is cancelled.
- `GetLock` asks for a POSIX lock that would conflict with a read or write `Lock`, and returns it in `Conflict`.
- A service advertises a lock kind only when its locks interoperate with native processes in the sandbox. A client-local lock table is not enough.

### Failures

```text
Failure
  Code     enum
  Errno    optional enum    // present exactly when Code is Errno
  Effect   Effect
  Message  bytes            // at most 1024 bytes, for people
```

| Code | Name | Returned when |
| --- | --- | --- |
| 1 | `InvalidArgument` | A payload fails validation, a request exceeds a declared limit, the export is unknown, the attachment is already attached, an acquisition names a reserved or open handle ID, or `Forget` exceeds the references held |
| 2 | `Unsupported` | The capabilities do not declare the operation or option, or the request needs a feature phase 1 excludes |
| 3 | `Unauthorized` | `Attach` names an export the Link binding does not grant, or asks for write access to a read-only grant |
| 4 | `StaleAttachment` | The attachment is not attached, has detached or its lease ended |
| 5 | `InstanceChanged` | The stream is bound to another service incarnation |
| 6 | `StaleNode` | The attachment holds no such `NodeRef` |
| 7 | `StaleHandle` | The attachment has no such open handle |
| 8 | `ResourceExhausted` | The stream holds `MaxInFlight` requests, or the attachment has `MaxOpenHandles` handles, counting reserved IDs |
| 9 | `Cancelled` | The request was cancelled |
| 10 | `DeadlineExceeded` | The caller's deadline passed |
| 11 | `Errno` | A file-system call failed; `Errno` says how |
| 12 | `Unknown` | The stream failed, or the service failed without a typed error |

`ResourceExhausted` is transient: the same request may succeed later, and `ErrorCode.Retryable` reports it. A client may resend a request that failed with a retryable code and `EffectNone` unchanged, and never resends one that failed with `EffectPossible`. Every other code is final for the request, or reports the caller's own cancellation.

`Errno` is a semantic enum, numbered from 1 in this order: `PermissionDenied`, `OperationNotPermitted`, `NotFound`, `Exists`, `NotDirectory`, `IsDirectory`, `DirectoryNotEmpty`, `InvalidArgument`, `BadDescriptor`, `TooManyOpenFiles`, `NoSpace`, `QuotaExceeded`, `ReadOnlyFilesystem`, `CrossDevice`, `NameTooLong`, `SymlinkLoop`, `FileTooLarge`, `Overflow`, `Busy`, `Again`, `Interrupted`, `IO`, `NoDevice`, `NoSuchDeviceOrAddress`, `BrokenPipe`, `NotSupported`, `NoLocks`, `Deadlock`. Each service converts its native errors; an unknown native error is `IO`, and success is never fabricated.

### Effects and cancellation

Every failure carries `EffectNone`, when the request certainly changed nothing, or `EffectPossible`. Transport loss after a request was written is `EffectPossible` unless the server later establishes the result.

`Describe`, `GetAttr`, `Access`, `Read`, `Readlink`, `StatFS`, `GetLock` and `ReadDir` without `WithAttrs` leave no state behind, so abandoning one is `EffectNone`. Every other request may create, change or acquire something.

An ambiguous mutation is never replayed automatically. This includes `Create`, a truncating `Open`, `SetAttr`, `Write` and a lock acquisition.

`CancelRequest` asks the server to stop an outstanding request. Its acknowledgement is not the target's result and never proves that a mutation did not happen; the target's own response still follows. A cancelled acquisition is settled with `Release` or `ReleaseDir` of its ID, as [Handles](#handles) describes.

### Uncached profile

`Uncached` means zero entry, attribute and negative TTLs, direct file I/O, no writeback cache and no directory listing retained by the client. It does not prohibit the server kernel from buffering data before `Fsync`.

### Not in phase 1

Phase 1 has no operations and no negotiated support for mmap, extended attributes, allocation or hole punching, copy-range, creating or opening special files, and change notifications. The client returns the matching unsupported outcome for these. Attributes still describe an existing special file.

### Limits

| Limit | Value |
| --- | --- |
| Frame payload | 1 MiB |
| `Read` size, `Write` data | 64 KiB |
| Entry name | 1024 bytes |
| Symlink target | 4096 bytes |
| `Walk` names | 1024 |
| `ReadDir` limit | 256 KiB |
| `Forget` entries | 4096 |
| Declared exports | 64 |
| Failure message | 1024 bytes |
| Requests in flight per stream | 256 |

Capabilities may declare smaller limits.

## Verification

`go test ./internal/sandboxfs` covers the golden frames, a round trip of every message, decode rejection, the retryable codes, admission while responses go unread, the succession fence for `Attach` then `Detach` and `Open` then `Release`, and a `Release` that follows a cancelled, still running `Open`. `go test -run '^$' -fuzz FuzzDecode ./internal/sandboxfs` fuzzes the decoder. `go test ./apps/sandboxio/...` runs the Linux service over an in-memory stream against a temporary export, including exclusive create, per-write and atomic append, a lost `Open` reply released on the next stream, duplicate handle IDs, rename modes, directory paging, the root-escape attempts, opaque symlinks and proc magic links, bind-mount aliases, flock against native `flock`, export grants, an incarnation change and transport loss. The bind-mount test reruns itself under `unshare -Urm` and is skipped where unprivileged user namespaces are unavailable.
