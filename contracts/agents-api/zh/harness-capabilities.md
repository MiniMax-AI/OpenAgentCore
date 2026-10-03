---
title: "Harness 能力"
source: contracts/agents-api/harness-capabilities.md
source_hash: e1ffc7a260ceaec64ba377f7c0db28f2c371c9d664098b110b740cc110506506
---

本页列出每个 Harness 在每种部署位置支持的能力。Core 根据 `services/core/internal/engine` 中 Harness 的引擎配置决定准入，运行 Session 的 Runtime 也必须声明操作。所链接契约定义各操作；[Harness 接入](harness-onboarding.md#qualify-the-adapter)说明资格验证方法。

| 状态 | 含义 |
| --- | --- |
| 已验证 | Core 允许准入，且该位置通过固定版本官方客户端的真实模型验收 |
| 已准入 | Core 通过相同 Runtime 路径允许准入，但该位置尚未运行真实模型验收 |
| 已拒绝 | Core 在执行前拒绝请求 |

部署位置为 `none`（无 Environment）、托管（`openai_hosted`）和自托管（`self_hosted`）。托管验收在 Docker 节点运行；E2B、microsandbox 使用相同 Runtime 与适配器，托管位置的每个已验证单元格在这两者中视为已准入。自托管验收在 Linux 机器运行；[自托管指南](../../../docs/zh/getting-started/self-hosted.md#platforms)列出支持平台。操作通过验证仅代表该操作本身，不代表与其他选项的所有组合；配置拒绝的组合列于对应操作契约。

## 执行与输入 {#execution-and-input}

| 操作 | Codex | Claude SDK | MiniMax Code |
| --- | --- | --- | --- |
| 文本 Turn、活动输入、取消、重启与继续 | 所有位置已验证 | 所有位置已验证 | 所有位置已验证 |
| [文件与 Artifact](environment-files.md) | 已验证：托管、自托管 | 已验证：托管、自托管 | 已验证：托管、自托管 |
| [仅空白消息文本](message-content.md) | 已准入；原样交付 | 已拒绝 | 已拒绝 |
| [内联 PNG、JPEG 消息图像](message-content.md) | 所有位置已验证 | 所有位置已验证 | 已拒绝 |
| 远程图像 URL | 已拒绝 | 已拒绝 | 已拒绝 |
| 显式 `reasoning`；非 `auto` 的 `service_tier` | 已拒绝 | 已拒绝 | 已拒绝 |
| 非 `medium` 的 `text.verbosity` | 已准入；由原生模型决定 | 已拒绝 | 已拒绝 |
| [公开 token 用量](sessions-events.md) | 测量计数器 | Null | Null |

各 Harness 原生模型参数及提供方协议见[模型执行](model-execution.md)。

## 工具 {#tools}

| 操作 | Codex | Claude SDK | MiniMax Code |
| --- | --- | --- | --- |
| 文本结果的[公开函数](execution-tools.md#functions) | 已验证：`none`、托管；已准入：自托管 | 所有位置已验证；仅对象根 schema | 已拒绝 |
| [含图像函数结果](message-content.md#function-results) | 已验证：`none`、托管；已准入：自托管 | 所有位置已验证；仅成功结果中的内联 PNG 或 JPEG | 已拒绝 |
| [结构化输出](execution-tools.md#structured-output) | 已拒绝 | 所有位置已验证 | 已拒绝 |
| [延迟函数发现](execution-tools.md#deferred-function-discovery) | 已拒绝 | 已验证：`none`、自托管；已准入：托管 | 已拒绝 |
| [禁用 Web 搜索与程序化工具调用](execution-tools.md#web-search-and-programmatic-tool-calling) | 已验证：`none`；已准入：托管、自托管 | 已验证：`none`；已准入：托管、自托管 | 已验证：`none`；已准入：托管、自托管 |
| 启用 Web 搜索或程序化工具调用 | 已拒绝 | 已拒绝 | 已拒绝 |
| [服务端来源 HTTP MCP](environments.md#public-mcp-connection-origin) | 已验证：`none`；其他位置拒绝 | 已验证：`none`；其他位置拒绝 | 已拒绝 |
| [Environment 来源 HTTP MCP](environments.md#public-mcp-connection-origin) | 已验证：托管、自托管 | 已验证：托管、自托管 | 已验证：托管、自托管；仅 `allowed_tools` null、`required` false |
| [HTTP MCP bearer 凭据](execution-tools.md#http-mcp) | 已验证 | 已验证 | 已验证 |
| 必需 MCP 初始化 | 已验证 | 已验证 | 已拒绝 |
| [Subagent](subagents.md) | 已验证：托管；已准入：`none`、自托管 | 已验证：托管；已准入：`none`、自托管 | 已验证：托管；已准入：`none`、自托管 |
| Subagent 与函数或 HTTP MCP 同时使用 | 已拒绝 | 已拒绝 | 已拒绝 |

Claude 结构化输出要求单 Agent、medium verbosity，且无 Skill、Plugin、能力目录、MCP 或 `tool_search`。Claude 延迟发现要求单 Agent，除 `tool_search` 与禁用控制项外仅函数工具，无 Skill、Plugin 或能力目录，也无结构化输出。Claude 在工作区位置需要每种操作的打包桥接功能（`workspace_functions`、`workspace_structured_output`、`workspace_tool_search`、`workspace_mcp_http`）。

## Environment 准备 {#environment-preparation}

这些操作要求工作区，因此仅适用于托管与自托管位置。

| 操作 | Codex | Claude SDK | MiniMax Code |
| --- | --- | --- | --- |
| [初始文件、设置命令、Skill 与 Plugin](environments.md#runtime-capability-preparation) | 已验证：托管、自托管 | 已验证：自托管；已准入：托管 | 已验证：自托管；已准入：托管 |
| 能力目录 | 已准入 | 已准入 | 已准入 |
| npm 与 Python 包 | 已准入 | 已准入 | 已准入 |
| `packages.system` | 已拒绝 | 已拒绝 | 已拒绝 |
| 网络 `disabled` 或 `restricted` | 已拒绝 | 已拒绝 | 已拒绝 |
| [stdio Plugin MCP](environments.md#plugin-mcp) | 已验证：托管、自托管 | 已验证：自托管；已准入：托管 | 已验证：自托管；已准入：托管 |
| HTTP Plugin MCP | 已准入，字面值头或 HTTPS bearer | 已准入，匿名或 HTTPS bearer | 已验证：自托管；已准入：托管；匿名或 HTTPS bearer |
