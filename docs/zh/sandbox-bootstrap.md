---
title: "沙箱引导"
source: docs/sandbox-bootstrap.md
source_hash: 8b705515313d1d190dac002902748ad9b60d6b24a3ff01cc549fdb70105ea50a
---

Sandbox Provider 通过交付一个引导文件来启动 Sandbox I/O 服务。本文负责 Provider 到该服务的启动输入。类型与验证器位于 [`internal/sandboxbootstrap`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxbootstrap/bootstrap.go)。服务凭此输入以 [沙箱 Link 协议](./sandbox-link-protocol.md)的 serve peer 身份连接 relay。

## 启动输入 {#launch-input}

将一个 JSON 对象交付到普通文件中，该文件仅允许服务账户和可信资源供应进程读取（Linux 上权限为 0600），并传入其绝对路径：

```sh
oac-sandbox-io --bootstrap-file /home/runtime/sandbox-io-bootstrap.json
```

该命令不接受其他参数，也不读取环境变量或配置文件。

| 字段 | 含义 |
| --- | --- |
| `version` | 精确的引导版本 `sandboxbootstrap.Version` |
| `link_url` | 符合 Link 协议 [URL 规则](./sandbox-link-protocol.md#handshake)的 relay URL：`wss://`，或仅指向 loopback 主机时使用 `ws://`，不含用户、查询或片段 |
| `credential` | Core 为此资源签发的 serve 凭据：非空，最多 4 KiB，不含空白或 NUL |
| `resource` | 该凭据服务的资源，是一个恰好包含下列字段的对象 |
| `resource.tenant_id`, `resource.environment_id`, `resource.id` | 规范的非零 UUID |
| `resource.kind` | `allocation` 或 `enrollment`；仅记录来源 |
| `resource.generation` | 资源的 generation，从 1 开始；重新创建的资源有更高的 generation |

解码器在每一层拒绝未知、重复、缺失和大小写别名字段，拒绝其他版本及超过 `sandboxbootstrap.MaxBytes`（16 KiB）的文档。错误不包含提交的值。文件缺失或格式错误时，服务在连接前失败。

该文件是服务唯一的认证输入。凭据不进入命令参数、环境变量或 URL；服务只在其 `ServeHello` 中发送凭据。

## 身份 {#identity}

服务以 Provider 启动它时使用的账户运行。输入不指定用户或组，服务也从不切换身份。Provider 本来就创建沙箱的账户并启动其中的进程，因此由它选择该账户，服务不需要降权代码。

## 职责与就绪状态 {#responsibilities-and-readiness}

Provider 创建账户和沙箱，交付该文件，并以该账户启动 `oac-sandbox-io`。`make build-sandbox-io` 构建静态 Linux 二进制。Provider 为进程重启保留该文件，仅在明确清理自己拥有的资源时删除。

在[自托管机器](./getting-started/self-hosted.md)上，`oac-daemon start` 充当该机器 enrollment 的 Provider。它用执行器凭据注册，并把该文件写入 Runtime 主目录下的 `daemon/sandbox-io-bootstrap.json`，其中包含 Core 返回的 `link_url` 和 `resource`，并以执行器令牌作为 `credential`。它以启动它的账户运行 `oac-sandbox-io`。服务退出时，它重新注册（轮换会推进 generation），然后重写该文件并重启服务。

服务验证输入并负责 link：它以 serve peer 身份连接，服务已绑定的 stream，并在凭据有效期间重连。`resource`（包括其 generation）必须是该凭据服务的资源，否则 relay 拒绝该 link。

File 服务只提供一个 [world export](./file-access-protocol.md#attach)，其隔离由 Provider 的沙箱设置负责。[Process 服务](./process-protocol.md#implement-a-service)以服务账户运行进程，服务作为 child subreaper 回收它们的孤儿后代进程。服务还提供 [Network 协议](./sandbox-network-protocol.md)：它在沙箱的网络命名空间中解析名称并建立 TCP 连接，范围限于每个 stream 的 `Bind` 携带的 egress。

服务无法启动时以非零状态退出，消息指出失败的步骤；`Serve` 返回[拒绝](./sandbox-link-protocol.md#implement-a-serve-peer)时，以 relay 的失败码退出。两种消息都不包含凭据。收到 SIGTERM 时，它停止接受 stream，像[所有权清理](./process-protocol.md#ownership)那样取消其活动操作，等待它们结束（最多为 cancel grace 上限加五秒），然后以 0 退出。

启动成功仅证明交付完成。当 relay 将服务作为该资源当前的 serve peer 持有，使对该资源的 `Open` 到达服务而不是以 `ServiceUnavailable` 失败时，服务才就绪。

## 验证 {#verification}

`go test ./internal/sandboxbootstrap` 覆盖输入契约，`go test ./apps/sandboxio/internal/sandboxio` 在 Linux 上针对测试 relay 运行服务。
