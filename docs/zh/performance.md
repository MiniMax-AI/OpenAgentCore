---
title: "性能"
source: docs/performance.md
source_hash: b51b7215dae5afb717ad4f8705cef4da438a3d24ff6b2e4be8a68fe1fc97ccfc
---

这些测量覆盖 microsandbox 上的沙箱生命周期：启动沙箱、准备 Environment、空闲时挂起以及恢复。E2B 的创建、暂停和恢复用其 SDK 测量作为对照，两者的差异见 [E2B](#e2b)。本页每个数字都由 [`scripts/benchmark.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/scripts/benchmark.py) 根据同目录发布的[原始记录](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/docs/performance)生成。

## 结果 {#results}

| Measurement | Median | Range | n |
| --- | --- | --- | --- |
| Sandbox create | 1.22 s | 1.18–1.37 s | 30 |
| Sandbox created → initialization start | 0.98 s | 0.75–1.48 s | 20 |
| Session created → environment ready, preparation cache miss | 7.35 s | 5.90–16.20 s | 10 |
| npm install, preparation cache miss | 4.67 s | 3.09–13.54 s | 10 |
| Session created → environment ready, preparation cache hit | 2.50 s | 2.30–2.70 s | 10 |
| Session created → environment ready, free slot | 2.50 s | 2.10–2.60 s | 3 |
| Session created → environment ready, full node | 5.90 s | 5.50–15.40 s | 7 |
| Suspend | 3.33 s | 2.88–4.53 s | 10 |
| Resume → sandbox running | 1.75 s | 1.16–4.70 s | 10 |
| Turn first text, resumed Session | 3.51 s | 2.11–6.91 s | 10 |
| Turn first text, running Session | 1.26 s | 1.11–2.31 s | 10 |
| E2B create | 0.60 s | 0.54–1.51 s | 10 |
| E2B pause | 1.04 s | 0.69–1.41 s | 10 |
| E2B resume → first command | 0.87 s | 0.58–31.10 s | 10 |
| E2B API round trip | 0.20 s | 0.19–0.21 s | 10 |

第二轮在所有 Session 挂起后清空了节点的页缓存，因此每次恢复都从磁盘读取检查点：

| Measurement | Median | Range | n |
| --- | --- | --- | --- |
| Resume → sandbox running | 1.99 s | 1.69–5.26 s | 10 |
| Turn first text, resumed Session | 3.11 s | 2.71–8.41 s | 10 |
| Turn first text, running Session | 1.26 s | 0.71–2.81 s | 10 |

## 各项测量的范围 {#what-each-measurement-covers}

### 沙箱启动 {#sandbox-start}

- **Sandbox create**：Core 的 `create` 步骤，从调用 Provider 到沙箱启动完成。
- **Sandbox created → initialization start**：从 `create` 结束到 Core 开始准备 Environment，包括 Runtime 启动、连接 Core，以及 Core 认领初始化。Core 只为带文件或初始化工作的 Environment 记录这一起点，因此这一行覆盖下文的冷启动 Session。

### Environment 就绪 {#environment-ready}

**Session created → environment ready** 从 `agent.session.created` 计到 `agent.session.environment.ready`，以 Core 主机上的应用在事件流上收到这两个事件的时间为准，因此包含排队、沙箱启动和准备。Core 以 100 ms 为间隔推送事件流。

每个冷启动样本使用一个新的 Template，含一个 npm 包（`prettier@3.3.3`）、一个文件和一条初始化命令。它的第一个 Session 未命中[准备缓存](../../contracts/agents-api/zh/environments.md#preparation-reuse)，需要完整安装；**npm install** 是这次安装中由 Runtime 计时的 `npm` 步骤，耗时取决于包仓库。第二个 Session 恢复已准备好的结果。

**Free slot** 和 **full node** 覆盖不带 Template、依次启动的 Session。最先的几个 Session 有空闲槽位。节点运行的沙箱达到上限后，每个 Session 都要等 Core [挂起](./sandbox-provider.md#suspension)一个空闲沙箱；Core 在该沙箱连续 15 s 无活动后才会挂起它。

### 挂起与恢复 {#suspend-and-resume}

- **Suspend**：Core 的 `suspend` 步骤。节点把沙箱的内存和磁盘捕获为检查点，然后停止沙箱。Session 在空闲超时后挂起；新 Session 需要它的槽位时会更早挂起。
- **Resume → sandbox running**：从 Core 的 `resume` 步骤开始，到沙箱重新运行为止，包括恢复检查点、唤醒 Runtime、确认 Environment 已恢复以及删除检查点。第一轮不改动节点的页缓存。
- **Turn first text**：从发送输入到第一个输出文本增量，与 Environment 就绪一样由应用测量，分别测恢复已挂起 Session 的 Turn 和同一 Session 运行中的下一个 Turn。两者之差是恢复带来的等待，其余是模型耗时。

### E2B {#e2b}

E2B 各行是客户端上 SDK 调用的耗时：`Sandbox.create`、`pause`，以及恢复时的 `connect` 加一条命令。每次调用至少包含一次客户端到 API 的往返，即 **E2B API round trip**，在保持连接的情况下测得。两边的沙箱都有 2 个 CPU 和 4 GiB 内存，并在相同的空闲等待后恢复。两者的差异在于：

- **计时位置**：E2B 各行在客户端计时。OpenAgentCore 的沙箱各行来自 Core 日志，Core 经 0.2 ms 的网络访问节点。
- **负载**：暂停前，每个 E2B 沙箱在一个进程中占用 650 MiB 内存并向磁盘写入 100 MiB。每个 OpenAgentCore 沙箱运行过一次 Codex Turn。
- **恢复的终点**：E2B 在第一条命令返回时结束。OpenAgentCore 在 Runtime 重新连接且 Environment 已恢复时结束。

## 测试环境 {#setup}

- **版本**：Core 为 `c5d4205d`；沙箱节点和 Runtime 为 `170a4983`，其后的改动不涉及这些步骤。测于 2026-10-25。
- **主机**：Core 和一个 microsandbox 节点分别运行在两台 Intel Xeon 6982P-C 云虚拟机上，系统为 Ubuntu 22.04、Linux 5.15，相互延迟 0.2 ms。Core 有 8 个 vCPU 和 62 GiB 内存；节点有 4 个 vCPU 和 15 GiB 内存，最多同时运行 3 个沙箱、保留 14 个。
- **沙箱**：每个 2 个 CPU、4 GiB 内存和 8 GiB 根磁盘，工作区位于 NFSv4.2。空闲 300 秒后挂起。
- **Session**：Codex，模型为 `https://api.minimaxi.com/v1` 上的 MiniMax-M2.7，在 Core 主机上用 `openai` 3.13.0 驱动，每个阶段 10 个样本。
- **E2B**：基于同一 Runtime 镜像构建的模板，2 个 vCPU、4 GiB 内存，在距 API 0.20 s 的客户端上用 `e2b` 2.51.0 驱动，共 10 个沙箱。

## 复现 {#reproduce}

在没有其他活动的安装上运行：报告会读取运行期间 Core 记录的所有生命周期步骤。

1. 按[安装 Core 和 Web](./getting-started/install.md)安装 OpenAgentCore，添加带工作区存储的 microsandbox 节点，设置默认模型并签发 Project API key。
2. 在 Core 主机上，用 Python 3.10 或更高版本执行 `pip install openai==3.13.0`，设置 `OPENAI_BASE_URL` 和 `OPENAI_API_KEY` 后运行 Session 测量。约需 15 分钟，其中 7 分钟在等待空闲超时：

   ```sh
   python3 scripts/benchmark.py sessions results --model MiniMax-M2.7 --harness codex
   ```

3. 保存 Core 日志并生成表格。报告会把用到的日志行保存在 `results/core.jsonl`：

   ```sh
   docker compose -f ~/.oac/core/compose.yaml logs --no-log-prefix core > core.log
   python3 scripts/benchmark.py report results core.log
   ```

4. 要测量从磁盘恢复，把第 2 步的输出目录换成 `results/cold` 再运行一次。在它打印 `waiting 420 s for the idle timeout` 约 6 分钟后、所有 Session 都已挂起时，在节点上执行 `sync; echo 3 | sudo tee /proc/sys/vm/drop_caches` 清空页缓存。然后对 `results/cold` 重复第 3 步。
5. 测量 E2B 时，执行 `pip install e2b==2.51.0`，设置 `E2B_API_KEY`，并提供一个基于 Runtime 镜像、内存为 4 GiB 的模板，然后再次运行 `report`：

   ```sh
   python3 scripts/benchmark.py e2b results --template <template>
   python3 scripts/benchmark.py report results
   ```

## 原始记录 {#records}

[原始记录](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/docs/performance)就是上面两张表的输入；`python3 scripts/benchmark.py report docs/performance` 会重新打印第一张表，`report docs/performance/cold` 打印第二张。

| 文件 | 内容 |
| --- | --- |
| `sessions.jsonl` | 每个 Turn 一行：`phase`（`cold`、`start`、`resume` 或 `warm`）、`sample`、冷启动的 `cache`、Session ID、`start` 阶段的 `created`（Core 的创建时间，Unix 秒），以及每种事件类型在请求发出后首次到达的秒数 |
| `core.jsonl` | 报告读取的本次运行 Core 日志行：生命周期步骤及其耗时、阶段变化、准备缓存结果和初始化步骤，allocation ID 截短为 8 个字符 |
| `window.json` | 本次运行在 Core 时钟上的起止时间，单位为 Unix 秒 |
| `e2b.jsonl` | 每个 E2B 沙箱一行，含创建、暂停和恢复耗时；每次 API 往返一行 |
| `cold/` | 从磁盘读取检查点那一轮的 `sessions.jsonl`、`core.jsonl` 和 `window.json` |
