---
title: "Harness 能力"
source: contracts/agents-api/harness-capabilities.md
source_hash: 8913afc18b99b847850a857e387e1a230085525ce5f1b28abd5a440abc9dcf16
---

本页列出每个 Harness 在每种部署位置支持的能力。Core 根据 `internal/harnessconfig/<package>/declaration.go` 中 Harness 的声明决定准入，运行 Session 的 Runtime 也必须声明操作。所链接契约定义各操作；[Harness 接入](harness-onboarding.md#qualify-the-adapter)说明资格验证方法。

本页未列出的行为遵循 Harness 在所选模型下的原生行为，与本地运行一致。每个适配器指南列出它为云端无人值守执行而覆盖的原生默认值（[设计规则](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#core-assembles-harnesses-run-natively)）。

| 状态 | 含义 |
| --- | --- |
| 已验证 | Core 允许准入，且该位置通过固定版本官方客户端的真实模型验收 |
| 已准入 | Core 通过相同 Runtime 路径允许准入，但该位置尚未运行真实模型验收 |
| 已拒绝 | Core 在执行前拒绝请求 |

部署位置为 `none`（无 Environment）、托管（`openai_hosted`）和自托管（`self_hosted`）。托管验收在 Docker 节点运行；E2B、microsandbox 使用相同 Runtime 与适配器，托管位置的每个已验证单元格在这两者中视为已准入。自托管验收在 Linux 机器运行；[自托管指南](../../../docs/zh/getting-started/self-hosted.md#platforms)列出支持平台。操作通过验证仅代表该操作本身，不代表与其他选项的所有组合；配置拒绝的组合列于对应操作契约。

## 执行与输入 {#execution-and-input}

| 操作 | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| 文本 Turn、活动输入、取消、重启与继续 | 所有位置已验证 | 所有位置已验证 | 所有位置已验证 |
| [文件与 Artifact](environment-files.md) | 已验证：托管、自托管 | 已验证：托管、自托管 | 已验证：托管、自托管 |
| Agent `instructions` | 已准入 | 已准入 | 最多 32 KiB；更长的 instructions 会被拒绝 |
| [仅空白消息文本](message-content.md) | 已准入；原样交付 | 已准入；原生会丢弃它，全空白消息变为 `(no content)` | 已拒绝 |
| [消息图像](message-content.md#images) | PNG、JPEG data URL：所有位置已验证；其他 data URL：已准入；远程 URL：已拒绝 | PNG、JPEG data URL：所有位置已验证；远程 URL 及 GIF、WebP data URL：已准入 | 已拒绝 |
| `reasoning.effort` | `none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`：已准入 | `low`、`medium`、`high`、`xhigh`、`max`：已准入 | `none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`：已准入 |
| `reasoning.summary` | `concise`、`detailed`、`auto`：已准入 | `auto`：已准入 | 已拒绝 |
| 非 `auto` 的 `service_tier` | `default`、`flex`、`priority`、`fast`：已准入 | 已拒绝 | 已拒绝 |
| 非 `medium` 的 `text.verbosity` | 已准入；由原生模型决定 | 已拒绝 | 已拒绝 |
| [公开 token 用量](sessions-events.md) | 测量计数器 | Null | Null |

各 Harness 原生模型参数及提供商协议见[模型执行](model-execution.md)。

## 工具 {#tools}

| 操作 | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| 文本结果的[公开函数](execution-tools.md#functions) | 已验证：`none`、托管；已准入：自托管 | 所有位置已验证；仅对象根 schema | 已验证：`none`；已准入：托管、自托管；仅对象根 schema；结果须在 120 秒内到达（[差异](index.md#differences-from-openai)） |
| [含图像函数结果](message-content.md#function-results) | 已验证：`none`、托管；已准入：自托管；远程 URL：已拒绝 | 仅成功结果；PNG、JPEG data URL：所有位置已验证；GIF、WebP：已准入 | 已拒绝 |
| [结构化输出](execution-tools.md#structured-output) | 所有位置已准入 | 所有位置已验证 | 已拒绝 |
| [延迟函数发现](execution-tools.md#deferred-function-discovery) | 已拒绝 | 已验证：`none`、自托管；已准入：托管 | 已拒绝 |
| [禁用 Web 搜索与程序化工具调用](execution-tools.md#web-search-and-programmatic-tool-calling) | 已验证：`none`；已准入：托管、自托管 | 已验证：`none`；已准入：托管、自托管 | 已验证：`none`；已准入：托管、自托管 |
| Web 搜索 `cached` 或 `live`，以及程序化工具调用 `enabled` | 所有位置已准入；`enabled` 与 `multi_agent` 同时出现时被拒绝 | `live` 在所有位置已准入；`cached` 和 `enabled` 已拒绝 | 已拒绝 |
| [服务端来源 HTTP MCP](environments.md#public-mcp-connection-origin) | 已验证：`none`；其他位置拒绝 | 已验证：`none`；其他位置拒绝 | 已验证：`none`，仅 `allowed_tools` null、`required` false；其他位置拒绝 |
| [Environment 来源 HTTP MCP](environments.md#public-mcp-connection-origin) | 已验证：托管、自托管 | 已验证：托管、自托管 | 已验证：托管、自托管；仅 `allowed_tools` null、`required` false |
| [stdio MCP](execution-tools.md#mcp) | 已准入：托管、自托管 | 已准入：托管、自托管；`cwd` 须为工作区目录，`allowed_tools` 须为 null | 已拒绝 |
| [HTTP MCP bearer 凭据](execution-tools.md#mcp) | 已验证 | 已验证 | 已验证 |
| 必需 MCP 初始化 | 已验证 | 已验证 | 已拒绝 |
| [Subagent](subagents.md) | 已验证：托管；已准入：`none`、自托管 | 已验证：托管；已准入：`none`、自托管 | 已验证：托管；已准入：`none`、自托管 |
| Subagent 与函数或 HTTP MCP 同时使用 | 已验证：`none`；已准入：托管、自托管 | 已验证：`none` 上的函数；已准入：HTTP MCP、托管、自托管 | 已准入；子级不获得 Session 的函数和 MCP 服务器 |

Claude 结构化输出要求 medium verbosity，可与 `multi_agent`、Skill、Plugin、能力目录、MCP 和 `tool_search` 组合。Claude 延迟发现可与 MCP、Skill、Plugin、能力目录和结构化输出组合。Claude Subagent 继承父级的工具，包括延迟加载的函数。`tool_search` 是否对所配置的模型、提供商和 beta 设置生效，由 Claude Code 决定。Claude 在工作区位置需要每种操作的打包桥接功能（`workspace_functions`、`workspace_structured_output`、`workspace_tool_search`、`workspace_mcp_http`）。

## Environment 准备 {#environment-preparation}

这些操作要求工作区，因此仅适用于托管与自托管位置。

| 操作 | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| [初始文件、设置命令、Skill 与 Plugin](environments.md#runtime-capability-preparation) | 已验证：托管、自托管 | 已验证：自托管；已准入：托管 | 已验证：自托管；已准入：托管 |
| 能力目录 | 已准入 | 已准入 | 已准入 |
| npm 与 Python 包 | 已准入 | 已准入 | 已准入 |
| null 或 `[]` 以外的 `packages.system` | 已拒绝 | 已拒绝 | 已拒绝 |
| 网络 `disabled` 或 `restricted` | 已拒绝 | 已拒绝 | 已拒绝 |
| [stdio Plugin MCP](environments.md#plugin-mcp) | 已验证：托管、自托管 | 已验证：自托管；已准入：托管 | 已验证：自托管；已准入：托管 |
| HTTP Plugin MCP | 已准入，字面值头或 HTTPS bearer | 已准入，字面值头或 HTTPS bearer | 已验证：自托管；已准入：托管；字面值头或 HTTPS bearer |

外部工作区存储还要求所选 Harness 声明和 Runtime 支持 `retained_native_history`；使用自有磁盘的 Docker 和 E2B 不要求此能力。microsandbox 始终使用外部存储，因此未声明该能力的 MiniMax Code 无法在其上运行。[工作区文件系统协议](../../../docs/zh/workspace-provider.md)定义存储组合准入规则。
