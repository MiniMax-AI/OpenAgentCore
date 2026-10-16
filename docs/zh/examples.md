---
title: "示例"
source: docs/examples.md
source_hash: 55efc4906a38e5bd9e65aef60f686f17c57f75c0e7356e5f6549b436d5e92137
---

基于 [Agents API](api/public-agent-api.md)构建的完整应用。每个示例都使用项目 API 密钥连接真实的 Core 安装。

| 示例 | 展示内容 |
| --- | --- |
| [Parsar Agent 工作台](#parsar-agent-workbench) | 项目、任务看板、可复用 Agent、执行历史、定时任务和飞书分配 |

## Parsar Agent 工作台 {#parsar-agent-workbench}

基于 Core 构建的任务工作空间，通过本地 Web UI 和受信任的飞书群操作。组织项目，将任务分配给可复用 Agent，继续执行 Session，检查 Artifacts 并记录交付进度。间隔计划和飞书分配复用公开 Session 流程。执行使用 bypass 模式；不包含产品账号和细粒度权限。

源码：[`example/parsar`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/README.md)。

按照[示例 README](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/README.md#run)运行和配置；该文档也定义其支持的功能、存储和验证方式。

### 阅读重点 {#what-to-look-at}

每项功能对应 API 的一个部分。结合指南章节阅读代码：

| 功能 | 使用的 API | 指南 | 代码 |
| --- | --- | --- | --- |
| 任务分配与恢复 | 公开 Session 创建和持久历史 | [创建 Session](api/public-agent-api.md#create-a-session) | [`server/work.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/work.mjs) |
| 飞书和定时任务 | 公开 Sessions、输入事件、Turns 和 Items | [发送输入](api/public-agent-api.md#send-input) | [`server/feishu.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/feishu.mjs)、[`server/schedules.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/schedules.mjs) |
| 服务端密钥和请求头 | Bearer 密钥、`OpenAI-Beta`、路由允许列表 | [开始前](api/public-agent-api.md#before-you-start) | [`server.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server.mjs) |
| 带 Harness 和模型的 Agent | `x_agents_core.harness`、`model_provider` | [Core 扩展](api/public-agent-api.md#core-extensions-x-agents-core) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs) |
| 三种 Runtime 上的 Session | `openai_hosted`、`none`、带 `workspace_directory` 的 `self_hosted` | [创建 Session](api/public-agent-api.md#create-a-session) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs) |
| 可在崩溃后恢复的 Session 创建 | `Idempotency-Key`、响应丢失后的恢复 | [幂等性](api/public-agent-api.md#idempotency) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs) |
| 实时回复 | 事件流、文本增量、重连与状态协调 | [流式事件](api/public-agent-api.md#stream-events) | [`src/lib/live-session.ts`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/lib/live-session.ts) |
| 后续输入与取消 | 带幂等键的输入事件 | [发送输入](api/public-agent-api.md#send-input) | [`src/Composer.tsx`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/Composer.tsx) |
| 历史 | 分页读取 Turns 和 Items | [分页](api/public-agent-api.md#pagination) | [`src/lib/api.ts`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/lib/api.ts) |
| Skills 和版本 | `/skills`、默认版本 | [Skills](api/public-agent-api.md#skills) | [`src/Skills.tsx`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/Skills.tsx) |
| 连接自己的机器 | `x_agents_core.installation`、Environment 状态 | [自托管执行](getting-started/self-hosted.md) | [`src/ConnectMachine.tsx`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/ConnectMachine.tsx) |

## 添加示例 {#add-an-example}

在 `example/` 下创建目录，用 README 说明运行方式及展示的 API 功能，然后在本索引添加一行。遵循[应用示例边界](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#optional-application-example)。
