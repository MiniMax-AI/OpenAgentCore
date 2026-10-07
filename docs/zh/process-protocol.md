---
title: "进程协议"
source: docs/process-protocol.md
source_hash: bf8d41ab0290aebae678c07371ea4ebae2084d0f2ea45da3a338c7929ec2c9a3
---

进程协议定义 agent host 如何在沙箱中启动和控制进程。沙箱内的 Sandbox I/O 服务提供该协议，agent host 的 broker 是其客户端。协议依据明确的 spec 启动进程，以有序事件流式传输其输出，在精确 offset 处接受 stdin，并将 leader 退出、输出结束和进程 scope 结束作为独立事实报告。

[`internal/sandboxprocess/protocol.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxprocess/protocol.go) 是唯一的权威定义：消息 tag、payload 布局、验证器和 `Service` 接口。同一个包包含通用客户端和服务端。[`apps/sandboxio/internal/processservice`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/sandboxio/internal/processservice) 是 Linux 服务。Frame 使用共享的[分帧](./sandbox-link-protocol.md#framing)，Link 层为每个 stream 提供经过认证的 attachment。

## Stream 与 operation {#streams-and-operations}

- 一个 stream 属于一个 attachment。请求从不指定 attachment、OS 用户或凭据；stream 的 attachment 限定其使用的每个 operation ID 的作用域。
- 请求的 RequestID 遵循[请求 ID 规则](./sandbox-link-protocol.md#framing)，响应携带相同的 RequestID。同一 stream 上的请求并发运行，因此响应可能以任意顺序到达。服务可以用 `Busy` 和 `EffectNone` 拒绝超出其并发限制的请求。
- 一个 operation 就是一次启动，由客户端选择的 `OperationID` 命名。其记录存在于一个服务 incarnation 中，该 incarnation 由 `ServerInstanceID` 命名。
- 事件是 RequestID 为 0 的 frame。每个事件携带其 `OperationID` 和 `Sequence`；`Sequence` 从 1 开始，该 operation 每产生一个事件加一。
- 一个 operation 只有一个 observer：启动它的 stream，或最近一次 `Attach` 的 stream。后来的 `Attach` 会转移 observer。使某个 stream 订阅的 `Start` 或 `Attach`，其响应先于该订阅涵盖的任何事件到达。

## 实现客户端 {#implement-a-client}

Go 客户端为 `sandboxprocess.NewClient(stream)`。`Start` 和 `Attach` 返回一个 `Operation` handle，其 `Events()` channel 按 sequence 顺序将该 operation 的每个事件交付一次；`Start` 发现已有 operation 时，客户端从 sequence 0 开始 attach。该 handle 提供 `WriteStdin`、`CloseStdin`、`CloseOutput`、`Resize`、`Signal`、`Cancel`、`Ack`、`Inspect` 和 `Release`。

1. 调用 `Describe`。保存 `ServerInstanceID`，并在发送每个 spec 和信号前对照 `Capabilities` 检查。Go 客户端保存这些能力，并将 stdin 写入拆分为 `MaxDataBytes` 大小的 chunk。
2. 每次启动选择新的 `OperationID`。`Start` 以 `EffectPossible` 失败后，使用相同 ID 和 spec 重复 `Start`：`Existing` 表示启动已经发生。启动结果不确定的工作绝不选择新 ID。
3. 处理事件，然后 `Ack` 最后处理的 sequence，使服务可以释放其重放缓冲区。stream 丢失后，在新 stream 上用最后处理的 sequence 执行 `Attach`。
4. 在服务已接受的 offset 处写入 stdin。stdin 写入以 `EffectPossible` 失败后，先用 `Inspect` 获取已接受的 offset，再决定重发什么。客户端从不自行重试 stdin 写入或信号。
5. 收到 `InstanceChanged` 表示服务已重启。旧 incarnation 的 operation 状态未知，也不能复用其 ID 来查明。

## 实现服务 {#implement-a-service}

实现 `sandboxprocess.Service`，并用 `sandboxprocess.Serve(ctx, stream, attachment, service)` 服务每个 stream。`Serve` 解码并验证请求，对格式错误的 payload 返回 `InvalidArgument`，遇到分帧违规时结束 stream，并将方法返回的 `*Failure` 作为类型化失败返回。它对每个 stream 最多同时运行 64 个请求，超出的请求不运行，直接以 `Busy` 应答；它从不停止读取，当另有 64 个拒绝等待写出时结束 stream。方法通过 `Conn.Send` 发送事件，对端未读取时该调用阻塞。

服务必须：

- 每当丢失 operation 记录时生成新的 `ServerInstanceID`，并对指向其他 incarnation 的请求返回 `InstanceChanged`；
- 仅声明自己强制执行的内容，并以 `Unsupported` 拒绝 spec 或信号请求中的其他任何内容；
- 在 attachment 打开期间保留其每条 operation 记录，见[去重与 tombstone](#deduplication-and-tombstones)；
- 绝不丢弃未被告知已交付的事件，见[输出、重放与流量控制](#output-replay-and-flow-control)；
- 将 stream 丢失仅视为 observer 丢失，见[所有权](#ownership)。

Linux 服务在二进制的 `main` 中首先调用 `processservice.Init()`。Go 无法设置子进程的 umask，因此每次启动都会将服务二进制作为 trampoline 重新执行：它从继承的描述符读取启动信息，将所有大于 2 的继承描述符标记为 close-on-exec，应用 umask 和工作目录，然后 exec 目标。`Init` 负责运行该 trampoline，正常启动时立即返回。Linux 服务用 `setsid` 在新会话中启动每个 operation，通过 `/proc` 观测会话，并且只声明 `ScopePOSIXSession`。它要求 `pidfd_open` 和 `pidfd_send_signal`（Linux 5.3 或更高版本）：缺少它们时 `processservice.New` 以 `ErrPidfdUnsupported` 失败。

开始服务前，`main` 将进程设为 child subreaper（`prctl(PR_SET_CHILD_SUBREAPER)`），并在进程整个生命周期内运行 `processservice.Reap(ctx)`。`Reap` 是进程中唯一的 `wait`：它回收每个子进程，将每个 leader 的退出交付给对应 operation，并回收 subreaper 继承的孤儿后代进程。二进制中其他任何代码都不得等待子进程；`Reap` 未运行时，任何 operation 都观测不到退出。二进制停止时，在其 stream 结束后调用 `Shutdown(ctx)`：`Shutdown` 像[所有权清理](#ownership)一样取消每个存活的 operation，并在每个 scope 关闭或 `ctx` 结束后返回。

## 参考 {#reference}

### 请求 {#requests}

除 `Describe` 外，每个请求都以 `ServerInstanceID` 和 `OperationID` 开头。响应以成功或失败判别符开头；失败携带 [`Failure`](#errors)。

| Tag | 请求 | 字段 | 响应 | 含义 |
| --- | --- | --- | --- | --- |
| 1 | `Describe` | – | `ServerInstanceID`, `Capabilities` | incarnation、能力、限制和清理策略 |
| 2 | `Start` | `Spec` | `Disposition`（`Created` 或 `Existing`） | 预留 ID，验证 spec，然后只启动一次。`Created` 使此 stream 订阅该 operation。`Existing` 不改变任何内容；用 `Attach` 观测该 operation |
| 3 | `Attach` | `AfterSequence` | `Status` | 从 `AfterSequence` 之后的事件开始观测。从不启动进程 |
| 4 | `Inspect` | – | `Status` | 当前记录 |
| 5 | `WriteStdin` | `Offset`, `Data` | `Accepted` | 在当前 stdin offset 处写入；返回已接受的字节数 |
| 6 | `CloseStdin` | `Offset` | – | 在 `Offset` 个已接受字节之后关闭管道 stdin。在同一 offset 上幂等。PTY 返回 `Unsupported` |
| 7 | `CloseOutput` | `Stream` | – | 关闭一个输出流的读端。写入方看到原生的管道或 PTY 行为，该输出流以 `Abandoned` 结束 |
| 8 | `ResizePTY` | `Rows`, `Cols`, `XPixels`, `YPixels` | – | 设置终端尺寸；前台作业收到 `SIGWINCH` |
| 9 | `Signal` | `Signal`, `Target` | – | 向声明的目标发送声明的信号 |
| 10 | `Cancel` | `GraceMillis` | – | 向 scope 发送 TERM，宽限期后发送 KILL，宽限期上限为 `CancelGraceLimitMillis`。完成情况以事件形式到达。处于 `Starting` 时保留该取消，进程一启动就应用 |
| 11 | `AckEvents` | `Sequence` | – | 确认直到 `Sequence` 的事件，将其从重放缓冲区释放 |
| 12 | `Release` | – | – | 释放已结算 operation 的资源并保留其 tombstone。未结算的 operation 返回 `Busy` |

`Status` 包含 `State`、`Exit`（状态为 `Exited` 时出现）、`StartFailure`（状态为 `StartFailed` 时出现）、`StdinOffset`、`StdinClosed`、`Output`（处置结果，所有输出流关闭后出现）、`Scope`、`Released`，以及保留的事件范围 `FirstRetained` 至 `LastSequence`；`FirstRetained` 更大时该范围为空。

| 枚举 | 值 |
| --- | --- |
| `OperationState` | `Starting`、`Running`、`Exited`、`StartFailed`、`Unknown`（无法观测到退出） |
| `ScopeState` | `Active`、`Closed`、`Unknown`（无法观测 scope；服务持续尝试，之后仍可能出现 `Closed`） |
| `OutputDisposition` | `Drained`（文件结束）、`Abandoned`（`CloseOutput` 之后）、`Lost`（读取失败） |

operation 在启动失败时，或在其退出已被观测或已丢失、所有输出均已关闭且 scope 为 `Closed` 时，即为已结算。scope 为 `Unknown` 时 operation 未结算，因此在服务确认 scope 关闭之前，`Release` 返回 `Busy`。`Release` 从不把 `Unknown` 状态变为已确认结果。

### 事件 {#events}

| Tag | 事件 | 字段 | 含义 |
| --- | --- | --- | --- |
| `0x4001` | `Started` | – | 启动成功 |
| `0x4002` | `StartFailed` | `Failure` | 启动失败。之后不再有其他事件，记录保留。仅当可以证明目标从未执行时，效果才为 `EffectNone` |
| `0x4003` | `Output` | `Stream`, `Offset`, `Data` | 输出字节。`Stream` 为 `Stdout`、`Stderr` 或 `Terminal` |
| `0x4004` | `StreamClosed` | `Stream`, `Offset`, `Disposition` | 一个输出流在其最终 offset 处结束 |
| `0x4005` | `Exited` | `ExitCode`，或 `ExitSignal` 和 `CoreDumped` | leader 的 wait 结果 |
| `0x4006` | `OutputClosed` | `Disposition` | 所有捕获的输出流都已关闭。处置结果取各输出流中最差者：`Lost` 最差，其次 `Abandoned`，然后 `Drained` |
| `0x4007` | `ScopeClosed` | – | operation 的 scope 已空 |
| `0x4008` | `ObservationLost` | `Observation`（`Exit` 或 `Scope`）、`Failure` | 某项必需观测变得不可用。对应状态变为 `Unknown`，效果为 `EffectPossible`：进程可能仍在运行。丢失的 scope 观测可能恢复，之后会出现 `ScopeClosed` |

顺序：

- `Started` 或 `StartFailed` 最先出现。
- `Exited` 与 `OutputClosed` 相互独立。保持输出流打开的后台进程会使 `OutputClosed` 在 `Exited` 之后仍未出现，输出也可能在 leader 退出前关闭。
- `Exited` 出现在服务回收 leader 时捕获输出流中已缓冲的所有输出字节之后，且从不等待确认。例外：被 `CloseOutput` 放弃或因读取失败而丢失的输出流不再交付输出；因[重放限制](#output-replay-and-flow-control)而服务未能读取的输出在 `Exited` 之后出现；使用 PTY 时，回收时仍在内核发往 master 的异步队列中的输出可能在 `Exited` 之后出现，终端 flush 会像原生行为一样丢弃输出。
- 回收之后，仍持有输出流的进程写入的输出可能在 `Exited` 之后出现，与原生行为一致。
- `OutputClosed` 出现在服务将交付的所有输出字节和每个 `StreamClosed` 之后。
- 不同输出流的输出之间，以及输出与文件系统变化之间，都没有顺序保证。

### 进程 spec {#process-spec}

启动只使用 spec；服务自身的环境变量、目录和 umask 从不传给进程。

| 字段 | 规则 |
| --- | --- |
| `Executable` | 不含 NUL 的字节。不含 `/` 的名称像 `execvpe` 一样在 spec 的 `PATH` 条目中查找，没有 `PATH` 的 spec 不能使用此类名称。含 `/` 的相对路径相对 `Cwd` 解析。文件缺失时启动以 `NotFound` 失败 |
| `Argv` | 完整的 argv，包括 argv[0]，原样传递。至少一项 |
| `Env` | 完整的环境：名称唯一且不含 `=` 或 NUL，值不含 NUL |
| `Cwd` | 绝对路径 |
| `Umask` | 最大 `0o777` |
| `IOMode` | `IOPipes` 或 `IOPTY`。仅连接描述符 0、1 和 2 |
| `PTY` | 当且仅当 `IOPTY` 时出现：尺寸、`Term` 和终端模式 |
| `Scope` | `ScopePOSIXSession` 或 `ScopeCgroupV2` |

启动失败按 OS 错误映射：文件或目录缺失为 `NotFound`，权限错误为 `Unauthorized`，文件不可执行为 `InvalidArgument`，资源限制为 `ResourceExhausted`，其他均为 `IO`。

### 去重与 tombstone {#deduplication-and-tombstones}

- Operation ID 的作用域为 `(AttachmentID, ServerInstanceID, OperationID)`。
- `Start` 在启动前预留 ID，并保存编码后 spec 的 SHA-256 digest。相同 ID 和相同 spec 返回 `Existing`，并发请求也是如此；spec 不同则返回 `OperationConflict`。
- 记录在其 attachment 打开期间保留。`Release` 保留包含 digest、状态和结果的 tombstone；对已释放 ID 的 `Start` 返回 `Released`。
- 打开的 attachment 的记录从不被淘汰。达到 `MaxOperationRecords` 或 `MaxActiveOperations` 时，`Start` 以 `ResourceExhausted` 失败。
- attachment 关闭且其 operation 均已结算后，服务丢弃这些记录；此后没有 stream 能再指向它们。

### Stdin offset {#stdin-offsets}

stdin offset 从 0 开始，计算服务已接受的字节数。`WriteStdin` 和 `CloseStdin` 仅在当前 offset 处成功；其他 offset 返回 `InputOffsetConflict`，因此旧的或重叠的写入不会重复注入字节。`Accepted` 可能小于发送的数据量。进程关闭其 stdin 后，写入返回 `StdinClosed`。

### 输出、重放与流量控制 {#output-replay-and-flow-control}

- 服务保留每个 operation 的事件直到其被确认，并按顺序交付给 observer。
- operation 所有输出流中未确认的 `Output` 数据总量限制为 `MaxReplayBytesPerOperation`。剩余额度不足一个内存页时，服务停止读取进程输出，进程因而在自己的写入上阻塞；一次读取从不拆分 packet 模式管道的一次写入。不丢弃任何数据。
- 慢速 observer 会拖慢其 stream：服务写入事件的速度不超过对端读取的速度。
- `Attach` 可以从保留范围内任意 sequence 之后恢复。请求已确认的事件返回 `ReplayGap`，因此缺失的输出绝不会被静默跳过。已接受的 `Attach` 承诺的事件在发送前一直保留，即使另一个 stream 先确认了它们。

### PTY {#pty}

`PTY` 设置以行、列和像素表示的尺寸、`TERM` 值（环境本身不得设置 `TERM`），以及按 RFC 4254 §8 opcode 与值对表示的终端模式，另加 RFC 8160 的 `IUTF8`（42）。控制字符模式取该字符，255 表示禁用；标志模式取 0 或 1。请求的模式必须出现在 `Capabilities.PTYModes` 中。只有终端语义来自 SSH，传输不是。

使用 PTY 时，输出以 `Terminal` 流到达，stdin 写入即终端输入，且没有 stdin 半关闭：要表示输入结束，写入终端的 EOF 字符，默认为 `^D`。所有进程都关闭终端后，终端输出以 `Drained` 结束。对 `Terminal` 流执行 `CloseOutput` 会关闭终端，从而挂断其会话。

### Scope 与信号 {#scope-and-signals}

两种 scope 都在新的 POSIX 会话中启动进程。`ScopeCgroupV2` 还会将进程放入新的 cgroup，且仅在服务强制执行这一点时才声明。后代进程可以调用 `setsid` 离开 `ScopePOSIXSession` scope；这是该 scope 的局限。Linux 服务通过轮询 `/proc` 中的存活成员来观测会话 scope，因此 `ScopeClosed` 在此局限内尽力而为。

请求从不指定进程 ID。`Signal` 接受 `Capabilities.Signals` 中的信号编号和以下目标之一：

| 目标 | 接收信号的进程 |
| --- | --- |
| `TargetLeader` | 启动的进程 |
| `TargetInitialProcessGroup` | leader 的进程组，随会话创建 |
| `TargetPTYForegroundGroup` | 终端的前台进程组。没有 PTY 时返回 `InvalidArgument` |
| `TargetScope` | scope 中的每个进程：会话成员或 cgroup |

没有进程的目标返回 `NotRunning`；`ScopeClosed` 之后的任何目标，以及终端没有前台进程组的 `TargetPTYForegroundGroup`，同样返回 `NotRunning`。

信号只送达服务能证明属于该 operation 会话的进程，绝不送达复用了 PID 或会话 ID 的进程。leader 尚未被回收时，其 PID 固定住会话 ID 和初始进程组 ID，Linux 服务按 ID 向 leader 或该进程组发送信号。否则，它通过为会话进程持有的 pidfd 发送信号，并且只发给同一请求内刷新证明仍在会话中的进程；刷新失败时，不再发送任何信号，请求以 `IO` 失败。只有当它已持有的某个进程在该次读取期间始终留在会话中时，它才为显示该会话 ID 的进程获取 pidfd；回收任何已持有进程之前，它会先更新持有集合。当它未持有任何进程，但仍有存活进程显示该会话 ID 时，它无法区分该会话与具有相同 ID 的新会话：它不发送任何信号，目标返回 `NotRunning`，scope 随 `ObservationLost` 变为 `Unknown`。

### 所有权 {#ownership}

丢失 stream 只会丢失 observer；operation 继续运行，同一 attachment 的任何 stream 都可以 `Attach` 到它。Link 层报告 attachment 的所有权失效时，服务等待 `OwnerLossGraceMillis`。所有权在宽限期内恢复则不做任何操作。宽限期到期或所有权被撤销时，服务对该 attachment 的每个存活 operation 先发送 TERM，`CancelGraceLimitMillis` 后再发送 KILL；仍在启动中的 operation 一启动就被取消。此后，在所有权恢复之前，该 attachment 上新 operation 的 `Start` 返回 `StaleAttachment`。

### 能力 {#capabilities}

| 字段 | 含义 |
| --- | --- |
| `Platform` | `PlatformLinux`：信号编号和终端语义遵循 Linux |
| `Scopes`, `IOModes`, `Signals`, `SignalTargets`, `PTYModes` | spec 或信号请求可以使用的内容 |
| `MaxStartBytes` | 编码后 `Start` payload 的最大大小 |
| `MaxDataBytes` | 单次 stdin 写入和输出 chunk 的最大大小，不超过 64 KiB |
| `MaxActiveOperations` | 尚未结算的 operation 数 |
| `MaxOperationRecords` | 服务保留的全部记录数，包括 tombstone |
| `MaxReplayBytesPerOperation` | 每个 operation 保留的未确认输出 |
| `OwnerLossGraceMillis` | 所有权失效后 operation 的存活时长 |
| `CancelGraceLimitMillis` | `Cancel` 的最长宽限期，也是所有权清理的宽限期 |

去重、重放、管道半关闭以及分别报告退出与输出完成，是每个服务都实现的协议语义，不是能力。

### 错误 {#errors}

失败携带 `Code`、`Effect` 和消息。`EffectNone` 表示请求没有产生效果；`EffectPossible` 表示可能已产生效果。发送请求后 transport 丢失，客户端报告为带 `EffectPossible` 的 `IO`。调用方 context 在 Go 客户端写入请求前结束时，失败为带 `EffectNone` 的 `Cancelled` 或 `DeadlineExceeded`；在写入期间结束时，客户端关闭 stream，失败带 `EffectPossible`；在写入后结束时，stream 保持打开，失败带 `EffectPossible`。与写入结束竞争的取消仍可能关闭 stream，其上正在处理的请求随后以 `EffectPossible` 失败。

| Code | 含义 |
| --- | --- |
| `InvalidArgument` | 请求违反规则，例如事件 sequence 超出最后一个 |
| `Unsupported` | 请求使用了能力未声明的内容 |
| `Unauthorized` | OS 拒绝了启动 |
| `StaleAttachment` | attachment 已失效，或其 operation 已被清理且所有权尚未恢复 |
| `InstanceChanged` | 请求指向另一个 incarnation |
| `NotFound` | operation 不存在，或可执行文件或目录缺失 |
| `OperationConflict` | 该 ID 已被另一个 spec 使用 |
| `Released` | operation 已释放 |
| `ReplayGap` | 请求的事件已被确认且已不存在 |
| `InputOffsetConflict` | stdin offset 不是当前 offset |
| `StdinClosed` | stdin 已关闭 |
| `OutputClosed` | 输出流或终端已关闭 |
| `NotRunning` | 目标没有进程，或 operation 仍在启动 |
| `Busy` | operation 尚未结算，或 stream 已运行服务允许的最多请求数 |
| `ResourceExhausted` | 声明的限制或 OS 资源已耗尽 |
| `DeadlineExceeded`, `Cancelled` | 调用方截止时间已过或已放弃 |
| `IO` | 服务或 transport 失败 |
| `Unknown` | 其他任何失败 |

## 验证 {#verification}

`go test ./internal/sandboxprocess` 检查 `internal/sandboxprocess/testdata` 中的 golden frame，`go test -fuzz FuzzDecode ./internal/sandboxprocess` 对解码器进行 fuzz 测试。`go test ./apps/sandboxio/internal/processservice` 使用真实进程，在内存 stream 上运行 Linux 服务。
