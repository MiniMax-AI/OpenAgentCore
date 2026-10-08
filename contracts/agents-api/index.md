---
title: "Agents API coverage ledger"
---

Core targets the complete OpenAI Agents API as pinned below ([public API rule](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#public-api)). This ledger records how much of each resource Core implements and which contract holds its details, then every known difference from the OpenAI service and every open gap. [API namespaces and credentials](../../docs/api/index.md) says who calls which API; the [Agents API guide](../../docs/api/public-agent-api.md) shows how to use it.

## Pinned baseline

| File | Contents |
| --- | --- |
| [upstream.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream.json) | The pin: [openai-python](https://github.com/openai/openai-python/tree/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents) 3.13.0 at commit `d7c41ef`, resources under `beta/agents`, Beta header `agents=v1` |
| [upstream/openapi.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream/openapi.json) | Unmodified official OpenAPI 3.1 at commit `046a2a0f325bf11f97966f2729219f27281ba71e`, published on 2026-09-10. Its 58 Agents, Vaults, Files and Skills operations match the pinned SDK route set. `upstream.json` records the SHA-256 checksum |
| [upstream-routes.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream-routes.json) | The normalized method/path inventory generated from the official schema |
| [openapi.yaml](./openapi.yaml) | The official public contract with Core's `x_agents_core` extension on Agent and Session request/response objects |
| [go-bindings.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/go-bindings.json) | Go names, field representations, encoding order and stored projections; it does not define official field membership, enums or constraints |

Run `make openapi` to regenerate the public Go types, the Agent request shapes, the route inventory and all three OpenAPI documents. `scripts/generate-public-api.py` reads the checked-in, checksum-verified official source without network access. It selects Agents, Vaults, Files and Skills and follows their schema references, preserving union types, nullability, required fields and constraints. The request shapes in `services/core/internal/api/official_shapes.gen.go` project `CreateAgentParams`, `UpdateAgentParams` and `SessionAgentConfigParam`; Core checks request bodies against them before it reads an Agent configuration. Core's extension types in `v1/` remain authored in Go and are added to the public schema during generation. The TypeScript client's types, enum values and field names in `packages/agents-client/src/generated/public-api.ts` are generated from the resulting public schema. The internal `/core/v1` and `/api/v1` documents come from handler annotations; the same generator projects the `/core/v1` document into `packages/agents-client/src/generated/core-api.ts`, which imports the public types it references from `public-api.ts`. In those annotations a response field Core always sends carries `binding:"required"`, a field that can be null carries `extensions:"x-nullable"`, and a Core-owned closed set uses a named Go type with typed constants in its producing package. Swag derives one enum definition from that type, shared by every wire field. Single-value discriminators keep `enums:` tags; fields mirroring official or Runtime-owned sets retain tags checked against their owners by contract tests. These annotations only shape the documents, and the client rejects a response that breaks them. `make check-openapi` checks freshness and the generator; it also runs through `make check-go`.

The public contract is the official API plus Core extensions. Standard fields are generated into `v1/official.gen.go`; `go-bindings.json` lists types consumed by Core and overrides only the Go representation or field order that existing storage or custom JSON encoding requires. Unspecified fields follow the official schema; shared shapes use one Go type. Selected discriminated unions also generate JSON serializers to retain required nullable fields for each variant. Other union serializers, Core's local limits, execution admission and state transitions remain implementation code. Contract tests verify that the public schema preserves the official definitions, extensions remain in `x_agents_core`, and all documents match registered routes. Official-client and raw HTTP tests verify behavior. Schema generation does not qualify an unimplemented feature; the gaps below still apply. Upstream upgrades update the OpenAPI and SDK pins together after comparison and compatibility tests.

The official source and existing service have these recorded differences: Agents authentication errors can return a null `code`; empty Files pages return null `first_id` and `last_id`; File resources can return null `expires_at` and `status_details`. The source declares those fields non-null. The official-client response validator allows null only for these named fields and otherwise validates OpenAPI 3.1 response schemas. Files and Skills operations omit error responses in the source, so those error bodies use the upstream shared `ErrorResponse` schema. [Wire semantics](./wire-semantics.md) and raw HTTP tests qualify service behavior; the published schema retains the official definitions.

Go input projections exclude `packages.system` to preserve its explicit rejection, recorded below. Generation does not enable an unsupported operation or change stored setup validation.

Evidence for a status comes from the pinned official SDK and raw HTTP against the running service, as [CONTRIBUTING](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#compatibility-evidence) requires.

## Coverage by resource

**Implemented** means every operation serves the pinned shapes; limits that remain are listed under [known gaps](#known-gaps). **Partial** names what is missing.

| Resource | Operations | Status | Contract |
| --- | --- | --- | --- |
| Agents | create, retrieve, update, list, delete | Implemented. Every pinned setting is saved; Session admission runs a subset | [Agents](./wire-semantics.md#agents) |
| Sessions | create (JSON or stream), retrieve, update, list, delete | Implemented. Update takes `metadata` only; deletion requires an idle or failed Session | [Sessions](./wire-semantics.md#sessions), [creation streaming](./sessions-events.md#creation-streaming) |
| Session events | create, stream | Partial: messages with text and inline images, cancellation, function results; the stream is live only | [Sessions, events and history](./sessions-events.md), [message content](./message-content.md) |
| Turns | retrieve, list | Implemented; Session Turn routes hold root Turns only | [Turns and Items](./sessions-events.md#turns-and-items) |
| Items | list | Partial: messages, commands, MCP calls, functions, web search, reasoning and Subagent coordination Items; other native variants are not projected | [Turns and Items](./sessions-events.md#turns-and-items) |
| Artifacts | retrieve, list, delete, content | Implemented | [Environment files and Artifacts](./environment-files.md) |
| Subagents | retrieve, list; Items; Turns retrieve and list; Turn Items | Partial: read-only child work; no live child progress or optional native operations | [Subagents](./subagents.md) |
| Environments | retrieve | Implemented | [Environments](./environments.md) |
| Environment files | create, list | Implemented; the list is not recursive | [Environment files and Artifacts](./environment-files.md) |
| Environment Templates | create, retrieve, update, list, delete | Implemented; execution limits are listed under [known gaps](#known-gaps) | [Environment Templates](./environments.md#templates) |
| Vaults | create, retrieve, list, delete | Implemented; no archive operation | [Vaults and Credentials](./vaults.md) |
| Vault Credentials | create, retrieve, update, list, delete | Implemented for `static_bearer` and `mcp_oauth` | [Vaults and Credentials](./vaults.md) |
| Files | create, retrieve, list, delete, content | Implemented for `purpose=user_data`; content download is rejected | [Files and Skills](./source-files.md) |
| Skills and Skill versions | create, retrieve, update, list, delete, content | Implemented | [Files and Skills](./source-files.md) |

Each Harness declares what it supports once, in `internal/harnessconfig/<kind>` ([Declare support](./harness-onboarding.md#declare-support)). [Core wire behavior](./wire-semantics.md) holds the rules that apply across resources: requests, errors and lists.

Core's own fields sit inside `x_agents_core` ([Core extensions](../../docs/api/public-agent-api.md#core-extensions-x_agents_core)). The Core administration API (`/core/v1`) and the machine API (`/api/v1`) are not part of the Agents API.

## Differences from OpenAI

Each item is Core's deliberate or native behavior where the official service behaves otherwise. The linked rule states the exact behavior.

**Requests and errors** ([Core wire behavior](./wire-semantics.md))

- Core sends no `OpenAI-Organization` or `OpenAI-Project` response headers.
- `HEAD` on the event stream, on content downloads and on the Environment files list returns 405.
- A JSON array body is rejected; the official service reads `[]` as `{}`.
- U+0000 in a stored string returns 400; the official service stores it.
- Not-found messages never name the resource; Core quotes a full metadata key where the official message abbreviates it.
- UUID identifiers also resolve in other spellings, such as uppercase or braces.
- The Files routes keep a local `unsupported_parameter` code for a repeated query key.

**Lists** ([lists](./wire-semantics.md#lists))

- A deleted Agent or Session used as a cursor returns 404; the official service still pages from it.
- A Turn cursor from another Session, a Credential cursor equal to its Vault ID, and a Skills cursor that is not a Skill ID return 404.
- Vault and Credential lists clamp a negative `limit`, as the pinned SDK describes; the official service returns 400.

**Agents and Sessions** ([Agents](./wire-semantics.md#agents), [Sessions](./wire-semantics.md#sessions))

- A repeated Session creation with the same `Idempotency-Key` returns the original Session; the official service creates a new one.
- Omitted programmatic tool calling keeps the harness's native behavior; the official default is on.
- An omitted reasoning effort stays null instead of taking the model's default.
- A Session's `agent.tools` omits `tool_search` declarations.
- Deleting a Session right after an events 202 returns 409, because Core admits the Turn in the same transaction; the official service returned 200.

**Input, events and history** ([Sessions, events and history](./sessions-events.md), [message content](./message-content.md))

- Input to a `none` Session is admitted synchronously; Core does not emulate the official asynchronous admission window.
- A function result that resumes a waiting Turn emits `turn.in_progress`, and cancelling a Turn that waits on a function result emits an interim `agent.session.in_progress`.
- Attaching to the stream mid-Turn sends no catch-up Item snapshots.
- The Items list includes in-progress and incomplete output Items, and keeps a failed function result's submitted `output`.
- Error messages omit the call and executor IDs that official messages include.
- An empty text part beside other text is accepted and stored.
- Empty input returns 400 `invalid_request` with a generic message and a null param; the official response is `invalid_request_error` with param `input`.
- Session usage is available as soon as every root Turn has settled; official reads lag by seconds.

**Files, Skills, Environment files and Artifacts** ([Files and Skills](./source-files.md), [Environment files and Artifacts](./environment-files.md))

- File uploads hold up to 512 MiB; the official limit is 512 MB. The Files list returns up to 10,000 Files by default, and a `purpose` filter other than `user_data` returns an empty page.
- Skill version numbers are never reused, and uploads and deletions of one Skill run one at a time.
- Environment files work on `self_hosted` Environments, which the official service refuses.
- Creating an Environment file over an existing regular file returns the "must not traverse symlinks or overwrite existing files" message. A parent symbolic link that stays inside the workspace is followed, and a parent that escapes the workspace or is a regular file gets a generic 400; the official service rejects symbolic-link parents.
- Artifact IDs are UUIDs.

**Vaults and Credentials** ([Vaults and Credentials](./vaults.md))

- Vault and Credential status is stored privately and defaults to `active`; with no archive operation, lists without a filter include both statuses.
- An unknown or foreign Vault in `vault_ids` returns 404 "Resource not found."; the official message names the ID.
- An explicit `null` for OAuth `access_token`, `refresh` or `token_endpoint_auth` in an update keeps the stored value.
- A static token must be an RFC 6750 `b64token` to run; other stored tokens fail at dispatch.
- Vault metadata has a 64 KiB bound and no pair or length limits; names are 1–256 bytes after trimming.

## Known gaps

**Configuration and tools**

- Explicit reasoning effort or summary, service tiers other than `auto`, enabled `web_search` and enabled programmatic tool calling are saved but rejected at Session admission.
- Harness support differs as each [declaration](./harness-onboarding.md#declare-support) states. Codex has no structured output or `tool_search`. Claude Code takes no whitespace-only text, `medium` verbosity only and function-result images only inline and in successful results; it rejects Subagents with MCP, including MCP servers that Environment Plugins install; structured output with Subagents, MCP, installed capabilities or `tool_search`; and `tool_search` with MCP or installed capabilities. MiniMax Code has no public functions, no service-origin MCP, no image input, no whitespace-only text, no required MCP and `medium` verbosity only, and takes `allowed_tools` null only. Each Harness reserves an MCP `server_label`: `codex_apps` for Codex, `functions` for Claude Code and `oac_workspace` for MiniMax Code. Claude Code also requires labels to match `^[a-zA-Z0-9_-]+$` and `allowed_tools` names to match `^[a-zA-Z0-9_.-]+$`.
- Model-derived reasoning defaults are not resolved.
- MCP tools support the `http` transport only; `stdio` is rejected, and so is an inline `authorization` on a Session MCP transport ([HTTP MCP](./execution-tools.md#http-mcp)).

**Execution and history**

- `self_hosted` installation supports Linux amd64 only. `oac-daemon install` and `start` return `UnsupportedPlatformError` on other platforms, before any installation credential claim. macOS, Windows and Linux arm64 Sandbox I/O execution are not qualified; see [self-hosted platforms](../../docs/getting-started/self-hosted.md#platforms).

- The stream does not emit reasoning-summary events, Environment `pending` or `ready` events, or every pinned interim tool-output variant.
- Native Item variants beyond those listed under [Turns and Items](./sessions-events.md#turns-and-items) are not projected, and Items cannot be modified.
- A function result that cancellation prevents from being applied never appears as an Item.
- Pinned Codex can lose command output emitted before its stream subscription.
- Behind the [credential gateway](./model-execution.md#credential-gateway), pinned Codex compacts history locally and never calls `/responses/compact`.
- Claude Code and MiniMax Code report no public usage.
- Claude Code and MiniMax Code do not support `native_session_recovery`: when a Session has a started Turn but Core has no recorded native Session ID, execution is rejected. Cold continuation with an already recorded native ID is a separate case; this declaration neither rejects it nor qualifies it. See [public-path qualification](./harness-onboarding.md#qualify-the-public-path).
- Core gives no crash-safe or exactly-once guarantee for native side effects; claimed work fails on restart without replay.
- Message images must be inline PNG or JPEG data URIs; remote URLs, `file_id` and `detail` are rejected.

**Environments and Templates**

- Runtimes do not enforce `disabled` or `restricted` networks, so Sessions that need them are rejected ([restricted network policy](./environments.md#restricted-network)).
- `packages.system` is rejected; system packages must be preinstalled.

**Files and Environment files**

- Files accept only `purpose=user_data`; other purposes, `expires_after` and the Uploads API are not supported.
- An Environment file write whose outcome is uncertain is never retried or recovered automatically; it blocks further writes and messages to the Session.

**Vaults and Credentials**

- There is no archive lifecycle, storage-key rotation or re-encryption.
- OAuth refresh happens only at dispatch: there is no refresh on a provider 401, no mid-Turn replacement and no withdrawal of a token already sent to a Runtime.
- Input sent to a Session whose selected Credential was deleted is admitted and then fails at dispatch.
- Credential creation takes no `Idempotency-Key`.

**Sessions**

- Session deletion does not purge the stored history physically.
- Stream lifetimes for `self_hosted`, hosted and no-input creation, and the creation-stream retry, are Core's own choices.

### Unverified against the official service

- The order of errors when one request has several faults, and error, default and payload-limit parity in general.
- Whether Subagent child Turns and pending Environment file writes block Session deletion.
- The official order between the pending-input error and an unknown result target.
- Failure reasons for npm, initial-file and Skill installation have no official sample.
- Codex behavior for an empty text part beside text, and for images in failed function results or as remote references; Claude behavior for a whitespace-only or empty text block inside a mixed message.
- Official `HEAD` behavior on the routes where Core returns 405.
- Environment Template hostname forms beyond exact hosts, and `disabled` combined with domains.
- Files purpose filtering and pagination while Files change; official Skill upload limits and error timing.
- Environment file list defaults (limit 20, the workspace root as the default path, no recursion, page-token invalidation), the 50 MiB `file_id` copy limit and the check order on create.
- Artifact capture of hard links, special files and a linked `outputs` directory, republication after changed bytes, and content headers and ranges.
- Vault and Credential error and retry semantics, pagination under concurrent writes, visibility after deletion, exact-URL matching against the official normalization, OAuth refresh timing and errors, and restricted-key scopes.
