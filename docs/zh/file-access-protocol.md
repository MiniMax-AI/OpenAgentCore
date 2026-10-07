---
title: "文件访问协议"
source: docs/file-access-protocol.md
source_hash: 848d891def538f4a4dc78448089348f470c45a7e55ab07eb96f07a9f727e6fb4
---

文件访问协议定义 Runtime 如何读取和修改沙箱中的文件。沙箱内的 Sandbox I/O 服务提供该协议，Runtime 是其客户端。它是一个 node 与 handle 协议，形态仿照 FUSE 低层操作：lookup 获取 node 引用，open 在客户端选择的 ID 下创建 handle，读写携带偏移量，目录读取从 cookie 处继续，锁与沙箱自身的进程协同生效。第 1 阶段仅提供 [Uncached](#uncached-profile) profile，没有变更 stream。

[`internal/sandboxfs/protocol.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxfs/protocol.go) 是权威定义：消息 tag、payload 布局、验证器和 `Service` 接口。同一个包包含通用客户端和 server。[`apps/sandboxio/internal/fileservice`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/sandboxio/internal/fileservice) 是 Linux 服务。frame 使用共享的[分帧](./sandbox-link-protocol.md#framing)，Link 层为每个 stream 提供经过认证的 attachment。

## Stream 与 attachment {#streams-and-attachments}

- 一个 stream 属于一个 attachment；Link 认证该 attachment，并以 `sandboxfs.Attachment` 交给 server：其 ID、stream 绑定的 `ServerInstanceID`、其 lease 以及授予它的 export。请求不指定 attachment、OS 用户或凭据。
- Request ID 遵循[分帧](./sandbox-link-protocol.md#framing)规则，响应携带其请求的 RequestID。同一 stream 上的请求并发运行，因此响应可能以任意顺序到达。协议没有事件。
- 一个 attachment 挂接到一个 export。其 node 引用、handle 和锁保留在服务中，直到它 detach、lease 结束或服务重启。同一 incarnation 中同一 attachment 的新 stream 在通过[接替 fence](#stream-succession) 后继续使用它们。
- 服务 incarnation 由 `ServerInstanceID` 命名。在绑定到其他 incarnation 的 stream 上发出的请求以 `InstanceChanged` 失败，不会自动重新打开任何内容。

## 实现客户端 {#implement-a-client}

Go 客户端为 `sandboxfs.NewClient(stream)`。它为每个操作提供一个方法，可安全并发使用，并对每次失败返回 `*sandboxfs.Failure`。其方法与 go-fuse node 操作一一对应，因此 FUSE 前端将每个内核请求转换为一次调用。

1. 调用 `Describe`。保存 `ServerInstanceID`，并在发送每个请求前对照 `Capabilities` 检查；服务拒绝 capabilities 未声明的任何内容。
2. `Attach` 一个 export，并保存根 `NodeRef`。
3. `Lookup`、`Walk`、`Create`、`Mkdir`、`Symlink`、`Link` 以及带 `WithAttrs` 的 `ReadDir` 各自对返回的每个 node 获取一个引用。内核 forget 引用时，用 `Forget` 释放它们。
4. `Walk` 在 symlink 之后停止。客户端自行相对于视图解析链接：使用 `Readlink` 和后续 walk。
5. `Open`、`Create` 和 `OpenDir` 在客户端选择的 [handle ID](#handles) 下打开 handle。每个 attachment 在其所有 stream 间共用一个 `sandboxfs.HandleIDs`，并从中获取每个 ID。handle 的每个描述符关闭时发送 `Flush`，最后一个描述符关闭时发送 `Release` 或 `ReleaseDir`。
6. 描述符处于 append 模式时，在每次 `Write` 上设置 `Append`。Append 是写入的属性，不是 handle 的属性。
7. 读取每次失败的 `Effect`。出现 `EffectPossible` 后，请求可能已经生效：绝不自动重放 mutation。报告失败，或先用 `GetAttr` 或 `Lookup` 检查状态。带[可重试](#failures) code 和 `EffectNone` 的失败可以原样重发。
8. 回复丢失或调用被取消而导致结果未知的获取操作，用对其 ID 的 `Release` 或 `ReleaseDir` 清理。stream 失败后，在同一 attachment 的新 stream 上恢复（服务仅在失败 stream 的请求结束后才为其提供服务），并在那里清理：不确定的 `Attach` 用 `Detach`，每个不确定的获取操作用 `Release` 或 `ReleaseDir`。[Handle](#handles) 说明清理能证明什么。

取消调用的 context 会立即返回。请求写出前被取消的调用以 `Cancelled` 或 `DeadlineExceeded` 和 `EffectNone` 失败。请求写出后，客户端为其发送 `CancelRequest` 并丢弃迟到的响应，调用以 `Cancelled` 或 `DeadlineExceeded` 失败：[无副作用请求](#effects-and-cancellation)为 `EffectNone`，其他请求均为 `EffectPossible`。在请求写出过程中取消调用会使 stream 失败，因为部分 frame 无法撤回：调用立即返回，不等待 transport 完成写入或关闭 stream，并携带 transport 失败、`Unknown` 和 `EffectPossible`，可匹配 `sandboxfs.ErrTransport`。与写入完成竞争的取消仍可能使 stream 失败，此时进行中的请求以同样方式失败。

要了解被取消的请求做了什么，应改用中断：context 来自 `sandboxfs.WithInterrupt` 的调用在 interrupt channel 关闭时发送 `CancelRequest`，并继续等待请求自身的响应，因此返回请求的结果，或服务的失败及其 effect。FUSE 前端将此用于等待中的锁，因为该锁可能恰好在取消到达前被获取。

stream 失败时，每个进行中的请求以 `Unknown` 和 `EffectPossible` 失败，之后的调用以 `EffectNone` 失败；`errors.Is(err, sandboxfs.ErrTransport)` 对两者都匹配。`Client.Done` 关闭，`Client.Err` 返回原因。要继续，按[沙箱 Link 协议](./sandbox-link-protocol.md)所述，为同一 attachment 打开设置了 `ExpectedServerInstanceID` 的新 stream。此时 `InstanceChanged` 表示该 attachment 的所有 node、handle 和锁都已消失。

## 实现服务 {#implement-a-service}

实现 `sandboxfs.Service`，创建一个 `sandboxfs.NewServer(service)`，并用 `server.Serve(ctx, stream, attachment, seq)` 服务每个 stream，其中 `seq` 是该 stream 的 [Link 绑定序号](./sandbox-link-protocol.md#implement-a-serve-peer)。一个 `Server` 服务该服务的所有 stream，因为[接替 fence](#stream-succession) 跨越这些 stream。`Serve`：

- 拒绝缺少 ID、`ServerInstanceID`、lease 或至少一个 ID 唯一的有效 export 授权的 attachment；如果 attachment 的 lease 在 stream 准入前已结束，则返回 `sandboxfs.ErrLeaseEnded`，不分派任何请求；
- 对每个 `(ServerInstanceID, AttachmentID)` 每次只准入一个 stream，并遵循绑定顺序：后继 stream 立即读取其请求，但在每个已移交过请求的更早 stream 排空之前，不向服务移交任何请求，也没有截止时间。被取代的 `Serve` 返回 `sandboxfs.ErrSuperseded`，若尚未移交任何请求则立即返回；绑定早于某个已准入 stream 的 stream，其 `Serve` 同样返回该错误，不分派其任何请求；
- 解码并验证每个请求，对格式错误的 payload 回复 `InvalidArgument` 和 `EffectNone`；
- 遇到分帧违规时结束 stream：未知 tag、不是请求的 frame，或不递增的 RequestID；
- 最多持有 `sandboxfs.MaxInFlight`（256）个请求，每个从准入起持有到其响应写出，超出的请求回复 `ResourceExhausted` 和 `EffectNone`，因此客户端读取响应期间 `CancelRequest` 仍能送达；
- 自行处理 `CancelRequest`，取消目标请求的 context；
- 拒绝 handle ID 仍被该 stream 上另一获取操作使用的 `Open`、`Create` 或 `OpenDir`，回复 `InvalidArgument` 和 `EffectNone`；对某 ID 的 `Release` 或 `ReleaseDir` 仅在该 ID 正在运行的获取操作结束后才运行。结合接替 fence，服务绝不会同时运行同一 ID 的两个获取操作，也不会在某 ID 的获取操作进行时并发运行其释放；
- 将方法返回的 `*Failure` 作为类型化失败返回，并把其他任何错误或未通过验证的响应报告为 `Unknown` 加 `EffectPossible`；
- stream 结束时取消每个请求的 context。

服务必须：

- 每当丢失 node 表和 handle 表时生成新的 `ServerInstanceID`，并对 `Attachment.ServerInstanceID` 不是自身的请求回复 `InstanceChanged`；
- attachment 未挂接或其 lease 结束后回复 `StaleAttachment`，对 attachment 未持有的引用或 handle 回复 `StaleNode` 或 `StaleHandle`。node ID 仅在使用新 generation 时复用；
- 按 [Handle](#handles) 所述，在产生任何文件系统作用之前原子地预留 `Open`、`Create` 或 `OpenDir` 的 handle ID，并在方法返回前发布请求创建的所有状态；
- 运行请求前调用 `Capabilities.Admit(request, readOnly)`，并返回其报告的失败。依赖服务状态的限制（如 `MaxOpenHandles`）由服务负责；
- 只声明自己强制执行的内容，并且只在锁与沙箱内原生进程互通时声明锁；
- 以自身的进程身份运行，绝不使用请求提供的身份，并精确应用请求的权限位；
- 将每个名称解析为目录 node 的一个条目，绝不遍历 symlink；
- 一旦 mutation 的任何部分可能已经应用，就报告 `EffectPossible`。

### Linux 服务 {#the-linux-service}

`fileservice.New(root)` 将绝对目录 `root` 作为其唯一的 export，即 [world export](#attach) 提供，`Describe` 仅向被授予它的 attachment 列出它。`oac-sandbox-io` 传入 `/`；其下一切内容的隔离由 Provider 的沙箱设置负责，如[沙箱引导](./sandbox-bootstrap.md#responsibilities-and-readiness)所述，服务不在 export 内部强制任何边界。`New` 将进程 umask 设为零，并将其有效 UID 和 GID 报告为 `Identity`。`InstanceID` 返回交给 Link 的 `ServerInstanceID`，`Close` 释放所有 attachment。

- 每个 node 持有一个 `O_PATH|O_NOFOLLOW` 描述符。在一个 attachment 中，一个 node 对应一个 mount ID、设备和 inode，因此硬链接共享一个 node，而 bind mount 与其源保持为两个 node。mount ID 来自带 `STATX_MNT_ID` 的 `statx`，在早于 5.8 的内核上来自 `/proc/self/fdinfo/<fd>` 的 `mnt_id` 行。
- lookup 在父 node 的描述符上用 `openat` 和 `O_NOFOLLOW` 打开一个路径组件。symlink（包括 `/proc/<pid>/cwd` 这类 proc magic link）是独立的 node，绝不被遍历：`Lookup` 和 `Readlink` 返回链接本身，对其执行目录操作以 `Errno` `NotDirectory` 失败，`Open` 以 `SymlinkLoop` 失败。
- 对 node 的操作，如打开、截断、修改 mode 或时间以及创建链接，都通过服务为其持有的描述符的 `/proc/self/fd` 名称进行。该名称解析到描述符自身的对象，绝不解析到 symlink 的目标，通过它打开时会先检查对象的文件类型。
- 打开的 handle 持有自己的描述符，因此文件被 unlink 或重命名后仍能继续工作。文件打开时不带 `O_APPEND`。一个 handle 的写入逐个运行，每次写入先按其 `Append` 设置或清除描述符的 `O_APPEND`；这样一次追加就是一次 `write` 调用。服务不使用带 `RWF_APPEND` 的 `pwritev2`，因为某些内核上的 overlayfs 会丢弃该标志，改为在偏移处写入。短追加按原样报告，绝不由另一次追加续写。
- `Rename` 使用 `renameat2`。仅当启动时的探测成功，服务才声明 `RenameNoReplace` 和 `RenameExchange`。
- `LockFlock` 锁定 handle 的描述符，因此与原生 `flock` 互通。`POSIXLocks` 为 false，`GetLock` 和 `LockPOSIX` 返回 `Unsupported`。
- `ReadDir` cookie 是内核的目录偏移。`Attr.Ino` 按 go-fuse loopback 的方式组合设备号和 inode 号。
- 它声明 `MaxNameBytes` 255、`MaxPathBytes` 4095、`MaxReadBytes` 和 `MaxWriteBytes` 64 KiB、`MaxWalkComponents` 256、`MaxReadDirBytes` 64 KiB 和 `MaxOpenHandles` 4096，以及除 `ReadOnly` 和 `POSIXLocks` 外的所有标志，rename 模式按探测结果声明。

## 参考 {#reference}

### 消息 {#messages}

请求使用 tag 1 到 30；tag `t` 的响应使用 `t | 0x8000`。

| Tag | 请求 | 字段 | 响应 | 含义 |
| --- | --- | --- | --- | --- |
| 1 | `Describe` | – | `ServerInstanceID`, `Identity`, `Capabilities`, `Exports` | incarnation、身份、capabilities 和已授予的 export |
| 2 | `Attach` | `Export`, `ReadOnly` | `Root`（`Entry`） | 挂接到已授予的 export。根为目录 |
| 3 | `Detach` | – | – | 释放 attachment 的所有 node、handle 和锁 |
| 4 | `Lookup` | `Parent`, `Name` | `Entry` | 查找一个条目 |
| 5 | `Walk` | `Parent`, `Names` | `Entries`，可选 `Failure` | 依次查找各组件。在 symlink 之后停止，或在 `Failure` 报告的失败处停止；第一个名称失败时整个请求失败 |
| 6 | `GetAttr` | `Target` | `Attr` | node 或打开的 handle 的属性 |
| 7 | `SetAttr` | `Target`, `Set`, 选定的值 | `Attr` | 修改选定的属性 |
| 8 | `Access` | `Node`, `Mask` | – | 以服务的身份检查权限 |
| 9 | `Open` | `Handle`, `Node`, `Access`, `Flags` | – | 将常规文件打开为 `Handle` |
| 10 | `Create` | `Handle`, `Parent`, `Name`, `Mode`, `Access`, `Flags`, `Exclusive` | `Entry` | 创建常规文件并打开为 `Handle` |
| 11 | `Read` | `Handle`, `Offset`, `Size` | `Data` | 返回字节少于请求表示到达文件末尾 |
| 12 | `Write` | `Handle`, `Offset`, `Append`, `Data` | `Written`，可选 `Failure` | 在 `Offset` 处写入，设置 `Append` 时写入文件末尾 |
| 13 | `Flush` | `Handle`, `Owner` | – | handle 的一个描述符已关闭 |
| 14 | `Fsync` | `Handle`, `DataOnly` | – | 使文件数据持久化，未设置 `DataOnly` 时其元数据也持久化 |
| 15 | `Release` | `Handle` | – | 关闭文件 handle |
| 16 | `OpenDir` | `Handle`, `Node` | – | 将目录打开为 `Handle` |
| 17 | `ReadDir` | `Handle`, `Cookie`, `Limit`, `WithAttrs` | `Entries`, `End` | 读取 `Cookie` 之后的条目 |
| 18 | `ReleaseDir` | `Handle` | – | 关闭目录 handle |
| 19 | `Mkdir` | `Parent`, `Name`, `Mode` | `Entry` | 创建目录 |
| 20 | `Unlink` | `Parent`, `Name` | – | 移除非目录条目 |
| 21 | `Rmdir` | `Parent`, `Name` | – | 移除空目录 |
| 22 | `Rename` | `Parent`, `Name`, `NewParent`, `NewName`, `Mode` | – | 以一种[模式](#rename)重命名 |
| 23 | `Link` | `Node`, `NewParent`, `NewName` | `Entry` | 创建硬链接 |
| 24 | `Symlink` | `Parent`, `Name`, `Target` | `Entry` | 创建 symlink |
| 25 | `Readlink` | `Node` | `Target` | 读取 symlink |
| 26 | `StatFS` | `Node` | 文件系统统计 | 包含该 node 的文件系统的统计 |
| 27 | `Forget` | `Entries` | – | 释放 lookup 引用，全部释放或全不释放 |
| 28 | `GetLock` | `Handle`, `Owner`, `Lock` | 可选 `Conflict` | 会产生冲突的 POSIX 锁 |
| 29 | `SetLock` | `Handle`, `Kind`, `Owner`, `Lock`, `Wait` | – | 获取、转换或释放锁 |
| 30 | `CancelRequest` | `Target`（一个 RequestID） | – | 请求取消一个未完成的请求 |

响应 payload 以一个 uint16 结果开头：1 表示成功，后跟响应的字段；2 表示失败，后跟一个 [`Failure`](#failures)。payload 按以下顺序列出字段：

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

[`testdata`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/internal/sandboxfs/testdata) 保存带注释的 golden frame，涵盖 `Describe`、`Walk`、`Create`、一次追加 `Write`、一次短 `Write`、带 cookie 的 `ReadDir`、`Rename` 和一次失败。

### 共享类型 {#shared-types}

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

- `NodeRef` 和 `HandleID` 的作用域是 attachment 和服务 incarnation。node ID 仅在其对象被 forget 之后才复用，且使用另一个 generation，因此过期的 `NodeRef` 绝不会指向另一个对象。
- `Attr.Mode` 以 Linux `st_mode` 布局恰好包含一种文件类型和权限位（`0o7777`）。`Size` 最大为 2^63−1，`Blocks` 以 512 字节块计数。`Ino` 在 export 内标识文件。
- `DirEntry` 的 `Type` 是同一布局中的一种文件类型。存在 `Entry` 时，其 `Attr.Ino` 和文件类型与该条目的相同。
- `Blocks`、`Files` 及其他 `StatFSResponse` 字段遵循 `statfs`：`BlocksAvailable` 是非特权进程可用的量。

### Describe 与 capabilities {#describe-and-capabilities}

`DescribeResponse` 携带 incarnation、身份、capabilities，以及授予该 attachment 的已声明 export：0 到 64 个唯一 `ExportID`，语法与 Link 的 [export 授权](./sandbox-link-protocol.md#opening-a-stream)相同。`Identity` 是服务运行时的有效 UID 和 GID，即其所创建文件的所有者；它仅供参考，没有请求携带或选择身份。

`Capabilities` 按以下顺序编码每个字段：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `PathProfile` | enum | `LinuxBytes`（1）：名称和 symlink 目标是 Linux 字节串 |
| `CacheProfile` | enum | `Uncached`（1），即 [Uncached profile](#uncached-profile) |
| `Durability` | enum | `FsyncRequired`（1）：仅在 `Fsync` 成功后数据才持久 |
| `MaxNameBytes` | u32 | 最长条目名，1 到 1024 |
| `MaxPathBytes` | u32 | 最长 symlink 目标，1 到 4096 |
| `MaxReadBytes`, `MaxWriteBytes` | u32 | 最大 `Read` 大小和 `Write` 数据，1 到 64 KiB |
| `MaxWalkComponents` | u32 | `Walk` 名称数上限，1 到 1024 |
| `MaxReadDirBytes` | u32 | 最大 `ReadDir` limit，1 到 256 KiB |
| `MaxOpenHandles` | u32 | 每个 attachment 的打开 handle 数上限，至少为 1 |
| `ReadOnly` | bool | 服务仅接受只读 attachment |
| `AtomicAppend` | bool | `Write` 支持 `Append`，且来自多个 handle 的追加在单次写入内绝不交错 |
| `AtomicRename` | bool | `RenameReplace` 原子替换目标 |
| `RenameNoReplace`, `RenameExchange` | bool | 支持该 rename 模式 |
| `HardLinks`, `Symlinks` | bool | 支持 `Link` 和 `Symlink` |
| `SetMode`, `SetOwner`, `SetTimes` | bool | `SetAttr` 可以设置 mode、所有者和时间 |
| `DirectoryFsync` | bool | 对目录 handle 执行 `Fsync` 使其条目持久化 |
| `ReadDirPlus` | bool | `ReadDir` 支持 `WithAttrs` |
| `Flock` | bool | `SetLock` 支持 `LockFlock` |
| `POSIXLocks` | bool | `GetLock` 和 `SetLock` 支持 `LockPOSIX` |

可写服务（即未声明 `ReadOnly` 的服务）声明 `AtomicAppend`、`AtomicRename`、`HardLinks` 和 `Symlinks`。超出声明限制的请求以 `InvalidArgument` 失败，名称或目标超限则以 `Errno` `NameTooLong` 失败；请求未声明的功能以 `Unsupported` 失败。

### Attach {#attach}

`Attach` 按 `ExportID` 选择一个 export；它从不接受 server 路径。该 export 必须是 attachment 的 Link 绑定在 `Attachment.Exports` 中授予的 export（参见[沙箱 Link 协议](./sandbox-link-protocol.md)），只读授权仅允许 `ReadOnly` attach；否则 `Attach` 以 `Unauthorized` 失败。已授予但服务未声明的 export 以 `InvalidArgument` 失败，`Detach` 之前的第二次 `Attach` 也是如此。只读 attachment 上每个修改文件的请求都以 `Errno` `ReadOnlyFilesystem` 失败：`SetAttr`、`Create`、`Write`、`Mkdir`、`Unlink`、`Rmdir`、`Rename`、`Link`、`Symlink`，以及用于写入或带 `OpenTruncate` 的 `Open`。

协议定义了一个 export ID：`world`（`sandboxfs.WorldExport`），即沙箱世界的 export，也就是沙箱中的进程从其 `/` 看到的文件系统；agent host 为其视图的 FUSE world（`worldfs`）attach 该 export。

### 名称与路径 {#names-and-paths}

- 名称和 symlink 目标是字节串，不是 UTF-8 字符串。
- 条目名为 1 到 1024 字节，不含 NUL 或 `/`，且绝不是 `.` 或 `..`。`ReadDir` 绝不返回 `.` 或 `..`。
- symlink 目标为 1 到 4096 字节，不含 NUL。服务原样存储和返回它；由客户端解析。
- 没有请求会解析路径或遍历 symlink。每次 lookup 都指定目录 node 的一个条目，需要目录的请求在其他任何 node 上以 `Errno` `NotDirectory` 失败。

### 属性 {#attributes}

`SetAttr` 作用于 node 或打开的 handle，只修改其 `Set` 掩码选定的属性：

| 位 | 名称 | 值 |
| --- | --- | --- |
| `0x01` | `AttrSize` | `Size`：截断或扩展常规文件 |
| `0x02` | `AttrMode` | `Mode`：仅权限位 |
| `0x04` | `AttrUID` | `UID` |
| `0x08` | `AttrGID` | `GID` |
| `0x10` | `AttrAtime` | `Atime` |
| `0x20` | `AttrMtime` | `Mtime` |
| `0x40` | `AttrAtimeNow` | 无：访问时间设为服务的当前时间 |
| `0x80` | `AttrMtimeNow` | 无：修改时间设为服务的当前时间 |

未知位被拒绝，`AttrAtime` 与 `AttrAtimeNow` 互斥，`AttrMtime` 与 `AttrMtimeNow` 互斥，未选定的值必须为零。选定的 `UID` 或 `GID` 为 4294967295（`chown` 将其视为不修改）时被拒绝。设置 symlink 的 mode 以 `Errno` `NotSupported` 失败；对 symlink 设置的时间作用于链接本身。响应携带修改后的属性。

`Access` 以服务的身份检查 `Mask` 位 `MayExecute`（1）、`MayWrite`（2）和 `MayRead`（4）；掩码为零时检查是否存在。

### 文件 {#files}

- `AccessMode` 为 `AccessRead`（1）、`AccessWrite`（2）或 `AccessReadWrite`（3）。
- `OpenFlags` 为 `OpenTruncate`（1）、`OpenNoFollow`（2）、`OpenSync`（4）和 `OpenDataSync`（8）；未知标志被拒绝。
- `Open` 打开常规文件 node。node 是对象而非路径，因此 `Open` 从不跟随 symlink：目录以 `Errno` `IsDirectory` 失败，symlink 以 `SymlinkLoop` 失败，特殊文件以 `Unsupported` 失败。
- `Create` 以恰好为请求值的权限位创建常规文件；不应用 umask。带 `Exclusive` 时，已存在的条目以 `Errno` `Exists` 失败。不带它时，打开已存在的常规文件，`OpenTruncate` 会将其截断。
- `Read` 接受最大 2^63−1 的偏移。定位 `Write` 必须在 2^63−1 或之前结束，否则以 `InvalidArgument` 失败。
- 带 `Append` 的 `Write` 忽略 `Offset`，将数据原子地写到文件末尾。任何可写的 handle 都接受两种写入，顺序不限，与原生描述符在 `fcntl` 设置或清除 `O_APPEND` 时一致。没有 `AtomicAppend` 的服务以 `Unsupported` 拒绝 `Append`。
- 成功的 `WriteResponse` 携带实际写入的确切字节数。写入在非零前缀后停止时，`Written` 计入该前缀，`Failure` 说明停止原因；未写入任何内容的写入是失败响应。
- `Flush` 既不是 `Fsync` 也不是 `Release`。它报告关闭 handle 的一个描述符时的错误，`Owner` 指定关闭方的锁 owner。
- 对目录 handle 执行文件操作，或对文件 handle 执行目录操作，以 `Errno` `IsDirectory`、`NotDirectory` 或 `BadDescriptor` 失败。

### Handle {#handles}

- 客户端为每个 `Open`、`Create` 和 `OpenDir` 选择 `HandleID`：非零，且从未在该 attachment 的任何 stream 上使用过。`sandboxfs.HandleIDs` 按递增顺序分配 ID。
- 服务在请求产生任何文件系统作用前预留 ID。获取操作运行期间，预留的 ID 计入 `MaxOpenHandles`。ID 已被预留或已打开的获取操作以 `InvalidArgument` 和 `EffectNone` 失败。
- 其他指定预留 ID 的请求以 `StaleHandle` 失败，`Release` 和 `ReleaseDir` 除外：server 仅在该 ID 的获取操作结束后运行它们，因此它们关闭该操作打开的 handle。
- 对某 ID 的 `Release` 或 `ReleaseDir` 可以结算结果未知的获取操作，无论其回复随 stream 丢失，还是其调用被取消。成功或 `StaleHandle` 证明不再有该 ID 的 handle。它不证明获取操作没有改变任何东西：`Create` 可能已创建文件，带截断的 `Open` 可能已截断文件。
- `Create`、`Lookup`、`Walk`、`Mkdir`、`Symlink`、`Link` 或带 `WithAttrs` 的 `ReadDir` 获取的 node 引用，如果其回复未送达客户端，或送达的是客户端已放弃的调用，则无法被 forget，因为客户端永远不知道该 `NodeRef`。它会连同服务为其 node 保留的描述符一直被持有，直到 attachment detach 或其 lease 结束。这类引用的数量没有固定上限：stream 失败会丢失所有尚未送达的回复，包括 server 已写出、不再计入 `MaxInFlight` 的回复，而一次 `Walk` 或 `ReadDir` 会为返回的每个 node 获取一个引用。客户端若只在 detach 之前结束此类调用的 context，其他情况下用 `sandboxfs.WithInterrupt` 停止调用、等待其结果并 forget 返回的内容，就不会给已放弃的调用留下引用。

### Stream 接替 {#stream-succession}

对每个 `(ServerInstanceID, AttachmentID)`，server 每次只准入一个 File stream。在分派后继 stream 的任何请求之前，它停止前任的准入，关闭并取消前任的请求，并等待每个已准入的 handler 和状态发布任务结束。前任的回复被丢弃。这道闸门在 `Attach` 创建任何状态之前就已存在。

接替遵循 Link 的绑定顺序，而不是 stream 到达 server 的顺序：绑定早于某个已准入 stream 的 stream 在分派任何内容之前即被拒绝。server 仅在 attachment 的 lease 已结束且其准入的每个 stream 都已排空后，才忘记该 attachment 的 stream，并以 `sandboxfs.ErrLeaseEnded` 拒绝该 lease 下之后的每个 stream，因此届时拒绝依然成立。后继 stream 在等待期间读取并准入请求，但分派指的是把请求交给服务，在此之前它不分派任何请求。在等待中被取代的后继 stream 没有分派任何内容，因此其 `Serve` 立即结束，下一个后继 stream 改为等待它原本等待的那些 stream。等待没有超时，因为 handler 在被取消后仍可能修改状态。已完成的作用会保留。因此后继 stream 发送的请求能看到其前任请求所做的一切：`Detach` 清理回复丢失的 `Attach`，`Release` 清理丢失的获取操作。

### 目录 {#directories}

- `OpenDir` 打开目录 node，`ReadDir` 读取 `Cookie` 之后的条目；cookie 0 表示开头。每个 `DirEntry.Cookie` 是该条目之后的位置，除此之外不透明。
- `Limit` 限制返回条目编码大小的总和：每个条目 25 字节加名称长度，携带 `Entry` 时再加 104。limit 小到容不下第一个条目时以 `Errno` `InvalidArgument` 失败。
- 带 `WithAttrs` 时，每个返回的条目携带一个 `Entry`，附带一个 lookup 引用。
- 没有剩余条目时 `End` 为 true，没有条目的页始终设置它。同一页内名称和 cookie 都不重复。对于读取过程中发生变化的目录，`ReadDir` 不保证快照。

### Rename {#rename}

`Rename` 恰好接受一种模式：`RenameReplace`（1）替换已存在的目标，`RenameNoReplace`（2）在目标存在时以 `Errno` `Exists` 失败，`RenameExchange`（3）交换两个已存在的条目。

### 锁 {#locks}

- `Lock` 覆盖 `Start` 到 `End`（含两端）；`End` 为 2^63−1 时延伸到文件末尾。
- `SetLock` 接受 `LockPOSIX`（1）或 `LockFlock`（2）、一个 attachment 范围的 `LockOwner`、一个范围、一个模式和 `Wait`。flock 锁覆盖整个文件，因此其范围为 0 到 2^63−1。不带 `Wait` 时，冲突的锁以 `Errno` `Again` 失败；带 `Wait` 时，请求等待直到锁空闲或请求被取消。
- `GetLock` 查询会与读或写 `Lock` 冲突的 POSIX 锁，并在 `Conflict` 中返回。
- 服务仅在其锁与沙箱内原生进程互通时才声明该锁类型。客户端本地的锁表不够。

### 失败 {#failures}

```text
Failure
  Code     enum
  Errno    optional enum    // present exactly when Code is Errno
  Effect   Effect
  Message  bytes            // at most 1024 bytes, for people
```

| Code | 名称 | 返回时机 |
| --- | --- | --- |
| 1 | `InvalidArgument` | payload 未通过验证、请求超出声明的限制、export 未知、attachment 已挂接、获取操作指定了已预留或已打开的 handle ID，或 `Forget` 超出持有的引用数 |
| 2 | `Unsupported` | capabilities 未声明该操作或选项，或请求需要第 1 阶段排除的功能 |
| 3 | `Unauthorized` | `Attach` 指定了 Link 绑定未授予的 export，或对只读授权请求写权限 |
| 4 | `StaleAttachment` | attachment 未挂接、已 detach 或其 lease 已结束 |
| 5 | `InstanceChanged` | stream 绑定到另一个服务 incarnation |
| 6 | `StaleNode` | attachment 未持有该 `NodeRef` |
| 7 | `StaleHandle` | attachment 没有该打开的 handle |
| 8 | `ResourceExhausted` | stream 已持有 `MaxInFlight` 个请求，或 attachment 已有 `MaxOpenHandles` 个 handle（含预留 ID） |
| 9 | `Cancelled` | 请求已被取消 |
| 10 | `DeadlineExceeded` | 调用方的截止时间已过 |
| 11 | `Errno` | 文件系统调用失败；`Errno` 说明原因 |
| 12 | `Unknown` | stream 失败，或服务在没有类型化错误的情况下失败 |

`ResourceExhausted` 是暂时性的：同一请求稍后可能成功，`ErrorCode.Retryable` 会报告这一点。客户端可以原样重发以可重试 code 和 `EffectNone` 失败的请求，绝不重发以 `EffectPossible` 失败的请求。其他 code 对该请求都是最终结果，或报告调用方自身的取消。

`Errno` 是语义枚举，按以下顺序从 1 开始编号：`PermissionDenied`、`OperationNotPermitted`、`NotFound`、`Exists`、`NotDirectory`、`IsDirectory`、`DirectoryNotEmpty`、`InvalidArgument`、`BadDescriptor`、`TooManyOpenFiles`、`NoSpace`、`QuotaExceeded`、`ReadOnlyFilesystem`、`CrossDevice`、`NameTooLong`、`SymlinkLoop`、`FileTooLarge`、`Overflow`、`Busy`、`Again`、`Interrupted`、`IO`、`NoDevice`、`NoSuchDeviceOrAddress`、`BrokenPipe`、`NotSupported`、`NoLocks`、`Deadlock`。每个服务转换其原生错误；未知原生错误为 `IO`，绝不虚构成功。

### Effect 与取消 {#effects-and-cancellation}

每个失败都携带 `EffectNone`（请求确定未改变任何内容）或 `EffectPossible`。请求写出后发生 transport 丢失时为 `EffectPossible`，除非 server 之后确定了结果。

`Describe`、`GetAttr`、`Access`、`Read`、`Readlink`、`StatFS`、`GetLock` 以及不带 `WithAttrs` 的 `ReadDir` 不留下任何状态，因此放弃它们为 `EffectNone`。其他每个请求都可能创建、修改或获取某些东西。

有歧义的 mutation 绝不自动重放。这包括 `Create`、带截断的 `Open`、`SetAttr`、`Write` 和锁获取。

`CancelRequest` 请求 server 停止一个未完成的请求。它的确认不是目标请求的结果，也绝不证明 mutation 未发生；目标请求自身的响应仍会随后到达。被取消的获取操作按 [Handle](#handles) 所述，用对其 ID 的 `Release` 或 `ReleaseDir` 结算。

### Uncached profile {#uncached-profile}

`Uncached` 表示条目、属性和否定缓存的 TTL 均为零，文件 I/O 直通，没有 writeback 缓存，客户端不保留目录列表。它不禁止 server 内核在 `Fsync` 之前缓冲数据。

### 第 1 阶段不包含的内容 {#not-in-phase-1}

第 1 阶段没有以下功能的操作，也不协商对它们的支持：mmap、扩展属性、空间分配或打洞、copy-range、创建或打开特殊文件，以及变更通知。客户端对这些功能返回相应的不支持结果。属性仍可描述已存在的特殊文件。

### 限制 {#limits}

| 限制 | 值 |
| --- | --- |
| frame payload | 1 MiB |
| `Read` 大小、`Write` 数据 | 64 KiB |
| 条目名 | 1024 字节 |
| symlink 目标 | 4096 字节 |
| `Walk` 名称 | 1024 |
| `ReadDir` limit | 256 KiB |
| `Forget` 条目 | 4096 |
| 声明的 export | 64 |
| 失败消息 | 1024 字节 |
| 每个 stream 的进行中请求 | 256 |

capabilities 可以声明更小的限制。

## 验证 {#verification}

`go test ./internal/sandboxfs` 覆盖 golden frame、每种消息的往返、解码拒绝、可重试 code、响应未被读取时的准入、`Attach` 后 `Detach` 以及 `Open` 后 `Release` 的接替 fence、被取代时立即结束的等待中后继 stream，以及跟在已取消但仍在运行的 `Open` 之后的 `Release`。`go test -run '^$' -fuzz FuzzDecode ./internal/sandboxfs` 对解码器做 fuzz 测试。`go test ./apps/sandboxio/...` 通过内存 stream 针对临时 export 运行 Linux 服务，覆盖独占创建、按写入设置的追加与原子追加、在下一个 stream 上释放的丢失 `Open` 回复、重复 handle ID、rename 模式、目录分页、逃逸根目录的尝试、不透明 symlink 和 proc magic link、bind mount 别名、flock 与原生 `flock` 的互斥、export 授权、incarnation 变更和 transport 丢失。bind mount 测试会在 `unshare -Urm` 下重新运行自身，在无特权 user namespace 不可用的环境中跳过。
