---
title: "示例"
source: docs/examples.md
source_hash: 3c10e50ae47479688cdfac708e82efd73bd5b5f77a2e8b16497a4e02cb14573d
---

基于 [Agents API](api/public-agent-api.md)构建的完整应用。每个示例都使用 Project API 密钥连接真实的 Core 安装。

| 示例 | 展示内容 |
| --- | --- |
| [Parsar Agent 工作台](#parsar-agent-workbench) | 产品 UI：模型、Skills、MCP、可复用 Agent，以及在任意 Runtime 上运行的 Session |

## Parsar Agent 工作台 {#parsar-agent-workbench}

基于 Core 构建的小型单用户产品。保存模型提供商、Skills 和 MCP 服务器，将它们组合成 Agent，然后在托管沙箱、无工作区环境或自己的机器上启动 Session。

源码：[`example/parsar`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/README.md)。

按照[示例 README](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/README.md#run)运行和配置；该文档也定义其支持的功能、存储和验证方式。

### 阅读重点 {#what-to-look-at}

每项功能对应 API 的一个部分。结合指南章节阅读代码：

| 功能 | 使用的 API | 指南 | 代码 |
| --- | --- | --- | --- |
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
