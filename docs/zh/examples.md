---
title: "示例"
source: docs/examples.md
source_hash: 99d9e955b20c81fea35ba28915e0bd96619afa94b6fc224b8b7a2a1047cce4a4
---

基于 [Agents API 指南](api/public-agent-api.md)构建的完整应用，每个都使用项目 API 密钥连接真实的 Core 安装。

## Parsar Agent 工作台 {#parsar-agent-workbench}

基于 Core 构建的对话工作空间。从资源库和环境模板组装 Agent，然后在本地网页聊天中对话，或通过以 Agent 身份工作的飞书机器人对话。两个入口都创建普通的公开 Session。

源码：[`example/parsar`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/README.md)。

按照[示例 README](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/README.md#run)运行和配置；该文档也定义其支持的功能、存储和验证方式。

### 阅读重点 {#what-to-look-at}

每项功能对应 API 的一个部分。结合指南章节阅读代码：

| 功能 | 使用的 API | 指南 | 代码 |
| --- | --- | --- | --- |
| 服务端密钥和请求头 | Bearer 密钥、`OpenAI-Beta`、路由允许列表 | [开始前](api/public-agent-api.md#before-you-start) | [`server.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server.mjs) |
| 带 Harness 和模型的 Agent | `x_agents_core.harness`、`model_provider` | [Core 扩展](api/public-agent-api.md#core-extensions) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs) |
| 网页与飞书对话 | 公开 Session 创建、输入事件、Turns 和 Items | [发送输入](api/public-agent-api.md#send-input) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs)、[`server/feishu.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/feishu.mjs) |
| 基于模板的托管 Environment | 带软件包、文件、`env` 和 `setup_commands` 的 `openai_hosted` | [创建 Session](api/public-agent-api.md#create-a-session) | [`server/runtime-profile.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/runtime-profile.mjs) |
| 可在崩溃后恢复的 Session 创建 | `Idempotency-Key`、响应丢失后的恢复 | [幂等性](api/public-agent-api.md#idempotency) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs) |
| 实时回复 | 事件流、文本增量、重连与状态协调 | [流式事件](api/public-agent-api.md#stream-events) | [`src/lib/live-session.ts`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/lib/live-session.ts) |
| 后续输入与取消 | 带幂等键的输入事件 | [发送输入](api/public-agent-api.md#send-input) | [`src/Composer.tsx`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/Composer.tsx) |
| 历史 | 分页读取 Turns 和 Items | [分页](api/public-agent-api.md#pagination) | [`src/lib/api.ts`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/lib/api.ts) |
| Skills 和版本 | `/skills`、默认版本 | [Skills](api/public-agent-api.md#skills) | [`src/Skills.tsx`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/Skills.tsx) |

要贡献示例，请遵循[应用示例边界](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#optional-application-example)。
