# Agent outside sandbox：统一计划与暂停交接

更新时间：2026-10-10。状态：**用户主动暂停探索**。本文件是本分支唯一的进度与规划入口；恢复工作前先读本文件、[AGENTS.md](AGENTS.md) 和对应协议。它记录工作状态，不替代产品协议或覆盖率文档。

## 目标与边界

将第三方 Harness 的 agent loop 放在 agent-host，将任务工作区文件、外部命令和执行环境放在 sandbox，使两者的运行时、生命周期和扩缩容解耦。对 Harness/agent 透明，对最外层用户保持一致的工具行为；不能接受仅仅拆开架构，却引入严重串行远程开销。思想参考 [Tetral: The Next Scaling Problem](https://tetral.ai/blog/the-next-scaling-problem/)，区别是本项目使用第三方原生 Harness，不自建模型或工具循环，也不直接复制其 reducer/持久化架构。

用户已明确：少量解析、文本处理和编辑计算可以留在 agent-host，但任务文件必须映射到 sandbox。Harness 自身运行闭包、私有历史和控制资源有明确的 host 归属，不要求把任意进程内计算迁移到另一台机器。通用机制优先，不为每个 Harness 新建一套执行后端。成熟项目和维护中的 SDK 优先；若方案接近跨机器操作系统模拟的复杂度，先讨论具体成本和取舍，再实施。

## 必须保留的约束

- 协议优先、组件可替换。Core 只编排协议行为；Provider、Harness 和模型差异收在 adapter 内，共用的取消、准备、复用和性能问题修在公共流程。每个边界只有一个协议定义与文档，变更同时更新实现和投影。
- 不因 vendor/Harness 名称增加 Core 分支、表、字段、设置或私有旁路。能力明确声明、组合明确验证，不支持时返回 typed error，不做隐式 fallback。
- 使用原生 Harness loop 与维护中的 SDK/协议；不另造 executor、工具循环或兼容框架来伪造一致性。官方 Agents API 以仓库 pin 为准，不能因引擎限制缩小契约；真实差异保留在 coverage ledger。
- 一份设置和数据只有一个归属；不新增环境变量或文件别名/fallback。项目 pre-release，替换过时路径时直接删除，不保留兼容层。凭证不提交到源码、计划或日志。
- 性能收益必须有可复现 baseline，明确 RTT、数据量、延迟、依赖、内存/CPU 成本和未测范围。不做当前测试网络专用的产品调优；不以 TTL、跳过权限检查或弱化一致性换取延迟。
- 当前安装范围仅 Linux amd64。arm64/macOS/Windows 原生安装与执行资格后续再补，不把交叉编译当成原生资格。
- 用户选定：不响应取消的 stdio MCP 服务及其子进程应强制终止，确认停止后再完成取消；后续使用重连，其他服务和工作区保留。该服务先前的后台任务也会结束。
- 用户批准公共取消确认预算 30 秒；普通函数 ACK 15 秒、资源关闭 10 秒不随之改变。预算不是完成证明，不允许伪造 settled。
- 开发采用独立 worktree 和分支；subagent 负责有边界的实现/研究，协调者集成。困难设计可咨询 Fable 5.1 high，必要时 Opus 5.5 max，注意成本。独立盲审发现按影响和 ROI 选择处理，极端 corner 可以明确保留；小修不自动重复盲审。
- 合并构建批次、复用内容未变的产物、只重验受影响流程；独立环境可并行，同一个 agent-host 的重启串行。CI 后台检查，不把等待 CI 变成停工；最终报告完成前处理真实失败。
- 授权检查→执行→收集→清理完整流程；只在实际失败时停下来分析。轻量证据即可，不叠加收据、哈希、协调审核。初始化、原生准备、模型响应分别计时，优先定位第一个明确卡点。
- 本分支不得擅自合入 main 或发布版本。恢复探索需用户明确安排；当前只整理、保存、清理并切回 main。

## 已集成的架构与实现

保存基线为 `7ca615c3766fb0586f47c8025b69b9885e10eadf`，包含此前 `aos/cutover` 全部已合工作。main 停在 `5e6c1e609c761a211f3c8053b157d38768ec2831`，两者不在本次整理中合并。

| 范围 | 当前状态 |
| --- | --- |
| Agent host / sandbox 分离 | 原生 loop 在 host，sandbox 不运行 agent loop；Sandbox I/O 承载任务进程、已安装 MCP 等工作；旧 guest Runtime 路径删除 |
| File / Process / Network / Link | 共用协议和实现；sessionview/worldfs 映射 sandbox world，process shim/broker 代理命令，gateway 保管凭证 |
| 工作区与准备 | 明确绑定 Environment workspace；公共准备/能力读取/导出改为有界并发或已有池复用，Mcode 必需 workspace 工具原生 discovery 作为准备门控 |
| 生命周期和取消 | Process descendants/cgroup 管理、leader 退出后的取消、stdio MCP 强取消与重连、公共取消确认预算已落地 |
| 分发和配置 | Core 选择并保存 RuntimeRelease；source_commit/artifacts 与 adapter 所需 pins 一致；节点安装分发归 Core，Web 不再注入 Runtime；禁止原地跨 release 升级的 guard 保留 |
| 架构整改协作线 | T2e、T6a、T6b、10-P2(c)、L/T6d、T5d 已合：typed 闭集、开放 observation reason、单一 dispatch/route/ownership、冗余 wrapper 删除、machine 注册与生成投影、安装器声明查表 |
| 文档/契约 | EN/zh 同步；API/schema/client 从拥有者生成；coverage ledger 保留 native 差异、真实失败与未资格项 |
| 最新公共文件优化 | 同一 syscall 内的完整路径 File.Walk 批次已合，CI 全绿；不跨 syscall 缓存，不改变权限/一致性语义 |

产品结构和协议以 [architecture](docs/architecture.md)、[runtime protocol](docs/runtime-protocol.md)、[Harness onboarding](contracts/agents-api/harness-onboarding.md)、[Sandbox Provider](docs/sandbox-provider.md)、[coverage ledger](contracts/agents-api/index.md) 为唯一权威；本计划不复制完整协议或所有已知 API 差异。

## 已有资格证据

- microsandbox 是优先支持的 Sandbox。Claude、Codex、Mcode 均已完成真实执行、取消、agent-host 重启、自然 idle suspension、snapshot、Files 唤醒/restore、历史续接和资源清理。
- 三个 Harness 的已安装 stdio MCP 强取消与重连均通过，包括终止后代、保留无关服务及工作区。
- Claude 普通前台 Bash 取消已通过：命令只启动一次，终态 cancelled，取消后文件副作用连续 3 秒稳定。一次同 DB 时钟测得 21.918422 秒，在批准的 30 秒内；不能据此声称所有情况都只需该时长。
- E2B 主链路、public Q5/Q9、Mcode 受影响完整组合已有通过证据。既有通过不表示全部官方字段或全部 native 组合均已资格。
- 真实网络故障、测试提示导致原生模型拒绝、测试脚本问题和产品失败分别记录；不把暂停或未执行步骤算成通过。

## 最新性能变更及边界

最新部署候选为 `79486373edcd2fdec83973ef06a3f3f63743873f`，其整棵源码树与保存基线 `7ca615c3` 完全相同。合并提交号变化无需重构建。官方构建、产物验证、传输、加载、Core/agent-host/Web 部署及节点 enrollment/readiness 已通过；最终原生 Read/Edit/cancel 回归在用户暂停时尚未完成。

实现使用 cilium/ebpf 0.21.0，在 Linux amd64 的 syscall entry/exit 中提供有界路径提示，由现有 worldfs 在**同一 syscall epoch** 内消费 File.Walk 结果。只覆盖适用的 statx/newfstatat/readlink/O_PATH 情况，kernel 仍执行原操作。引用由既有 Forget 流程回收，syscall 结束清理对应 scope，最后一个启用 View 关闭后释放共享 BPF 资源。没有新增 Core 分支、远端协议、服务或产品设置；BPF 不可用时执行原本 FUSE 路径，不改变功能支持。

| 测量 | 基线 | 优化后 | 能说明什么 |
| --- | --- | --- | --- |
| 注入 100 ms metadata 延迟，native Claude Bash | 41.909 s | 25.333 s | 此受控 fixture 约减少 40%，仍然很慢 |
| 同条件 native Read | 12.229 s | 5.672 s | 此受控 fixture 约减少 54%，不等于真实网络保证 |
| 旁路 host getpid | 168.495 ns | 235.780 ns | 有活跃 scope 时附加约 67 ns，全局 hook 有成本 |
| 旁路 host statx | 793.098 ns | 859.760 ns | 有活跃 scope 时附加约 67 ns |

这些不是 live 改善百分比或统计意义上的零开销结论。最终二进制重验的是 100 ms 行；0/5 ms 行来自前一相同设计候选，不冒充 final 全矩阵。测试可执行文件增大约 1.43 MB，尚无完整生产 RSS/allocations 结论。所有适用 CI、特权 FUSE/BPF 测试、不可用路径和独立盲审已通过，但本次 live 优化资格仍未闭环。

## 当前问题与未完成事项

### 恢复时优先处理

1. 完成暂停的最新 File 变更聚焦资格：原始 Bash 写入/原生 Read、增加的原生 Edit→Read、精确文件字节/Artifact/HTTP、原始取消副作用断言和完整清理。活跃 Session 需确认实际 BPF 已加载，最终 View 释放后资源回收；未启用 BPF 的功能通过不能当优化 live 资格。无需重跑代码未变的三 Harness 完整 micro 生命周期。
2. 严重远程 metadata 串行延迟仍未闭环。100 ms fixture 下 Bash 仍约 25 秒；98 次 Walk 的服务时间区间并集约 9.823 秒，剩余 Lookup 有大量重复。现有数据不能把全部重复归因于跨 syscall，更不能宣布不可优化的下限。下一步要以当前候选逐调用归属、实际使用模式和可复现收益选方案，不能直接加弱一致性 TTL。
3. 通用命令执行仍有覆盖缺口。shell 内启动的 Python/编译器/脚本已经在 sandbox；Harness 直接启动任意其他程序的情况尚无完整通用方案。PATH 快照/rehash 无法覆盖动态安装、直接路径及发现语义，不等于用户目标。当前没有采用 exec supervisor 或修改相关生产协议。

### 已知限制与明确暂缓

- 官方 API/native 功能差异以 coverage ledger 为准，不能因本轮暂停抹除：native Item/stream 覆盖、部分 feature 组合、Usage、网络策略、平台、恢复和副作用等均有声明边界。
- Claude 结构化输出的极端精确数值 schema 一致性未资格，binary64 相关 corner 用户允许按低 ROI 保留；不另建验证框架。
- Codex 已活跃子 Turn 被另一 root Turn steering 后的取消归属 corner 未资格；不把已通过的普通强取消扩张到它。
- Linux arm64 安装、macOS/Windows 原生 Sandbox I/O、完整跨平台 live 资格未纳入本轮。
- 当前服务不提供 native 外部副作用 crash-safe/exactly-once 保证，不能套用 Tetral 自研 reducer 的恢复承诺。已有 native ID 的冷续接与尚未记录 ID 的 started Turn 恢复必须区分。
- 测试环境依赖现有 Mac 跳板和内网开发机及临时入口，延迟高且入口会失效。用户接受继续使用现有环境，不接受针对这个网络写产品特调。恢复时先核对现存入口和资源，不照抄旧地址，也不把网络 RTT 算成 sandbox 命令计算时间。

### 历史记录需核对的事项

这些条目在旧计划中没有找到明确关闭证据，**不等于已经确认仍存在的当前缺陷**；恢复时对照代码和后续证据判断，不重开整条旧 lane。

- M1：forker thread capability cut 的验证，以及 stuck spawn 时 launcher crash 的发现时机。
- 多 host：初始化可能 sticky 绑定到声明支持但当前不可用的 Harness host 并 typed failure；不要擅自改成隐式换实现或重放。
- 多 Core：embedded relay 的 Serve/Attach 同实例约束；multi-Core 资格尚未建立。
- 架构审计：E2B wizard 按声明渲染字段原先暂缓；cutover 后 daemon 的 `OAC_RUNTIME_*`、布局常量和 binpath fallback 复核需对照后续完成记录。
- Store：旧计划提出 Provider 副作用的“租约内持久意图 + generation/取消 fence”核对。租约连接上的一次读本身不证明陈旧 owner 已被阻止；需对照现行生命周期实现，不能用普通 happy-path 验收关闭。
- BE-8 服务端 Agent 监控汇总仍暂停。现有浏览器汇总有 200 Sessions、4 并发、10 Turn 页、5 Item 页和 45 秒预算，不宣称部署全量监控。
- 最终合 main 时需重新核对旧计划的 wire version 合入规则；本次没有 main 合并或 version bump。

附加不可回退的已有约束：Provider authored unsupported reason 保持开放安全码，与 unavailable 区分；实际镜像和二进制身份、禁止跨 release 原地升级的 guard 与有效数据不因清理而重写；Core 分发可用性从现有制品重查，不永久缓存 provider 集。持久层由领域拥有规则、Postgres adapter 拥有加密，事务参与者不自行提交或调用 Provider，Worker 排空后关闭租约。安装器保留端口占用检查、80/443 显式失败、清理恢复、权限、checksum/token 和中断保护；最后 unlink 间 SIGKILL/断电可能仍需人工清理。

## 研究结论与未来规划

以下是待决策方向，**不是已经批准的实现清单**。

- 优先继续改善公共 File/Process/Network 的操作粒度和往返数，边界确实无法表达当前需求时才做最小协议变更。通用文件性能应覆盖第三方 native Harness，而不是只优化自写工具。
- E2B envd、OpenSandbox、Daytona 的完整路径文件操作、批次与 process 流值得参考；OAC 已有复用 Link，不再增加同类 transport。OpenHands remote workspace 常把 agent 放到远端，不能视为本项目 loop/executor 分离问题的现成解。
- gVisor Shared WalkStat 提供完整路径参考，但整个 View 替换尚有锁竞争与生命周期成本。观察事件不等于一致性 lease；不能用 watcher 丢事件风险掩盖 cache 正确性。
- Reverie 可参考选择性 syscall tracing 和 process owner，但不是现成跨机执行器。其 Rust nightly/Tokio/libunwind 分发成本、与现有 Wait4 owner 冲突，以及 exec errno/FD/discovery/remote Start 原子性都未解决，未采纳依赖。
- Codex native remote Environment、Mcode workspace worker、Claude SDK tool 接口仅作为能力与成本比较；不据此默默引入三套执行后端。若不能用简单公共机制满足透明性，整理可验证的支持边界、收益和成本交用户讨论。
- M3 的 Volume Provider、真正的 file/compute lifetime 分离与 lazy compute 尚未实现，已有 snapshot 生命周期通过不等于 M3 完成。更多平台/Provider、进一步恢复能力与独立扩缩容可以继续规划；没有当前调用者的不先建扩展点。

## 暂停现场与恢复顺序

用户暂停后，已停止 detached chain/runner 后续调度，未进入新增 Edit 或原始取消验收用例。对当时的 Session 执行取消和公共 API 清理：Session 已删除、两个 Project 为空；Core release、native SDK 精确 Compute/Snapshot absence、node allocations、View/cgroup 和 daemon-only 检查全部通过，observer/chain/runner 均停止，daemon 的 BPF program/map fd 为空。已部署候选 `79486373` 保留，不回滚、不发布。该清理证明暂停安全完成，不代表新的原生文件优化资格通过。

恢复时：先读本文件和 ledger → 核对用户是否恢复探索及范围 → 检查实际 branch/source/部署和资源状态 → 复用现有已通过证据与未变制品 → 从上述聚焦未完成项继续。只有实际失败所影响的部分重验。主链路功能闭环和性能达到可接受范围是两件事，不互相替代。

## 证据与历史保存

本文整合本机 `~/.parsar/plans` 中本项目计划、整改协作线、brief、qualification/research 和历史交接的现行结论；旧计划不再作为并行进度入口。历史原文保存在本机归档 `~/.oac/archives/aos-pause-20261010/plans-before.tar.gz`。分支清理前全部本地/远端引用已保存为 `branches-before.bundle`，并通过 `git bundle verify`；同目录保存 refs、worktree 状态与当时 open PR 清单。它们是恢复材料，不是新的活跃计划。

原始 live/fixture/advisor 证据保留在本机 `~/.oac/tests/aos-handoff-20261008/dependencies/`，包含：

- `micro-lifecycle-claude-d56e8e31-purpose-02-preparation/REPORT.md`：完整 Claude micro 生命周期和普通 Bash 取消。
- `full-composition-mcode-micro-db30cac5-baseline-01/REPORT.md`：Mcode 组合资格。
- `claude-tool-cancel-advisory/metadata-bridge/DELIVERY.md`：最新 metadata fixture 收益、成本和限制。
- `unified-command-execution/`：原生接口、社区项目与 advisor 研究；没有相应生产实现。

原始证据和临时脚本可能包含私有测试信息，故不整包提交 Git。未提交诊断代码保留原 worktree；清理分支时只解除分支关联，不 reset、clean 或删除其文件。未合入的无关历史分支也不混入本分支实现，仍可从本机 bundle 恢复。
