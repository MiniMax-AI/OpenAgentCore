---
title: 工作区文件系统 Provider
source: docs/workspace-provider.md
source_hash: e9749e1aab1fc2135dcd92a843263243e4adccea393038c495fec67e114dbe83
---

独立工作区文件系统边界由 [`workspacefs.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/workspacefs/workspacefs.go) 定义。本文规定必需的集成契约，并不表示所有 Sandbox Provider 或执行位置均已实现。文件系统适配器拥有存储对象与原生挂载解析职责；[Sandbox Provider](sandbox-provider.md) 拥有计算资源职责。Core 在分配任一资源前选择并验证二者的组合。

## 标识与配置 {#identity-and-configuration}

`Reference` 包含 `TenantID`、`EnvironmentID` 和 `ObjectID`，均为不可变、规范小写且非零的 UUID。Core 在创建前持久化引用。对象标识永不复用，包括删除后或变更结果尚未确认时。适配器必须在暴露或删除对象前验证完整的归属元组。

`Configuration` 是 Core 数据库中的不可变记录，具有规范 UUID `ID`、`Adapter` 标识和由适配器定义的 JSON 对象 `Parameters`。数据库是其唯一编写来源；更改配置须创建新的标识。Core 转发参数而不解释原生路径。现有对象始终绑定其原始配置。

`Attachment` 将适配器签发的 JSON 对象 `Native` 绑定到完整引用、配置 ID 和挂载类型。它是内部归属凭证，不是应用提供的主机路径。共享验证检查信封结构以及预期引用和配置的匹配；所选适配器还必须验证参数、凭证及原生归属。结构有效本身绝不授权文件系统访问。公开 Session 载荷不得提供挂载凭证、配置参数或解析后的主机目录。

`Binding` 临时携带配置和挂载凭证至节点解析器，不引入第二份持久化节点配置。`Resolver.Resolve` 验证绑定后返回用于本地计算挂载的 `Directory{Path}`。仅适配器负责将原生存储解析为规范绝对路径；Core 不构造路径。结构有效的目录本身不是归属凭证。

## 操作与结果 {#operations-and-outcomes}

每个控制适配器实现 `Declaration`、`Check`、`Create`、`Observe` 和 `Delete`；每个节点挂载适配器实现 `Resolve`。不得使用可选旁路接口、隐式替换适配器或 nil 实现回退。

| 操作 | 必需语义 |
| --- | --- |
| `Declaration()` | 描述所选配置已经验证的挂载、用户扩展属性和配额行为。 |
| `Check(ctx)` | 验证配置和可用性，不创建对象。 |
| `Create(ctx, reference)` | 创建归属对象或收敛到其现有挂载凭证，不覆盖数据；重试与并发调用使用同一不可变引用。 |
| `Observe(ctx, reference)` | 返回现有对象的绑定挂载凭证，不创建、修复或准备对象。 |
| `Delete(ctx, reference)` | Core 授权删除且确认计算资源停止后，收敛到已验证归属对象的终态删除。 |
| `Resolve(ctx, binding)` | 验证原生归属并为本地计算解析挂载。 |

错误支持 `errors.Is`：`ErrInvalid` 表示无效输入，`ErrOwnership` 表示引用或配置不匹配，`ErrUnavailable` 表示存储不可用，`ErrUnconfirmed` 表示变更结果未知，`ErrNotFound` 表示对象不存在，`ErrUnsupported` 表示不支持的组合。适配器包装错误时保留这些结果。观察到不存在不证明先前变更已结束。超时或上下文取消只结束调用方的等待，不证明原生操作停止或回滚。结果未知时 Core 保留归属。Core 可以使用同一持久化不可变引用和配置重试 `Create` 或 `Delete`，包括收到 `ErrUnconfirmed` 后；绝不能通过替换新对象标识重试结果不确定的操作。

同一引用的重试与并发收敛是适配器必须提供的保证。`Create` 保留现有对象数据并返回该归属对象的挂载凭证，绝不重新初始化存活对象。重复或并发的 `Delete` 调用收敛到同一终态删除。一旦记录终态删除，并发、延迟或重试的 `Create` 均不得使对象复活，`Resolve` 也不得暴露该对象。允许保留删除元数据，并在先前进行中的操作结束后继续清理。这些保证适用于存储生命周期操作，不表示超时时变更已结束，也不提供分布式计算隔离。

## 能力验证 {#capability-validation}

`Requirements` 声明计算位置的挂载类型及 `user_xattr` 需求。`Declaration` 报告文件系统适配器的挂载类型、`UserXAttr` 支持和 `CapacityQuota` 强制执行能力。`ValidateCombination` 是共享的分配前准入检查。当前挂载类型为 `host_directory`；未知类型会被拒绝。用户扩展属性能力表示挂载后的文件系统支持 Environment 准备流程使用的用户扩展属性。

请求容量为零表示未请求配额。以 MiB 为单位的正容量仅在适配器实际强制执行该限制时获准；报告可用空间或接受未经检查的数字均不足够。适配器的不可变配置必须强制执行获准上限。适配器不能仅根据 Sandbox Provider 的计算磁盘大小声明配额支持。

工作区仅允许单写入方访问。Core 必须串行管理计算资源归属，并在挂载替代计算资源前确认先前写入方已停止。本协议没有跨虚拟机锁能力：客户机内的 `flock` 不证明主机锁或对其他客户机的互斥。执行位置不得基于客户机本地锁宣称并发挂载安全。

## 生命周期与文件系统范围 {#lifetime-and-filesystem-scope}

对象包含整个 `/environment` 目录树，包括工作区、暂存文件、初始化状态和包内容。它们位于同一文件系统，以保留 Environment 准备期间文件系统操作的语义。Harness 的私有原生历史和配置仍属于检查点状态，不重定向到工作区对象。

工作区存储随 Session 保留，直至显式删除 Session。归档、过期、重置和删除计算检查点均不授权删除工作区。Core 仅在显式删除 Session 且确认计算资源停止后调用 `Delete`；未解决的计算或存储变更须保留归属。Session 仍被保留时，创建失败不授权删除其存储。本契约不定义快照过期恢复。

## 内核 NFS 适配器 {#kernel-nfs-adapter}

`nfs` 适配器使用由运维人员预挂载的 Linux 内核 NFS 文件系统。支持的配置是受信任 AUTH_SYS 客户端上的 NFSv4.2，启用 `root_squash`，Core 和节点使用相同导出和绝对挂载路径。适配器不挂载导出，也不管理 NFS 凭据或服务账户。其他操作系统返回 `ErrUnsupported`。[配置文档](./configuration.md#independent-workspace-storage)负责安装步骤和选择方式。

参数精确包含 `root`、`namespace_id` 和 `uid`。`root` 是规范的绝对挂载路径；`namespace_id` 是非零规范 UUID；`uid` 是 Core 和节点均须使用的非零有效主机 UID。初始部署使用 65532。运维人员将挂载根目录设置为该 UID 所有、权限 `0700`，并准备同 UID 所有、权限 `0600` 的常规文件 `.oac-storage-root`，内容精确为命名空间 UUID 加一个换行符。应由导出服务端管理员在服务端存储上完成此操作；启用 root squash 的客户端不能假定有权变更所有者。根标记声明所选导出的身份，并非凭据或分布式锁。

构造函数只验证并规范化 JSON，不执行文件系统 I/O。`Check` 验证打开的根目录确实位于内核 NFS，有效 UID 与私有所有权和权限匹配，命名空间标记匹配，并且实际文件描述符上的 `user.*` xattr 设置、读取和删除成功。包括打开根目录和可用性检查在内的所有文件系统操作，共享每进程最多 32 个在途操作的预算。调用者超时后收到 `ErrUnconfirmed`；该操作仍占用名额，直到内核调用返回。预算耗尽返回 `ErrUnavailable`。相对于目录描述符的操作始终锚定已打开的根目录。挂载不可用或配置路径只是本地目录时，验证失败，不创建本地回退。

能力声明为 `host_directory`、`user_xattr: true`、`capacity_quota: false`。原生挂载凭据仅包含命名空间标识；解析时根据不可变配置和文件系统所有权验证完整 binding，随后返回根目录下的 `objects/<ObjectID>/live/data`。仅此数据目录暴露给 guest。租户和应用输入都不提供主机路径。

每个对象拥有 `objects/<ObjectID>/identity`、`staging/`、`live/`、`trash/`，以及删除开始后的 `deleted-marker`。永久标识绑定租户、Environment 和对象 UUID。Create 先准备并同步唯一的私有 staging 封装目录，再原子发布其非空目录为 `live`；重放不会覆盖现有 live 数据。Delete 先持久记录对象本地的终态标记，将 live 数据退役到唯一的对象本地 trash，再先删除数据、后删除其所有权标记。并发删除者收敛。最少量的标识和删除元数据会有意保留；删除不代表所有元数据文件均消失。Create 和 Resolve 拒绝已退役标识。迟到的在途 Create 可能留下空的受控元数据，需要在其系统调用完成后重复 Delete；它不能授权新写入者或复用对象标识。清理只查看该对象的 staging 和 trash，不全量扫描命名空间，也不使用后台服务。

NFS 存储不提供计算 fencing 或自动分布式故障转移。Guest `flock` 不是跨 VM 写入者锁。上述单写入者和显式删除要求仍然必须遵守。共享文件系统保留 `/environment`，Harness 的私有原生历史仍在计算检查点内；因此，即使工作区文件仍然存在，原生快照过期仍可能导致私有历史丢失。保留工作区不代表原生快照 TTL 到期后仍可恢复原生执行。
