---
title: "Harness capabilities"
---

This page lists what each Harness supports on each placement. Core decides admission from the Harness's engine profile in `services/core/internal/engine`, and the Runtime that runs the Session must also advertise the operation. The linked contracts define each operation; [Harness onboarding](./harness-onboarding.md#qualify-the-adapter) describes how a Harness is qualified.

| Status | Meaning |
| --- | --- |
| Verified | Core admits it, and real-model acceptance through the pinned official client passed on that placement |
| Admitted | Core admits it through the same Runtime path, but no real-model acceptance has run on that placement |
| Rejected | Core rejects the request before execution |

Placements are `none` (no Environment), hosted (`openai_hosted`) and self-hosted (`self_hosted`). Hosted acceptance ran on Docker nodes; E2B and microsandbox run the same Runtime and adapters, and every Verified hosted cell counts as Admitted there. Self-hosted acceptance ran on Linux machines; the [self-hosted guide](../../docs/getting-started/self-hosted.md#platforms) lists the supported platforms. A verified operation is verified on its own, not in every combination with other options; combinations that a profile rejects are listed in the operation's contract.

## Execution and input

| Operation | Codex | Claude SDK | MiniMax Code |
| --- | --- | --- | --- |
| Text Turns, active input, cancellation, restart and continuation | Verified on all placements | Verified on all placements | Verified on all placements |
| [Files and Artifacts](./environment-files.md) | Verified: hosted, self-hosted | Verified: hosted, self-hosted | Verified: hosted, self-hosted |
| [Whitespace-only message text](./message-content.md) | Admitted; delivered unchanged | Rejected | Rejected |
| [Inline PNG and JPEG message images](./message-content.md) | Verified on all placements | Verified on all placements | Rejected |
| Remote image URLs | Rejected | Rejected | Rejected |
| Explicit `reasoning`; `service_tier` other than `auto` | Rejected | Rejected | Rejected |
| `text.verbosity` other than `medium` | Admitted; the native model decides | Rejected | Rejected |
| [Public token usage](./sessions-events.md) | Measured counters | Null | Null |

Native model parameters and provider protocols per Harness are in [model execution](./model-execution.md).

## Tools

| Operation | Codex | Claude SDK | MiniMax Code |
| --- | --- | --- | --- |
| [Public functions](./execution-tools.md#functions) with text results | Verified: `none`, hosted; admitted: self-hosted | Verified on all placements; object-root schemas only | Rejected |
| [Function results with images](./message-content.md#function-results) | Verified: `none`, hosted; admitted: self-hosted | Verified on all placements; successful inline PNG or JPEG results only | Rejected |
| [Structured output](./execution-tools.md#structured-output) | Rejected | Verified on all placements | Rejected |
| [Deferred function discovery](./execution-tools.md#deferred-function-discovery) | Rejected | Verified: `none`, self-hosted; admitted: hosted | Rejected |
| [Disabled web search and programmatic tool calling](./execution-tools.md#web-search-and-programmatic-tool-calling) | Verified: `none`; admitted: hosted, self-hosted | Verified: `none`; admitted: hosted, self-hosted | Verified: `none`; admitted: hosted, self-hosted |
| Enabled web search or programmatic tool calling | Rejected | Rejected | Rejected |
| [Service-origin HTTP MCP](./environments.md#public-mcp-connection-origin) | Verified: `none`; rejected elsewhere | Verified: `none`; rejected elsewhere | Rejected |
| [Environment-origin HTTP MCP](./environments.md#public-mcp-connection-origin) | Verified: hosted, self-hosted | Verified: hosted, self-hosted | Verified: hosted, self-hosted; `allowed_tools` null and `required` false only |
| [HTTP MCP bearer credentials](./execution-tools.md#http-mcp) | Verified | Verified | Verified |
| Required MCP initialization | Verified | Verified | Rejected |
| [Subagents](./subagents.md) | Verified: hosted; admitted: `none`, self-hosted | Verified: hosted; admitted: `none`, self-hosted | Verified: hosted; admitted: `none`, self-hosted |
| Subagents together with functions or HTTP MCP | Rejected | Rejected | Rejected |

Claude structured output requires a single Agent, medium verbosity and no Skills, Plugins, capability directories, MCP or `tool_search`. Claude deferred discovery requires a single Agent, only function tools besides `tool_search` and the disabled controls, no Skills, Plugins or capability directories and no structured output. Claude on a workspace placement needs the packaged bridge features for each operation (`workspace_functions`, `workspace_structured_output`, `workspace_tool_search`, `workspace_mcp_http`).

## Environment preparation

These operations need a workspace, so they apply to hosted and self-hosted placements only.

| Operation | Codex | Claude SDK | MiniMax Code |
| --- | --- | --- | --- |
| [Initial files, setup commands, Skills and Plugins](./environments.md#runtime-capability-preparation) | Verified: hosted, self-hosted | Verified: self-hosted; admitted: hosted | Verified: self-hosted; admitted: hosted |
| Capability directories | Admitted | Admitted | Admitted |
| npm and Python packages | Admitted | Admitted | Admitted |
| `packages.system` | Rejected | Rejected | Rejected |
| Network `disabled` or `restricted` | Rejected | Rejected | Rejected |
| [Plugin MCP over stdio](./environments.md#plugin-mcp) | Verified: hosted, self-hosted | Verified: self-hosted; admitted: hosted | Verified: self-hosted; admitted: hosted |
| Plugin MCP over HTTP | Admitted, with literal headers or HTTPS bearer | Admitted, anonymous or HTTPS bearer | Verified: self-hosted; admitted: hosted; anonymous or HTTPS bearer |
