---
title: "沙箱节点协议"
source: contracts/agents-api/node-generation-protocol.md
source_hash: 1ee43dfcdd0eec0806ea3bc8a4c1227e10bd8ac5e69486505cb113a98e3f548a
---

沙箱节点在其主机上运行 Docker 或 microsandbox Provider，并通过一个 WebSocket 与 Core 相连。Core 通过该连接发送 Provider 操作；节点针对本地 Provider 执行这些操作，并报告就绪状态、主机测量值及其持有的部署代次。Core 始终是唯一的生命周期所有者：节点绝不重试变更操作或调度工作。帧和校验器位于 [`services/core/internal/sandbox/node`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/node)（`wire.go`、`generation_wire.go`）；节点用于注册和读取配置的 HTTP 路由位于[机器连接 API](machine-api.md#node-routes)。

## 帧与版本 {#frames-and-version}

每个帧都是一个 JSON 文本消息，其 `version` 等于 `node.ProtocolVersion`；两端都会拒绝任何其他版本，并且没有回退解码器。成员名必须精确且唯一：未知成员、大小写别名、重复项和意外的空值都会被拒绝。控制帧（`hello`、`welcome`、`heartbeat`、`heartbeat_ack`、`retention`、`retention_ack`）最多为 32 KiB；`request` 和 `response` 帧最多为 72 MiB。无效帧会关闭连接。

## 连接 {#connection}

1. 节点在其存储的 Core 源地址上发起对 `/api/v1/sandbox-node/connect?node_id=<uuid>` 的连接（对于 `https` 使用 `wss`），并以 Bearer 请求头发送节点凭据。Core 对被拒绝的凭据返回 401，节点将其视为永久性拒绝；其他任何失败（包括代理返回的 403）都会使用有界退避进行重试。当某个节点身份已有一个连接正在建立、存活或关闭时，Core 会以 409 拒绝第二个连接。
2. 节点须在 15 秒内发送 `hello`，其中包含节点身份（`node_id`、`installation_id`、`provider`、`backend_fingerprint`、已登记的 `deployment_generation` 和 `specification_digest`、`max_active`、`max_retained`）、首次健康报告；如果节点能够准备并保留多个部署代次，还包含 `generation_management: true`。除非身份与已认证节点匹配，否则 Core 会关闭连接。
3. Core 记录节点的存在状态，然后回复 `welcome`，其中包含新的 `connection_id` 和当前 `owner_epoch`；对于支持代次管理的节点，还包含 `deployment`：目标 `generation`、其 `specification_digest` 和可空的 `serving_generation`。节点保存更高的所有者 epoch，并拒绝更低的值。
4. 节点每 10 秒发送一次 `heartbeat`，其中包含 `connection_id`、`owner_epoch` 和健康状态。对于每次心跳，Core 都会再次认证节点凭据并检查所有者 epoch，记录健康状态并回复 `heartbeat_ack`；对于支持代次管理的节点，回复中还包含 `deployment`。任一端连续 35 秒未收到任何帧时都会关闭连接。

只要节点在当前所有者 epoch 下保持连接，并且最近一次心跳距今不足 45 秒，Core 就会将该节点计为在线。心跳会确立 Provider 的就绪状态和最近的主机测量值，但绝不表示 Session 活动。

健康报告包含 `provider_ready`、可选的固定 `diagnostic`、`observed_at`、最多为 32 的 `active_operations`，以及 [Runtime 遥测 API](runtime-observability-api.md#node-host-observations-and-history) 报告的主机测量值。未启用代次管理的节点每次报告时都会探测其 Provider；未就绪的 Provider 会报告一个固定诊断代码，该代码根据类型化探测错误进行分类；探测文本和主机路径保留在节点上。Core 会将未知代码存储为 `provider_unavailable`。[节点指南](../../../docs/zh/getting-started/nodes.md#readiness-codes) 列出了这些代码及其原因。支持代次管理的节点则按下文所述按代次报告就绪状态。

## Provider 请求 {#provider-requests}

Core 发送包含以下内容的 `request` 帧：

| 字段 | 含义 |
| --- | --- |
| `id` | 每个请求使用新的 UUID |
| `sequence` | 在此连接上，每个请求恰好递增 1 |
| `connection_id`、`owner_epoch` | `welcome` 中的值 |
| `deployment_generation` | 分配所属的部署代次，与其计算代次相互独立 |
| `operation` | 下列操作之一 |
| `timeout_ms` | 剩余预算，范围为 1 到 120000 |
| `reference` | 精确的 `(tenant_id, environment_id, allocation_id)` |

每个操作都携带自己的参数，并在成功时返回以下结果：

| `operation` | Provider 方法 | 参数 | 成功结果 |
| --- | --- | --- | --- |
| `create` | `Create` | `bootstrap` | `info` |
| `info` | `GetInfo` | 无 | `info` |
| `renew` | `Renew` | 无 | `info` |
| `kill` | `Kill` | 无 | 无 |
| `command` | `RunCommand` | `command` | `command` |
| `observe` | `Observe` | `observation` | `sample` |
| `initial` | `Initial` | 无 | `compute` |
| `new_compute` | `NewCompute` | 大于零的计算 `generation` 和可选的 `snapshot` | `compute` |
| `compute` | `GetCompute` | `compute` | `state` |
| `kill_compute` | `KillCompute` | `compute` | 无 |
| `resume_compute` | `ResumeCompute` | `compute` | `state` |
| `command_compute` | `RunCommandCompute` | `compute` 和 `command` | `command` |
| `suspend` | `Suspend` | `suspend` | `state` |
| `resume` | `Resume` | `resume` | `state` |
| `delete_snapshot` | `DeleteSnapshot` | `snapshot` | 无 |

只要 `connection_id`、`owner_epoch` 或 `sequence` 中任一值不匹配，请求就会关闭连接。格式错误的请求会得到 `invalid` 响应。未启用代次管理的节点仅接受其登记的 `deployment_generation`；支持代次管理的节点在对应代次的 Provider 上运行请求，无法运行时回复 `unconfirmed`。Core 仅向节点上已就绪的代次发送 `create` 和非 observe-only 的 `resume`，并且每条连接最多保留 32 个待处理请求。

预算采用相对计时：节点收到请求时以自己的时钟为基准锚定 `timeout_ms`，并在请求排队等待期间持续消耗该预算，因此各主机的时钟无需保持一致。Core 仍会限制自身等待时长。节点队列已满时会关闭连接。

`response` 帧包含 `id` 和 `connection_id`。成功响应携带操作表中指定的结果；对于 `kill`、`kill_compute` 或 `delete_snapshot`，响应不含结果字段。失败响应携带一个 `error_code`：

| `error_code` | 含义 |
| --- | --- |
| `invalid`、`ownership`、`exists`、`not_found` | `ErrInvalid`、`ErrOwnership`、`ErrExists`、`ErrNotFound` |
| `command_unconfirmed` | `ErrCommandUnconfirmed` |
| `observation_unavailable`、`runtime_not_running` | 对应的观察结果 |
| `unsupported` | 该操作被声明为不支持；见下文 |
| `unconfirmed` 或任何其他值 | 结果未知 |

失败响应不携带结果，唯一的例外是作为精确引用 `CreateSettled` 回执的 `info` 结果：即便已确认的原生 Create 在后续检查中失败，仍可证明该尝试已有确定结果。超时、响应丢失或断连属于不可用或不确定情况，绝不能证明资源不存在；发生这些情况后，Core 绝不重放变更操作，而是改为观察原始操作。[Sandbox Provider 指南](../../../docs/zh/sandbox-provider.md#operation-outcomes-and-retries) 定义了每种结果。

节点启动和代次加载会在接受工作前验证完整的 Provider 操作声明，Core 代理使用同一份已注册声明，因此不支持的操作会在节点解析或原生 I/O 之前被拒绝。操作清单由[操作契约](../../../docs/zh/sandbox-provider.md#explicit-operation-contracts)维护。`unsupported` 响应包含一个 `unsupported` 对象，其中有精确的方法 `operation` 和经作者编写且安全的 `reason`；代理会将两者与请求进行核对。证据缺失、格式错误或不匹配会得到 `unconfirmed` 结果，而绝不会证明变更操作被拒绝。`unsupported` 始终不同于观察不可用，也不同于计算或命令结果未知；它既不确定资源所有权，也不授权重放。

## 代次控制 {#generation-control}

未启用代次管理的节点只服务其登记的代次，配置固定，并且不会收到准备或保留帧。支持代次管理的节点会准备 Core 在 `welcome` 和 `heartbeat_ack` 中通告的目标代次，并在此期间继续服务其持久化的服务代次；目标代次的准备独立于服务 Provider 的就绪状态。

节点的 `hello` 和心跳最多携带八条代次观察记录。每条记录指定一个正值的有符号 64 位代次编号、其小写 SHA-256 规范摘要、`ready`、`preparing` 或 `failed` 状态，以及可选的固定诊断信息。目标代次和服务代次的记录排在前面，其余记录公平轮换。八条记录限制的是单条消息，而不是节点可保留的代次数量。省略某条观察记录绝不会授权删除，也不会暗示不存在。

保留使用独立且有界的交换。`retention` 请求最多指定八个本地 `(generation, specification_digest)` 引用、一个 UUID、一个每次递增 1 的 `sequence`、当前 `connection_id` 和所有者 epoch。`retention_ack` 必须与完整的待处理请求匹配，包括条目顺序和身份信息，并为每个条目给出显式布尔值 `keep`。每条连接只能有一个交换处于待处理状态，断连会将其丢弃。任何未经请求、重放、过期、不完整或混合的确认都不会删除任何内容。保留流量从不使用 Provider 请求队列。

## 本地保留与辅助程序生命周期 {#local-retention-and-helper-lifetime}

Core 的丢弃授权是回收的必要条件，但并非充分条件。排队和运行中的 Provider 调用、准备过程、本地目标代次以及服务固定引用都会保留引用；回收会再次检查这些引用，并拒绝在已取消的连接上执行。取消 Provider 调用方并不能证明其原生辅助程序已经停止；节点会将该辅助程序计为存活，直到其实际 `Wait` 返回。

每个代次都拥有一个永久私有租约文件 `state/node/generations/<generation>.lease`。在启动原生辅助程序之前，节点先在该文件上获取共享 flock，在持有该锁时验证持久化租约身份，并要求最终 Provider 配置已经发布且不存在 preparing、collecting 或 dropped 日志。辅助程序继承该描述符；节点仅在 `Wait` 返回后关闭自己的副本，且绝不显式解锁该共享打开文件描述，因此取消调用方或节点退出都不会释放仍在运行的辅助程序所持的引用。原生辅助程序在调用 SDK 前会设置描述符的 close-on-exec 标志，因此 VM 和守护进程后代不会继承它。

回收在检查引用、删除共享镜像或版本文件或发布 dropped 标记之前，先获取独占非阻塞 flock，并在这些变更期间持续持有该锁。租约文件属于稳定的节点状态，回收期间绝不删除或替换；符号链接、多重硬链接文件、外来所有权、不安全权限以及被替换的锁定路径都会被拒绝。在第一个辅助程序能够启动之前，安装器先独占创建租约并对其执行 fsync，随后以原子方式持久化私有 `.lease-identity` 记录，并对该记录及其目录执行 fsync。该记录绑定安装、代次、规范摘要、设备号和 inode。Python 回收器和 Go 辅助程序打开器在每次打开时都会验证同一记录，包括重启后；身份记录缺失时，两者都不会进行接管，也不会替换其 inode 或在回收后将其擦除。若安装在身份记录持久化前中断，则会拒绝重新接管，并保留该安装以供检查。身份记录被移除或租约被替换时，即使当前元数据一致，也会拒绝重新接管。

已丢弃代次绝不能再次准备或使用。辅助程序退出只能证明本地文件方面的状态，不能证明远程变更操作或结果不确定的 Provider 回执已释放；Core 对分配和放置的持久保留仍保持独立。

## 完全匹配的新安装 {#matched-fresh-installation}

主机程序版本与 Core 选择的 Runtime 版本彼此独立。全新节点从控制台当前版本获取其可执行文件和私有准备器，并从经认证的配置中读取确切的 Runtime 源、镜像身份以及原生运行时和固件摘要。当该 Runtime 较旧时，控制台仍会提供其不可变的 `releases/<source>/` 清单、校验和以及允许列表中的制品：Runtime 辅助程序、固件、seccomp 配置文件和镜像字节都来自所选版本；即使下载期间控制台的当前版本发生变化，制品 URL 仍固定到已验证的清单。

所需的保留版本缺失时，安装会被拒绝，而不会替换为当前 Runtime；仅包含另一个 Runtime 的本地捆绑包也会导致拒绝。所有这些拒绝都发生在安装器写入节点身份、导入 Runtime、注册节点或启动服务之前。

已发布的控制台版本会保留其元数据和制品字节。再次发布时，会先验证所有元数据及每个已存在的已声明制品，然后可以原子地仅添加缺失且校验和匹配的已声明制品；任何冲突都会阻止全部添加，并且不会覆盖任何内容。

## 重启恢复 {#restart-recovery}

Runtime 字节缺失时，绝不将固定的放置实例迁移到当前 Runtime。节点将原始代次和规范摘要保留为未就绪状态，并在恢复前通过 Core 的经认证配置路由请求该精确代次。缺少 seccomp 字节可能留下未就绪的 Provider 占位项；Provider 探测发现镜像或原生制品缺失时，会将修复排队，但不会通告就绪状态。

准备和修复串行执行。目标代次和服务代次优先，其余代次则以有界方式推进。每次尝试的截止时间为 30 分钟；失败后依次退避 1、2、5 和 10 分钟，此后最多退避 30 分钟。在连接提供部署信息之前，不准备任何内容。修复保留现有配置和路径，验证所选版本及每个现有同级文件的校验和，并且只下载缺失的不可变文件。字节冲突或保留规范不同都会拒绝修复；如果缺少 Provider 配置且没有精确的准备计划，也会拒绝，而不是重建该配置。

修复与回收使用相同的独占代次租约和安装锁，因此正在运行的辅助程序或回收器会保持所有权，修复稍后重试。字节恢复后，节点仍会运行 Provider 的就绪探测；文件存在和具备可执行能力永远不能证明就绪。

## 准备与回收记录 {#preparation-and-collection-records}

新的准备过程会写入两条不同的记录。下载前，`.preparing` 保存安装、代次和规范身份、私有 Provider 路径以及 `import_started: false`；它是恢复和回收计划，而不是 Provider。导入器运行前，计划会记录 `import_started: true`。Python 保留发现流程和 Go 重启恢复流程都能识别仅有待处理状态的计划，但绝不会据此构建、探测或获取 Provider；恢复或回收仍需要当前连接上的授权。

只有准备成功才会发布最终 `.json` Provider 配置，该配置只写入一次。Docker 记录其解析器返回的不可变本地镜像 ID；对于同一规范，构建器的配置身份或清单身份均可有效。准备日志清除之前，发布结果必须已经持久化。两者之间发生中断时，会重新验证同一计划和最终身份；计划、规范或路径发生漂移时会拒绝。已取消或失败的导入仍会向 Core 的保留交换保持可见，但不会成为服务代次。

在删除任何原生层对象或版本文件之前，节点都会持久化一份私有回收日志，并将其绑定到安装、代次和规范摘要。重启后，未完成的日志仅是保留交换的候选项：它不能准备、探测、获取或通告该代次，必须重新获取一份与之关联的新 Core 丢弃授权后才能继续。在删除版本文件之前先持久化原生清理完成状态，因此重试可以完成部分删除的版本，而不会运行已经删除的辅助程序。完成持久化文件清理后，再写入与摘要绑定的 dropped 标记，以防重新接管；这些小型的配置和所有权日志会保留为本地身份记录。

对于仅属于该安装的 microsandbox 存储，成功且完整的原生镜像清单可以区分“不存在”和“CLI 失败”；查询失败、清单格式错误、原生层因正在使用而拒绝或所有权不明时，都会保留这些字节。共享 microsandbox 镜像和私有版本会一直保留到最后一个本地引用消失。Docker 镜像属于主机的共享守护进程：自动回收绝不删除或修剪这些镜像，只有主机管理员在确认主机上没有安装需要它们后才能删除。

全新安装还会另行记录其 Runtime 文件的已验证校验和，与主机程序分开保存。回收原始代次时，只删除未被任何保留配置引用的确切私有 Runtime 辅助程序、可执行文件、固件、seccomp 和导入缓存文件。第一次删除前会检查每个剩余文件；哈希未知、字节发生变化、存在链接或缺少所有权元数据时，都会拒绝清理。节点可执行文件、准备器、身份、基础 Provider 配置和清单会保留，因此重启后的节点仍可读取其登记身份并构建较新的保留 Provider。删除共享原生路径前，会先在所有保留配置之间对这些路径进行比较。

下载中断后，只会修复原始路径中缺失的字节。如果在任何导入尝试之前执行回收，准备日志会证明该代次没有已导入的原生镜像。如果某代次的原生可执行文件缺失，且导入可能已经开始，该代次仍会保留：文件缺失永远不能证明原生制品不存在，而空的原生清单也永远不能抹除回执或存储历史。

诊断代码编写于 `services/core/internal/sandbox/node_diagnostic.go`。共享的 `services/core/internal/sandbox/testdata/node-diagnostics.json` 测试夹具检查 Go 映射、OpenAPI 源注释和生成的枚举，以及 TypeScript 客户端声明。Web 使用客户端规范化器，并检查每个已声明代码的本地化消息。代码变更时要同步更新这些投影；未知代码会规范化为 `provider_unavailable`。

准备诊断使用固定的类型化原因。只有制品传输、校验和或版本来源验证失败才会报告 `runtime_download_failed`；私有准备器通过退出类别指示这一类失败，Core 和节点都不解析 stderr。Provider 故障、所有权故障、取消和未分类故障保留其类型化代码，或使用 `provider_unavailable`。协议中不会传输任何 Provider 原始文本。
