---
title: "Harness capabilities"
---

This page lists what each Harness supports on each placement. Core decides admission from the Harness's declaration in `internal/harnessconfig/<package>/declaration.go`, and the Runtime that runs the Session must also advertise the operation. The linked contracts define each operation; [Harness onboarding](./harness-onboarding.md#qualify-the-adapter) describes how a Harness is qualified.

Anything this page does not list follows the Harness's native behavior with the selected model, as in a local run. Each adapter guide lists the native defaults it overrides for unattended cloud execution ([design rule](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#core-assembles-harnesses-run-natively)).

| Status | Meaning |
| --- | --- |
| Verified | Core admits it, and real-model acceptance through the pinned official client passed on that placement |
| Admitted | Core admits it through the same Runtime path, but no real-model acceptance has run on that placement |
| Rejected | Core rejects the request before execution |

Placements are `none` (no Environment), hosted (`openai_hosted`) and self-hosted (`self_hosted`). Hosted acceptance ran on Docker nodes; E2B and microsandbox run the same Runtime and adapters, and every Verified hosted cell counts as Admitted there. Self-hosted acceptance ran on Linux machines; the [self-hosted guide](../../docs/getting-started/self-hosted.md#platforms) lists the supported platforms. A verified operation is verified on its own, not in every combination with other options; combinations that a profile rejects are listed in the operation's contract.

## Execution and input

| Operation | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| Text Turns, active input, cancellation, restart and continuation | Verified on all placements | Verified on all placements | Verified on all placements |
| [Files and Artifacts](./environment-files.md) | Verified: hosted, self-hosted | Verified: hosted, self-hosted | Verified: hosted, self-hosted |
| [Whitespace-only message text](./message-content.md) | Admitted; delivered unchanged | Admitted; native drops it, and an all-blank message becomes `(no content)` | Rejected |
| [Message images](./message-content.md#images) | PNG and JPEG data URLs: verified on all placements; other data URLs: admitted; remote URLs: rejected | PNG and JPEG data URLs: verified on all placements; remote URLs and GIF and WebP data URLs: admitted | Rejected |
| `reasoning.effort` | `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`: admitted | `low`, `medium`, `high`, `xhigh`, `max`: admitted | `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`: admitted |
| `reasoning.summary` | `concise`, `detailed`, `auto`: admitted | `auto`: admitted | Rejected |
| `service_tier` other than `auto` | `default`, `flex`, `priority`: admitted | Rejected | Rejected |
| `text.verbosity` other than `medium` | Admitted; the native model decides | Rejected | Rejected |
| [Public token usage](./sessions-events.md) | Measured counters | Null | Null |

Native model parameters and provider protocols per Harness are in [model execution](./model-execution.md).

## Tools

| Operation | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| [Public functions](./execution-tools.md#functions) with text results | Verified: `none`, hosted; admitted: self-hosted | Verified on all placements; object-root schemas only | Verified: `none`; admitted: hosted, self-hosted; object-root schemas only; a result must arrive within 120 s ([difference](./index.md#differences-from-openai)) |
| [Function results with images](./message-content.md#function-results) | Verified: `none`, hosted; admitted: self-hosted; remote URLs: rejected | Successful results only; PNG and JPEG data URLs: verified on all placements; GIF and WebP: admitted | Rejected |
| [Structured output](./execution-tools.md#structured-output) | Admitted on all placements | Verified on all placements | Rejected |
| [Deferred function discovery](./execution-tools.md#deferred-function-discovery) | Rejected | Verified: `none`, self-hosted; admitted: hosted | Rejected |
| [Disabled web search and programmatic tool calling](./execution-tools.md#web-search-and-programmatic-tool-calling) | Verified: `none`; admitted: hosted, self-hosted | Verified: `none`; admitted: hosted, self-hosted | Verified: `none`; admitted: hosted, self-hosted |
| Web search `cached` or `live` and programmatic tool calling `enabled` | Admitted on all placements | `live` admitted on all placements; `cached` and `enabled` rejected | Rejected |
| [Service-origin HTTP MCP](./environments.md#public-mcp-connection-origin) | Verified: `none`; rejected elsewhere | Verified: `none`; rejected elsewhere | Verified: `none`, with `allowed_tools` null and `required` false only; rejected elsewhere |
| [Environment-origin HTTP MCP](./environments.md#public-mcp-connection-origin) | Verified: hosted, self-hosted | Verified: hosted, self-hosted | Verified: hosted, self-hosted; `allowed_tools` null and `required` false only |
| [Stdio MCP](./execution-tools.md#mcp) | Admitted: hosted, self-hosted | Admitted: hosted, self-hosted; `cwd` must be the workspace directory and `allowed_tools` null | Rejected |
| [HTTP MCP bearer credentials](./execution-tools.md#mcp) | Verified | Verified | Verified |
| Required MCP initialization | Verified | Verified | Rejected |
| [Subagents](./subagents.md) | Verified: hosted; admitted: `none`, self-hosted | Verified: hosted; admitted: `none`, self-hosted | Verified: hosted; admitted: `none`, self-hosted |
| Subagents together with functions or HTTP MCP | Verified: `none`; admitted: hosted, self-hosted | Verified: functions on `none`; admitted: HTTP MCP, hosted, self-hosted | Admitted; children get none of the Session's functions or MCP servers |

Claude structured output requires medium verbosity and combines with `multi_agent`, Skills, Plugins, capability directories, MCP and `tool_search`. Claude deferred discovery combines with MCP, Skills, Plugins, capability directories and structured output. Claude Subagents inherit the parent's tools, including deferred functions. Claude Code decides whether `tool_search` takes effect for the configured model, provider and beta settings. Claude on a workspace placement needs the packaged bridge features for each operation (`workspace_functions`, `workspace_structured_output`, `workspace_tool_search`, `workspace_mcp_http`).

## Environment preparation

These operations need a workspace, so they apply to hosted and self-hosted placements only.

| Operation | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| [Initial files, setup commands, Skills and Plugins](./environments.md#runtime-capability-preparation) | Verified: hosted, self-hosted | Verified: self-hosted; admitted: hosted | Verified: self-hosted; admitted: hosted |
| Capability directories | Admitted | Admitted | Admitted |
| npm and Python packages | Admitted | Admitted | Admitted |
| `packages.system` | Rejected | Rejected | Rejected |
| Network `disabled` or `restricted` | Rejected | Rejected | Rejected |
| [Plugin MCP over stdio](./environments.md#plugin-mcp) | Verified: hosted, self-hosted | Verified: self-hosted; admitted: hosted | Verified: self-hosted; admitted: hosted |
| Plugin MCP over HTTP | Admitted, with literal headers or HTTPS bearer | Admitted, with literal headers or HTTPS bearer | Verified: self-hosted; admitted: hosted; literal headers or HTTPS bearer |

External workspace storage additionally requires the selected Harness declaration and Runtime to support `retained_native_history`; Docker and E2B, which keep their own disks, do not. microsandbox always uses external storage, so MiniMax Code, which does not declare the capability, cannot run on it. The [Workspace filesystem protocol](../../docs/workspace-provider.md) owns storage combination admission.
