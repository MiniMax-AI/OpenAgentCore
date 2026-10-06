---
title: "沙箱 Link 协议"
source: docs/sandbox-link-protocol.md
source_hash: d753586c244650329796b8abd17994c2e57aa433b55ebd78c499c86368e8aa6f
---

Link 协议通过 relay 连接沙箱 I/O 的两端。Sandbox I/O 服务运行在沙箱内并为其提供服务，是 serve peer。agent host 上的 Runtime 在沙箱外运行 Harness，并通过该服务使用沙箱，是 attach peer。每个 peer 各自向 relay 认证自己的 link。relay 授权 attach peer 打开的每个服务 stream，将其绑定到该资源当前的 serve peer，然后在两个 stream 之间复制字节而不读取内容。服务帧从不携带凭据或 grant。

权威定义位于 [`internal/sandboxlink/protocol.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxlink/protocol.go)。同一 package 包含 serve peer 与 attach peer 库；relay 核心位于 [`internal/sandboxlink/relay`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxlink/relay/relay.go)。该服务的启动输入是[沙箱引导](./sandbox-bootstrap.md)。本文还负责所有沙箱 I/O 协议共用的[帧布局](#framing)。

## Link 的工作方式 {#how-a-link-works}

link 是基于 TLS、通往 relay 的 WebSocket。每条 WebSocket 消息都是二进制消息，携带同一字节流的后续字节，yamux 在该字节流上多路复用 stream。peer 是 yamux client。它打开的第一个 stream 是 control stream，先携带 Hello，之后携带控制请求和事件。之后的每个 stream 恰好携带一种服务协议：

1. attach peer 打开一个 stream 并发送 `Open`。
2. relay 让其 Authority 授权该 Open，向资源当前的 serve peer 打开一个 stream 并发送 `Bind`。
3. serve peer 回复 `Bound`，relay 向 attach peer 回复 `Opened`。此后两个 stream 携带服务自身的帧，relay 原样复制。

stream 以两种方式之一结束，relay 端到端区分二者。有序结束（`CloseWrite`，即 yamux FIN）在此前写入的全部字节之后，以 EOF 到达对端。中止（`Reset`，或 relay 因 lease 到期、撤销或 link 丢失而关闭 stream）以错误到达对端，绝不是 EOF。

## 实现 serve peer {#implement-a-serve-peer}

Sandbox I/O 服务以 `ServeConfig` 运行 `sandboxlink.Serve`：

- `URL`、`Credential` 和 `Resource` 来自[引导输入](./sandbox-bootstrap.md)。凭据标识该服务，因此 Hello 不携带 peer ID。服务每次在没有其 operation 与 handle registry 的情况下启动时，`ServerInstanceID` 都是新 ID。
- `Services` 为每个提供的服务和版本保存一个 handler。handler 接收 `Bind`（携带已授权的 binding，包括 File stream 的 export）、一个 bind 序号和该 stream。handler 拥有该 stream，用完后返回。attachment 关闭或 `Serve` 返回时，其 context 结束。Bind 顺序是 `Serve` 分配 bind 序号的顺序，分配在跟踪 attachment 的锁下、回复 `Bound` 之前进行；并发的 `Bound` 和 `Opened` 回复可能以其他顺序到达打开方，延迟运行的 handler 保留其 stream 的位置。该序号属于一次 `Serve` 调用，跨重连保持；由于所有 attachment 共享该序号，它严格递增但有间隙，服务可以依靠它对接替进行 fencing。
- link 断开时，`Serve` 以带 jitter 的指数 backoff 重连，并发送同一 `ServerInstanceID`。其 context 结束，或 relay 以不可[重试](#failures)的失败拒绝 Hello 时，它返回，例如凭据被撤回后的 `AuthenticationFailed`，或更新的沙箱接管该资源后的 `StaleGeneration`。返回前，它取消每个 handler 的 context 并等待 handler 结束。
- attachment 仍处于打开状态而其最后一个打开的 stream 结束时（例如 link 断开），触发 `OnAttachmentLost`。某个 stream 重新绑定已丢失的 attachment 时，触发 `OnAttachmentRestored`。relay 报告 `AttachmentClosed` 时，携带原因触发 `OnAttachmentClosed`。丢失 socket 不等于关闭 attachment：服务保留 attachment 的状态，直到它被关闭。
- 关闭是最终的。relay 在关闭前发送的 `Bind` 可能在 `AttachmentClosed` 之后到达，因此对于在最近 `sandboxlink.HandshakeTimeout` 内关闭的 attachment，`Serve` 以 `LeaseExpired` 拒绝其 `Bind`。已关闭 attachment 的 stream 稍后结束时，绝不会将另一个 attachment 标记为丢失。

serve peer 不打开 stream，在 Hello 之后也不在其 control stream 上发送任何内容。relay 会结束违反此规则的 link。relay 只向 serve peer 发送 `AttachmentClosed` 事件；link 上出现请求时，`Serve` 结束该 link。

## 实现 attach peer {#implement-an-attach-peer}

agent host 上的 Runtime 使用其 Runtime ID 和凭据调用 `sandboxlink.DialAttach`，然后：

- `OpenService` 在新 stream 上发送 `Open`，返回该 stream 和 `Opened`。拒绝时返回 `*sandboxlink.Error`，`errors.Is(err, sandboxlink.PermissionDenied)` 可匹配其 code。context 限制的是打开过程，不是 stream。
- `Renew` 在 `LeaseExpiresAt` 到达前，用当前 grant 延长 attachment 的 lease。`CloseAttachment` 结束一个 attachment 及其全部 stream。两者等待轮到自己写入以及等待回复的时间，都不超过其 context 允许的范围。
- 请求发送前 context 就已结束的调用返回带 `EffectNone` 的 `ServiceUnavailable`，并保持 link 连接。请求可能已到达 relay、但因 context 结束或 link 断开而未得到回复的调用，返回带 `EffectPossible` 的 `ServiceUnavailable`（`sandboxlink.Uncertain`）。在写入控制请求期间结束的 context 会结束 link，因为 control stream 不能携带不完整的帧；请求写入后才结束的 context 只停止等待。与写入完成竞争的取消仍可能关闭 link，此时进行中的调用以 `EffectPossible` 失败。
- `OnAttachmentClosed` 报告 relay 因 lease 到期、撤销或更新的资源 generation 而关闭 attachment。

attachment 的生命周期长于其 link。重连后，Runtime 使用相同的 binding 身份打开 stream。要恢复服务持有的状态，Runtime 将 `ExpectedServerInstanceID` 设为此前 `Opened` 中的 `ServerInstanceID`；此时 `InstanceChanged` 表示服务已重启并丢失该状态。attachment 关闭后，Runtime 以新的 `AttachmentID` 打开新的 attachment。

## 运行 relay {#run-a-relay}

`relay.New` 接收 `Authority`，返回 `*relay.Relay`，它是一个 `http.Handler`。relay endpoint 位于安装实例的 HTTPS ingress 之后，由 ingress 终止 TLS，因此 handler 在 ingress 的明文 HTTP 一跳上接受 upgrade；peer 在拨号时强制 TLS。每条 link 最多承载 256 个并发服务 stream。

relay 的 owner 基于其持久记录实现 `Authority`，relay 对每个 Hello、Open 和续期都咨询它。撤销时，先撤回授权，再调用 `RevokeAttachment` 或 `RevokeResource`，让 relay 关闭其持有的对象。

测试使用 `sandboxlinktest.NewAuthority`（持有静态凭据和 grant）和 `sandboxlinktest.StartRelay`（在 `httptest` TLS server 上运行 relay，并返回其 URL 和信任它的 TLS 配置）。

## 分帧 {#framing}

包括 Link 在内，所有沙箱 I/O 协议都以相同方式对消息分帧。[`internal/sandboxwire`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxwire/frame.go) 实现帧和基本类型。每个协议的 `protocol.go` 负责其 tag、payload 布局和验证器。

帧由 16 字节 header 和随后的 payload 组成。整数为大端序。

| 偏移 | 字段 | 类型 | 规则 |
| --- | --- | --- | --- |
| 0 | `PayloadLength` | uint32 | 不超过 1 MiB（`sandboxwire.MaxPayload`）；在读取 payload 前检查 |
| 4 | `MessageType` | uint16 | 协议定义的 tag |
| 6 | `Flags` | uint16 | 零 |
| 8 | `RequestID` | uint64 | 请求中为非零值，且大于发送方在该 stream 上的上一个请求 ID；响应中为对应请求的 ID；事件中为零 |

消息 tag：

- 请求按协议列出的顺序，从 1 起依次使用 tag。
- 请求 tag `t` 的响应使用 `t | 0x8000`。其 payload 以协议定义的 uint16 结果判别值开头。失败携带类型化 code 和 `Effect`。
- 事件按协议列出的顺序，从 `0x4001` 起依次使用 tag。
- 其他 tag 均为格式错误。

payload 由协议列出的字段按序组成，没有填充、名称或保留空间：

| 基本类型 | 编码 |
| --- | --- |
| u8, u16, u32, u64 | 对应宽度的无符号整数 |
| i64 | 二进制补码，8 字节 |
| 布尔值 | 一个字节，`0` 或 `1` |
| 枚举 | uint16；零和未知值无效 |
| ID | 16 个不透明字节；在要求 ID 的位置，零 ID 无效 |
| 字节串 | uint32 长度，后跟字节 |
| 数组 | uint32 元素数量，后跟元素；每个数组都有最大数量 |
| 可选字段 | 一个布尔存在标志字节，仅在存在时后跟值 |
| Effect | 枚举：`EffectNone` = 1，`EffectPossible` = 2 |

解码规则：

- 分配内存前，对照剩余 payload 及其最大值检查每个长度和数量。
- 拒绝未知 tag、非零 flag、未知枚举值、重复条目、溢出和 payload 尾部多余字节。每个此类错误都包装 `sandboxwire.ErrMalformed`。
- 没有 map，也没有隐式默认值。
- 文件或进程数据 chunk 最多 64 KiB（`sandboxwire.MaxChunk`）。
- 每个发送方在一个 stream 上的请求 ID 按 wire 顺序严格递增，因此接收方以常量内存检查唯一性。发送方在写入帧的临界区内从 `sandboxwire.RequestSequence.Next` 取得每个 ID。接收方用 `Admit` 检查每个 ID，对未递增的 ID 按协议违规回复，不分派该请求。服务 stream 依次携带两个序列：先是打开它的 Link 交换（attach 侧为 `Open` 和 `Opened`，serve 侧为 `Bind` 和 `Bound`），然后是服务协议自身的序列，它在该交换之后开始，因此第一个服务请求可以再次使用 ID 1。
- 失败的请求说明其是否可能已生效：`EffectNone` 或 `EffectPossible`。分派后的 transport 丢失为 `EffectPossible`，除非服务端之后确定了结果。

每个协议在其 package 的 `testdata` 下保存带注释的 golden 帧，并提供一个解码器 fuzz target。

## 消息 {#messages}

| Tag | 请求 | 响应 | Stream |
| --- | --- | --- | --- |
| 1 `OpHello` | `ServeHello` 或 `AttachHello` | `HelloAccepted` | control stream |
| 2 `OpOpen` | `Open` | `Opened` | attach peer 发起的服务 stream |
| 3 `OpBind` | `Bind` | `Bound` | relay 向 serve peer 发起的服务 stream |
| 4 `OpRenewAttachment` | `RenewAttachment` | `AttachmentRenewed` | control stream，attach peer |
| 5 `OpCloseAttachment` | `CloseAttachment` | `CloseAccepted` | control stream，attach peer |
| `0x4001` `EventAttachmentClosed` | `AttachmentClosed` 事件 | | control stream，由 relay 发出 |

响应 payload 以 uint16 结果开头：1 表示成功，后跟响应字段；2 表示失败，后跟 `Code`（枚举）和 `Effect`。每个 Link payload 最多 16 KiB（`sandboxlink.MaxMessageBytes`）。

共享字段类型：

- `ResourceRef`：`TenantID` ID、`EnvironmentID` ID、`Kind` 枚举（`ResourceAllocation` = 1，`ResourceEnrollment` = 2）、`ID` ID、`Generation` u64（至少为 1）。`Kind` 仅记录来源；没有服务依据它分支。
- `Service` 枚举：`ServiceFile` = 1，`ServiceProcess` = 2，`ServiceNetwork` = 3。服务版本是非零 u16。
- lease 到期时间是自 Unix epoch 起的毫秒数，类型为 i64，大于零。

```text
Hello
  Version           u16            // 1
  Role              enum           // RoleServe = 1, RoleAttach = 2
  if RoleServe:
    Credential        bytes        // 1..4096 bytes
    Resource          ResourceRef
    ServerInstanceID  ID
    Services          count 1..3 of { Service enum, Version u16 }, no service twice
  if RoleAttach:
    RuntimeID         ID
    Credential        bytes        // 1..4096 bytes

HelloAccepted       (no fields)

Open
  Service                   enum
  Version                   u16
  Resource                  ResourceRef
  ExpectedServerInstanceID  optional ID
  AttachmentID              ID
  SessionID                 ID
  AssignmentID              ID
  AssignmentEpoch           u64    // at least 1
  AttachGrant               bytes  // 1..8192 bytes

Opened
  AttachmentID      ID
  ServerInstanceID  ID
  LeaseExpiresAt    i64 ms

Bind
  AttachmentID              ID
  Service                   enum
  Version                   u16
  SessionID                 ID
  AssignmentID              ID
  AssignmentEpoch           u64
  LeaseExpiresAt            i64 ms
  ExpectedServerInstanceID  ID     // the serve peer's ServerInstanceID as the relay knows it
  Exports                   optional, present exactly when Service is ServiceFile:
                              count 1..64 of ExportGrant, no ID twice
  Egress                    optional, present exactly when Service is ServiceNetwork:
                              count 0..256 of EgressRule

ExportGrant
  ID                bytes          // 1..64 bytes of a-z, 0-9, '_' and '-'
  ReadOnly          bool

EgressRule
  Family            enum           // FamilyIPv4 = 1, FamilyIPv6 = 2
  Address           4 or 16 bytes  // by Family
  PrefixLength      u8             // 0..32 or 0..128
  PortFirst         u16
  PortLast          u16

Bound               (no fields)

RenewAttachment
  AttachmentID      ID
  AttachGrant       bytes          // 1..8192 bytes

AttachmentRenewed
  AttachmentID      ID
  LeaseExpiresAt    i64 ms

CloseAttachment
  AttachmentID      ID

CloseAccepted       (no fields)

AttachmentClosed
  AttachmentID      ID
  Reason            enum           // CloseRequested = 1, CloseLeaseExpired = 2, CloseRevoked = 3, CloseStaleGeneration = 4
```

[`testdata/link_v1.hex`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxlink/testdata/link_v1.hex) 保存这些消息的带注释 golden 帧。

## 握手 {#handshake}

1. peer 拨号 relay 的 URL：使用 `wss://`；仅当主机为 `localhost` 或 loopback 地址时可用 `ws://`。URL 不含 user、查询或片段，也绝不含凭据。`sandboxlink.CheckRelayURL` 对两个 peer 和[引导输入](./sandbox-bootstrap.md)应用此规则，并以 `sandboxlink.ErrRelayURL` 拒绝其他 URL。使用 `wss://` 时，由 TLS 认证 relay。
2. peer 启动 yamux 并打开 control stream。
3. 它以请求 ID 1 发送 Hello，作为 control stream 的第一个请求：Sandbox I/O 服务发送 `ServeHello`，Runtime 发送 `AttachHello`。凭据只在 Hello 中传输。
4. relay 通过其 Authority 认证 peer，并回复 `HelloAccepted`，或回复失败，随后结束 link。对其他版本的 Hello，relay 不读取版本之后的内容，直接回复 `VersionMismatch`。如果 Authority 裁决 serve Hello 期间发生撤销，relay 会再次询问，因此已撤回的凭据绝不会建立 serve peer。

后续控制请求延续 Hello 的请求 ID。attach link 的请求 ID 未递增时，relay 以 `ProtocolViolation` 结束该 link。`Open` 和 `Bind` 各自是其 stream 上唯一的请求，使用请求 ID 1。

对于 serve peer，Authority 返回 peer 的身份及该凭据所服务的资源（包括 generation）。该资源必须与 Hello 中的一致，否则回复 `PermissionDenied`。随后 relay 应用 [generation 规则](#authority-and-staleness)，并将该 link 设为资源当前的 serve peer。

relay 和 serve peer 以 `sandboxlink.HandshakeTimeout`（10 秒）限制每个握手步骤：WebSocket upgrade、Hello、读取 `Open`、`Bind` 及其回复，以及每次 Authority 调用。attach peer 用其 context 限制 Open。

## 打开 stream {#opening-a-stream}

attach peer 打开一个 stream 并发送 `Open`。relay 随后：

1. 调用 `Authority.AuthorizeOpen`。Authority 检查 grant、Runtime、当前 assignment 及其 epoch、资源 generation、允许的服务和访问权限，以及资源的 serve 授权是否当前有效。它返回 binding 身份、服务、lease、`ServiceFile` 的 export 和 `ServiceNetwork` 的 egress 规则。如果 Authority 裁决期间发生撤销，relay 会再次询问。
2. 依次检查：每条 link 的 stream 上限（`LimitExceeded`）；relay 见过的该资源最新 generation 不比 Open 中的更新（`StaleGeneration`）；Open 所指 generation 的 serve peer 已连接并提供该服务（`ServiceUnavailable`），且支持 Open 的版本（`VersionMismatch`）；非零的 `ExpectedServerInstanceID` 等于该 serve peer 的值（`InstanceChanged`）；lease 尚未到期（`LeaseExpired`）；relay 已以该 `AttachmentID` 持有的 attachment 具有完全相同的身份和 Runtime（`AttachmentConflict`）。
3. 向 serve peer 打开 stream 并发送 `Bind`。serve peer 回复 `Bound`，或回复失败：对其不提供的服务回复 `ServiceUnavailable`，或回复 `VersionMismatch`，`ExpectedServerInstanceID` 不是自身值时回复 `InstanceChanged`，对最近关闭的 attachment 回复 `LeaseExpired`，或回复 `ProtocolViolation`。relay 将失败转交给 attach peer。`Bind` 已开始发送但未收到回复时，relay 回复带 `EffectPossible` 的 `ServiceUnavailable`。
4. 回复 `Opened`，并将两个 stream 拼接起来。

attachment 的 binding 身份由其 `AttachmentID`、`Resource`、`SessionID`、`AssignmentID` 和 `AssignmentEpoch` 组成。无论在同一 link 还是之后的 link 上重新打开 attachment，都要求同一 Runtime 提供完全相同的身份，并具有当前有效的授权。

`Bind` 携带已授权的 binding，绝不携带 grant 或凭据：

- 对于 File stream，`Exports` 列出该 stream 可使用的 1 到 64 个 export，每项包含 ID 以及读写或 `ReadOnly`。export ID 由 1 到 64 个小写字母、数字、`_` 和 `-` 组成，同一列表中的 ID 互不相同。Process 和 Network bind 不携带 `Exports`。[沙箱引导](./sandbox-bootstrap.md#responsibilities-and-readiness)说明 File 服务提供哪些 export。
- 对于 Network stream，`Egress` 列出该 stream 可访问的目的地：位于某条规则前缀内的地址，端口在 `PortFirst` 到 `PortLast` 之间。空列表拒绝一切。每个前缀的主机位为零，`1 ≤ PortFirst ≤ PortLast`，且没有重复规则。File 和 Process bind 不携带 `Egress`。[网络协议](./sandbox-network-protocol.md#egress-check)说明服务如何应用它。

stream 的 export 和 egress 在打开时确定；续期只改变 lease。

## Authority 与陈旧性 {#authority-and-staleness}

relay 在内存中保存 link、attachment 和 lease。Authority 始终是持久的裁决方：

- `AuthenticateServe(ctx, ServeHello) (ServePeer, error)` 验证 serve 凭据及其服务的资源。
- `AuthenticateAttach(ctx, AttachHello) (AttachPeer, error)` 验证 Runtime 凭据。relay 将 `AttachPeer` 传给之后的每次调用，其 `Revision` 使 Authority 能够拒绝用此后已轮换的凭据认证的 link。
- `AuthorizeOpen(ctx, AttachPeer, Open) (Authorization, error)` 裁决 Open。
- `Renew(ctx, AttachPeer, RenewAttachment) (Authorization, error)` 裁决续期。其 `Authorization` 不指定服务、export 或 egress。

方法以 `*sandboxlink.Error` 表示类型化拒绝；其他任何错误都回复为 `ServiceUnavailable`。凭据 revision 和允许的访问来自 Authority，绝不来自 peer 的声明。

重新创建的资源具有更高的 generation。相同或更高 generation 的 serve peer 替换资源当前的 serve peer；更高 generation 还会以 `CloseStaleGeneration` 关闭旧 generation 的所有 attachment。generation 比 relay 见过的最新 generation 更旧的 serve peer 或 Open 会以 `StaleGeneration` 被拒绝。

## Lease、关闭与撤销 {#leases-closing-and-revocation}

- 每个 attachment 都有 lease，在 `Opened` 和 `AttachmentRenewed` 中以 `LeaseExpiresAt` 报告。lease 到期且未续期时，relay 以 `CloseLeaseExpired` 关闭该 attachment。
- 续期 relay 已不再持有的 attachment 返回 `LeaseExpired`；续期其他 Runtime 的 attachment 返回 `PermissionDenied`。一条 attach link 同时最多有 `sandboxlink.MaxControlRequests`（16）个续期在裁决中；对超出的续期，relay 不咨询 Authority，直接回复 `LimitExceeded`。
- `CloseAttachment` 以 `CloseRequested` 关闭调用方的 attachment。关闭未知 attachment 会成功，关闭其他 Runtime 的 attachment 返回 `PermissionDenied`。
- `Relay.RevokeAttachment` 关闭一个 attachment。`Relay.RevokeResource` 关闭某个资源 generation 及更旧 generation 的所有 attachment，向 serve peer 写入相应的 `AttachmentClosed` 事件，然后断开它。两者都以 `CloseRevoked` 关闭。

关闭 attachment 会重置其全部 stream，并向 serve peer 发送 `AttachmentClosed`；除 `CloseRequested` 外，也向其 attach peer 发送。relay 将每个 serve peer 未写出的事件保存在没有大小上限的集合中，仅在事件写出后才将其移除，因此已断开的 serve peer，或在事件写出前 link 断开的 serve peer，会在以相同 generation 重连时收到该事件。被关闭打断的 Open 根据原因以 `AttachmentConflict`、`LeaseExpired`、`PermissionDenied` 或 `StaleGeneration` 失败；其 `Bind` 可能已到达 serve peer 时带 `EffectPossible`。

丢失 link 会重置其承载的 stream，并保留其 attachment，直到 lease 到期或 attachment 被关闭。

## Stream 结束 {#stream-ends}

交给服务 handler 的 stream 和 `OpenService` 返回的 stream 实现 `sandboxlink.Stream`：`Read`、`Write`、`CloseWrite`、`Close`（有序结束写入并丢弃后续输入）、`Reset`，以及 `SetDeadline`、`SetReadDeadline` 和 `SetWriteDeadline`。与 yamux 的 deadline 一样，这些 deadline 限制 `Read` 或 `Write` 的等待时间：将等待超过 deadline 的调用以 `Timeout` 为 true 的错误失败，stream 仍可使用，`Read` 仍返回已缓冲的输入。一个 goroutine 可以在另一个 goroutine 写入时读取；并发读取或并发写入需要调用方自己加锁。

relay 通过 32 KiB 缓冲区复制每个方向的数据，每个 stream 最多持有一个 256 KiB 的 yamux 窗口：

- 某个 peer 结束其写入端时，relay 将结束前的全部字节写给对端，然后结束该方向的写入端。另一方向继续传输。
- 任一 stream 失败时，无论原因是 `Reset`、transport 错误、lease 到期、撤销，还是任一 link 丢失，relay 都重置两个 stream。中止绝不会变成有序 EOF。
- 任一 link 丢失都会立即重置两个 stream，即使 relay 正在等待向对端写入。
- yamux 只向该 stream 的读取方或写入方报告 peer 的 `Reset`。relay 正在等待向不读取的 peer 写入时，来自另一 peer 的 `Reset` 会在 relay 下次读写该 stream、attachment 关闭或 link 结束时到达。attachment 的 lease 限制这一等待。

## 失败 {#failures}

| Code | 名称 | 返回条件 |
| --- | --- | --- |
| 1 | `VersionMismatch` | Hello 不是版本 1，或 serve peer 不提供 Open 所需的服务版本 |
| 2 | `AuthenticationFailed` | 凭据无效 |
| 3 | `PermissionDenied` | Authority 拒绝该 Runtime、binding、服务或 peer，attachment 属于其他 Runtime，或 attachment 在 Open 期间被撤销 |
| 4 | `ResourceNotFound` | Authority 不知道该资源 |
| 5 | `ServiceUnavailable` | 没有 Open 所指 generation 的 serve peer 提供该服务，Authority 不可用，或 Open 期间 link 断开；请求可能已生效时带 `EffectPossible` |
| 6 | `StaleGeneration` | 该资源存在更新的 generation |
| 7 | `StaleAssignment` | 存在更新的 assignment epoch |
| 8 | `InstanceChanged` | serve peer 的 `ServerInstanceID` 不是 `ExpectedServerInstanceID` |
| 9 | `LeaseExpired` | lease 已过期，或 relay 不再持有该 attachment |
| 10 | `AttachmentConflict` | 该 `AttachmentID` 以其他身份或 Runtime 被持有，或在 Open 期间被关闭 |
| 11 | `LimitExceeded` | 达到 link 的 stream 上限，或达到裁决中续期的数量上限 |
| 12 | `ProtocolViolation` | 消息格式错误、出现在不允许的位置，或携带未递增的请求 ID |

`ServiceUnavailable` 和 `LimitExceeded` 是临时失败：相同请求稍后可能成功，`Code.Retryable` 将它们报告为可重试。其他 code 都是最终失败：以相同凭据、attachment 和 generation 重复请求会再次失败。

格式错误的响应，或其请求 ID、操作与请求不符的响应，会使该请求以 `ProtocolViolation` 和 `EffectPossible` 失败，因为请求可能已经生效。

## 验证 {#verification}

`go test ./internal/sandboxlink/...` 覆盖 golden 帧、解码拒绝，以及 relay 的授权、generation、lease、撤销、续期上限和重连行为，包括有序结束与中止的传播。`go test -run '^$' -fuzz FuzzDecode ./internal/sandboxlink` 对解码器进行 fuzz 测试。
