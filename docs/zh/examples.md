---
title: "示例"
source: docs/examples.md
source_hash: 4bd0d6d3d382f7a6e3ef666c204528cb99c21172b8cebb8694c83a16d48456c6
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

## RL 与评测 rollout {#rl-and-evaluation-rollouts}

一个 Python 脚本，把任务作为托管 Session 批量运行，可使用任意 Harness 和模型，并在沙箱外为每个答案评分。它展示 Core 作为 RL 训练与评测的 rollout 引擎。每次 rollout 是一个无网络的 Session，任务文件作为初始文件，答案作为 Artifact。

源码：[`example/rl-eval`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/README.md)。

按照[示例 README](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/README.md#run)运行和配置；该文档也定义其输出格式、任务格式和训练接入方式。

### 阅读重点 {#what-to-look-at-1}

| 功能 | 使用的 API | 指南 | 代码 |
| --- | --- | --- | --- |
| 每次 rollout 一个 Session | 内联 `input`、`metadata` 和 `openai_hosted` | [创建 Session](api/public-agent-api.md#create-a-session) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| 无网络的任务文件 | 内联 `files` 和 `network.access: disabled` | [初始文件](../../contracts/agents-api/zh/environments.md#initial-files) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| Harness 与模型矩阵 | `x_agents_core.harness`、`model`、`model_provider` | [Core 扩展](api/public-agent-api.md#core-extensions) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| 等待、超时与取消 | 轮询 Turn 列表、`agent.session.input.cancel` | [取消](api/public-agent-api.md#cancel) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| 沙箱外取得答案 | `outputs/` 下的 Artifacts | [Artifacts](api/public-agent-api.md#artifacts) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| 轨迹与用量 | Items 和 Turn `usage` | [Turns 和 Items](api/public-agent-api.md#turns-and-items) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |

要贡献示例，请遵循[应用示例边界](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#optional-application-example)。
