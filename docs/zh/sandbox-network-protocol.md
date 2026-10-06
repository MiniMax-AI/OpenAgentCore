---
title: "沙箱网络协议"
source: docs/sandbox-network-protocol.md
source_hash: 208762673da0dbecec132f5d1af99316b6c133aa7a83e473e3361919ad385796
---

Network 协议是 agent host 打开一条源自沙箱的 TCP 连接的方式。agent host 通过 Link 打开一个 Network stream 并发送一个 `Connect`。Sandbox I/O 服务用沙箱的解析器解析名称，按 Link 绑定到该 stream 的 egress 检查目标，从沙箱的网络发起连接并作答。`Connected` 之后，该 stream 在两个方向上承载连接的原始字节。凭据和 TLS 留在 agent host：服务转运客户端写入的字节，不读取其中任何内容。

[`internal/sandboxnet/protocol.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxnet/protocol.go) 是唯一的权威定义：消息 tag、payload 布局、验证器、egress 规则和 `Service` 接口。同一个包中有客户端和通用服务端。[`apps/sandboxio/internal/netservice`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/sandboxio/internal/netservice) 是 Linux 服务。帧使用共享的[帧格式](./sandbox-link-protocol.md#framing)。stream 的授权（包括其 `Egress`）在 stream [打开](./sandbox-link-protocol.md#opening-a-stream)时来自 Link，绝不来自 `Connect`。

## 连接如何工作 {#how-a-connection-works}

1. 客户端为 `ServiceNetwork` 版本 1 打开一个 Link stream。
2. 客户端发送一个 `Connect`，其 request ID 遵循 [request ID 规则](./sandbox-link-protocol.md#framing)，在收到应答前不再写入任何内容。
3. 服务按 stream 的 egress 检查请求，解析主机，并在 `TimeoutMillis` 内连接第一个获准的地址，详见 [Egress 检查](#egress-check)。
4. 服务以该请求的 ID 应答 `Connected` 或 `Failed`。`Failed` 之后，它有序结束该 stream。`Connected` 之后，两个方向都承载不分帧的原始字节。

每个方向各自结束。客户端的有序结束（`CloseWrite`，即 FIN）在其之前的所有字节之后，作为 TCP 半关闭到达目标；目标的半关闭在其之前的所有字节之后，作为 EOF 到达客户端。半关闭不启动任何超时，另一个方向继续传输。

其他任何情况都会中止两个方向：目标发来的 reset、任一侧读写失败、attachment 结束或 link 丢失。客户端随后读到一个绝不是 EOF 的错误，服务则 reset 该 socket。`Connected` 之后，失败从不以帧报告。如 [Stream 结束](./sandbox-link-protocol.md#stream-ends)所述，服务只有在下一次读写该 stream 时才得知 reset：如果客户端半关闭之后 serve link 断开，而目标保持静默，socket 会一直打开，直到目标发送或结束，或 attachment 关闭。

一个 stream 承载一条连接。没有重放或续传：丢失的应答使该次尝试结果不确定，也不会有任何东西自动重试。

## 实现客户端 {#implement-a-client}

Go 客户端是 `sandboxnet.Connect(ctx, stream, host, port, timeout)`。它接收一个新打开的 Network stream，从此拥有该 stream，并返回一个 `*sandboxnet.Conn`，它是一个 `net.Conn`。

- 失败是一个带 `Code` 和 `Effect` 的 `*sandboxnet.Error`，`errors.Is(err, sandboxnet.CodeDenied)` 匹配其 code。参数验证失败时以 `InvalidArgument` 失败；请求发送之前 context 已结束时以 `Cancelled` 或 `TimedOut` 失败，两者都带 `EffectNone`。一旦请求可能已发送，丢失应答（`IO`）、应答格式错误（`Unknown`）或 context 结束（`Cancelled` 或 `TimedOut`）都以 `EffectPossible` 失败：沙箱可能已经建立连接。`Connect` 从不重试。
- `timeout` 向上取整到整毫秒，必须在 1 ms 到 60 s 之间。context 限定等待应答的时间。
- `Conn.CloseWrite` 在进行中的 `Write` 之后有序结束写方向。`Conn.Close` 仅在 `Read` 已返回 `io.EOF`、没有失败的读写且没有进行中的 `Write` 时有序结束；否则它中止连接，使未读输入不会占住 Link 的流控窗口，挂起的调用也会返回。`Conn.Reset` 显式中止。
- deadline 遵循 `net.Conn`：deadline 一旦过去，即使有已缓冲的输入，`Read` 或 `Write` 也返回 `os.ErrDeadlineExceeded`，直到 deadline 被延长。各方法可以并发调用。`RemoteAddr` 是请求的主机和端口。`LocalAddr` 为空，因为协议不报告沙箱的本地端点。

其他语言的客户端遵循相同规则：每个 stream 一个 `Connect`，应答前不发送字节，不自动重试带 `EffectPossible` 的失败。

## 实现服务 {#implement-a-service}

实现 `sandboxnet.Service`，并用 `sandboxnet.Serve(ctx, stream, bind.Egress, service)` 服务每个 Network stream，其中 `ctx` 是 attachment 的 context。`Serve` 负责协议：

- 它读取一帧。不是 `Connect` 或 request ID 为零的帧会 reset 该 stream。验证失败的 `Connect` 以 `InvalidArgument` 应答。
- 解析和连接期间它持续读取 stream。在连接成功之前到达的任何字节（包括第二个 `Connect`）都是协议违规：它取消连接并 reset 该 stream。之后到达的字节会等到 `Connected` 写出后再转发，因此应答之前没有任何内容到达目标。
- 它执行 [egress 检查](#egress-check)，并调用 `Resolve` 和 `Dial`。`Dial` 恰好连接给定的地址，使用该地址的协议族，从不解析或尝试其他地址。
- 它原样应答来自 `Resolve` 或 `Dial` 的 `*Error`。其他错误在超时已过时变为 `TimedOut`，来自 `Resolve` 时变为带 `EffectNone` 的 `NameResolutionFailed`，来自 `Dial` 时变为带 `EffectPossible` 的 `IO`。
- `Connected` 之后，它把 stream 和 socket 拼接起来。`ctx` 结束时随时中止两者。

`netservice.New()` 返回 Linux 服务。它用沙箱的系统解析器解析名称，该解析器读取沙箱的 `/etc/hosts` 和 `/etc/resolv.conf`；它从沙箱的网络命名空间按地址类型通过 `tcp4` 或 `tcp6` 发起连接。`Service.Handle` 是 `sandboxlink.ServiceNetwork` handler 的 `Serve` 函数。它按 errno 为失败的连接分类，见[失败表](#failures)。

## 参考 {#reference}

### 消息 {#messages}

```text
Version = 1                    // the Link service version of ServiceNetwork
OpConnect = 1
ConnectResponseTag = 0x8001

ConnectRequest payload, in order:
  Network        uint16        // NetworkTCP = 1
  HostLength     uint32
  Host           byte[HostLength]
  Port           uint16
  TimeoutMillis  uint32

ConnectResponse payload, in order:
  Result         uint16        // ResultConnected = 1, ResultFailed = 2
  if ResultFailed:
    Code         uint16
    Effect       uint16        // EffectNone = 1, EffectPossible = 2
```

payload 最多 265 字节。未知的枚举值、`Connected` 之后的失败字段以及尾随字节都属于格式错误。

### 参数 {#arguments}

- `Host` 为 1 到 253 字节：不带方括号且不含 zone 的 IPv4 或 IPv6 字面量，或 DNS 名称。名称由点分隔的 label 组成，末尾可带一个点。每个 label 由 1 到 63 个 ASCII 字母、数字、连字符和下划线组成，且不以连字符开头或结尾。最后一个 label 不能全是数字。含非 ASCII 字符的名称以 IDNA A-label 形式（`xn--`）发送。NUL、空白、URL、方括号和内嵌端口都会被拒绝。
- `Port` 在 1 到 65535 之间。
- `TimeoutMillis` 在 1 到 60,000 之间，涵盖解析加连接的时间。

### Egress 检查 {#egress-check}

服务按以下顺序用 stream 的 [`Egress`](./sandbox-link-protocol.md#opening-a-stream) 检查 `Connect`：

1. 没有规则允许该端口时，应答为 `Denied`，不做任何解析。
2. IP 字面量是唯一的候选地址。否则服务解析名称，返回的每个地址都是候选。
3. 每个候选与端口一起检查。IPv4 映射的 IPv6 地址按 IPv4 检查和连接。带 zone 的地址永不允许，未指定地址（`0.0.0.0`、`::`）也不允许，因为无论规则如何，TCP 协议栈都会把它连到沙箱自身。其他所有地址（包括多播和广播）都按原样检查：TCP 无法连接这些地址，因此允许它们只会导致连接失败。
4. 服务对第一个获准的候选只连接一次，直接使用该地址，不再解析。没有候选获准时，应答为 `Denied`。

### 失败 {#failures}

| Code | 名称 | Effect | 返回时机 |
| --- | --- | --- | --- |
| 1 | `InvalidArgument` | None | `Connect` 格式错误，或其主机、端口或超时超出范围 |
| 2 | `UnsupportedNetwork` | None | 服务不提供所请求的网络 |
| 3 | `Denied` | None，下述情况除外 | egress 既不允许该端口也不允许任何候选地址，或沙箱拒绝了 connect（`EACCES`、`EPERM`） |
| 4 | `NameNotResolved` | None | 名称不存在或没有地址 |
| 5 | `NameResolutionFailed` | None | 解析因其他原因失败 |
| 6 | `ConnectionRefused` | None，下述情况除外 | 目标拒绝连接（`ECONNREFUSED`） |
| 7 | `Unreachable` | None，下述情况除外 | 沙箱没有到目标的路由（`ENETUNREACH`、`EHOSTUNREACH`、`ENETDOWN`、`EHOSTDOWN`、`EAFNOSUPPORT`） |
| 8 | `TimedOut` | 解析期间或客户端发送请求之前为 None，之后为 Possible | `TimeoutMillis` 已过或内核放弃了 connect（`ETIMEDOUT`）；对客户端而言，其 context 的 deadline 已过 |
| 9 | `ResourceExhausted` | None，下述情况除外 | 沙箱耗尽了端口、描述符或内存（`EADDRNOTAVAIL`、`EMFILE`、`ENFILE`、`ENOBUFS`、`ENOMEM`、`EAGAIN`） |
| 10 | `Cancelled` | 请求发送之前为 None，之后为 Possible | 客户端的 context 被取消，或 attachment 已结束 |
| 11 | `IO` | Possible | 连接因其他原因失败；对客户端而言，stream 在应答到达前失败 |
| 12 | `Unknown` | Possible | 对客户端而言，应答违反了协议 |

由 errno 分类得到的 code 只有在 `socket` 或 `connect` 本身返回该 errno 时才带 `EffectNone`。连接过程中较后的步骤（例如在 `connect` 发出后把 socket 注册到 poller）返回同一 errno 时带 `EffectPossible`，因为连接可能已经建立。

## 验证 {#verification}

`go test ./internal/sandboxnet/ ./apps/sandboxio/internal/netservice/` 覆盖 golden 帧、解码拒绝、主机语法、egress 规则以及在 `Connected` 写出前暂存字节；并通过测试 relay 覆盖双向字节、两侧各自的半关闭、目标 reset、并发写入、已过的读 deadline、不发起连接的 `Denied`（包括未指定地址）、`NameNotResolved`、`ConnectionRefused`、`TimedOut`、第二个 `Connect` 以及丢失的应答。`go test -run '^$' -fuzz FuzzDecode ./internal/sandboxnet` 对解码器做 fuzz 测试。
