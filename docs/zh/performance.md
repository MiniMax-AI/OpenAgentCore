---
title: "性能"
source: docs/performance.md
source_hash: da14f0b9de75af9b224d67ff69084edd7572090018b5356e56da623355bca30c
---

这些测量覆盖 microsandbox 上的沙箱生命周期：启动沙箱、准备 Environment、空闲时挂起以及恢复；E2B 以同样方式测量作为对照。本页每个数字都由 [`scripts/benchmark.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/scripts/benchmark.py) 根据同目录发布的[原始记录](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/docs/performance)生成。

## 结果 {#results}

| Measurement | Median | Range | n |
| --- | --- | --- | --- |
| Sandbox create | 1.20 s | 1.16–1.22 s | 30 |
| Sandbox created → initialization start | 0.80 s | 0.76–1.07 s | 20 |
| Session created → environment ready, preparation cache miss | 8.15 s | 5.90–18.90 s | 10 |
| Session created → environment ready, preparation cache hit | 2.30 s | 2.30–2.60 s | 10 |
| Suspend | 2.92 s | 2.87–2.97 s | 10 |
| Resume → sandbox running | 1.24 s | 1.13–1.29 s | 10 |
| Turn first text, resumed Session | 3.06 s | 2.11–7.81 s | 10 |
| Turn first text, running Session | 1.51 s | 1.01–5.41 s | 10 |
| E2B create | 0.59 s | 0.28–1.68 s | 10 |
| E2B pause | 0.60 s | 0.49–0.87 s | 10 |
| E2B resume → first command | 0.75 s | 0.52–1.99 s | 10 |
| E2B API round trip | 0.20 s | 0.19–0.23 s | 5 |

## 各项测量的范围 {#what-each-measurement-covers}

### 沙箱启动 {#sandbox-start}

- **Sandbox create**：Core 的 `create` 步骤，从调用 Provider 到沙箱启动完成。
- **Sandbox created → initialization start**：从 `create` 结束到 Core 开始准备 Environment，包括 Runtime 启动、连接 Core，以及 Core 认领初始化。

### Environment 就绪 {#environment-ready}

**Session created → environment ready** 从应用事件流上的 `agent.session.created` 计到 `agent.session.environment.ready`，因此包含排队、沙箱启动和准备。每个样本使用一个新的 Template，含一个 npm 包（`prettier@3.3.3`）、一个文件和一条初始化命令。它的第一个 Session 未命中[准备缓存](../../contracts/agents-api/zh/environments.md#preparation-reuse)，需要完整安装；第二个 Session 恢复已准备好的结果。恢复和保存准备结果都不到 0.4 秒，因此未命中时大部分时间用于安装包，耗时取决于包仓库。

### 挂起与恢复 {#suspend-and-resume}

- **Suspend**：Core 的 `suspend` 步骤。节点把沙箱的内存和磁盘捕获为检查点，然后停止沙箱。Session 在空闲超时后挂起；新 Session 需要它的槽位时会更早挂起。
- **Resume → sandbox running**：从 Core 的 `resume` 步骤开始，到沙箱重新运行为止，包括恢复检查点、唤醒 Runtime 并确认 Environment 已恢复。
- **Turn first text**：从发送输入到第一个输出文本增量，分别测量恢复已挂起 Session 的 Turn 和同一 Session 运行中的下一个 Turn。两者之差是恢复带来的等待，其余是模型耗时。

### E2B {#e2b}

E2B 各行是客户端上 SDK 调用的耗时：`Sandbox.create`、`pause`，以及恢复时的 `connect` 加一条命令。暂停前，每个沙箱在一个进程中占用 650 MiB 内存并向磁盘写入 100 MiB，接近一次 Harness Turn 之后的沙箱；每个沙箱在与 Session 相同的空闲等待后恢复。每次调用至少包含一次客户端到 API 的往返，即 **E2B API round trip**，在保持连接的情况下测得。OpenAgentCore 各行由 Core 自己测量，Core 通过本地网络访问节点。

## 测试环境 {#setup}

- **版本**：Core 为 `c5d4205d`；沙箱节点和 Runtime 为 `170a4983`，其后的改动不涉及这些步骤。测于 2026-10-25。
- **主机**：Core 和一个 microsandbox 节点分别运行在两台 Intel Xeon 6982P-C 云虚拟机上，系统为 Ubuntu 22.04、Linux 5.15，相互延迟 0.2 ms。Core 有 8 个 vCPU 和 62 GiB 内存；节点有 4 个 vCPU 和 15 GiB 内存，最多同时运行 3 个沙箱、保留 14 个。
- **沙箱**：每个 2 个 CPU、4 GiB 内存和 8 GiB 根磁盘，工作区位于 NFSv4.2。空闲 300 秒后挂起。
- **Session**：Codex，模型为 `https://api.minimaxi.com/v1` 上的 MiniMax-M2.7，在 Core 主机上用 `openai` 3.13.0 驱动，每个阶段 10 个样本。
- **E2B**：基于同一 Runtime 镜像构建的模板，2 个 vCPU、2 GiB 内存，在距 API 0.20 s 的客户端上用 `e2b` 2.51.0 驱动，共 10 个沙箱。

## 复现 {#reproduce}

在没有其他活动的安装上运行：报告会读取运行期间 Core 记录的所有生命周期步骤。

1. 按[安装 Core 和 Web](./getting-started/install.md)安装 OpenAgentCore，添加带工作区存储的 microsandbox 节点，设置默认模型并签发 Project API key。
2. 在任一能访问 API 的主机上，用 Python 3.10 或更高版本执行 `pip install openai==3.13.0`，设置 `OPENAI_BASE_URL` 和 `OPENAI_API_KEY` 后运行 Session 测量。约需 15 分钟，其中一半时间在等待空闲超时：

   ```sh
   python3 scripts/benchmark.py sessions results --model MiniMax-M2.7 --harness codex
   ```

3. 在 Core 主机上保存 Core 日志并生成表格。报告会把用到的日志行保存在 `results/core.jsonl`：

   ```sh
   docker compose -f ~/.oac/core/compose.yaml logs --no-log-prefix core > core.log
   python3 scripts/benchmark.py report results core.log
   ```

4. 测量 E2B 时，执行 `pip install e2b`，设置 `E2B_API_KEY`，并提供一个内存至少 2 GiB 的模板，然后再次运行 `report`：

   ```sh
   python3 scripts/benchmark.py e2b results --template <template>
   python3 scripts/benchmark.py report results
   ```

## 原始记录 {#records}

[原始记录](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/docs/performance)就是上表的输入；`python3 scripts/benchmark.py report docs/performance` 会重新打印该表。

| 文件 | 内容 |
| --- | --- |
| `sessions.jsonl` | 每个 Turn 一行：`phase`（`cold`、`start`、`resume` 或 `warm`）、`sample`、冷启动的 `cache`、Session ID，以及每种事件类型在请求发出后首次到达的秒数 |
| `core.jsonl` | 报告读取的本次运行 Core 日志行：生命周期步骤及其耗时、阶段变化和准备缓存结果，allocation ID 截短为 8 个字符 |
| `window.json` | 本次运行在 Core 时钟上的起止时间，单位为 Unix 秒 |
| `e2b.jsonl` | 每个 E2B 沙箱一行，含创建、暂停和恢复耗时；每次 API 往返一行 |
