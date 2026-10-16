# Parsar workspace

[简体中文](README.zh.md)

Parsar is an optional, independently started task workspace built on OpenAgentCore’s public `/v1` Agents API. Its dense lists, detail rails, navigation, themes and conversation controls follow the original Parsar UI. Core owns execution; Parsar owns Agents, projects, tasks, capabilities, recurring work and Feishu delivery. Execution always uses bypass mode. Accounts and fine-grained permissions are deferred. Feishu is the supported IM integration.

## Run

Use Node 22.13+ and pnpm 10.30.3. Configure [Core](../../docs/getting-started/install.md), a model connection and a dedicated Project key. Keep credentials in a private environment file outside the checkout:

```sh
export OPENAI_BASE_URL='http://127.0.0.1:8091/v1'
export OPENAI_API_KEY='<project-api-key>'
pnpm --filter @oac/parsar-example... install --frozen-lockfile
pnpm --filter @oac/parsar-example dev
```

Open http://127.0.0.1:18180. `OPENAI_BASE_URL` ends in `/v1`; remote endpoints require HTTPS. An SSH tunnel can expose a remote Core on loopback. `OAC_EXAMPLE_PORT` changes the local port. For production assets, run `pnpm --filter @oac/parsar-example build`, then `pnpm --filter @oac/parsar-example start`. The server binds only to loopback. Links from other sites may open the homepage as a top-level browser navigation; API requests retain same-origin checks. A local tunnel may use a different port; the browser Host must remain `localhost` or `127.0.0.1`, and write Origins must exactly match that Host, including its port. Use one server process per data directory.

**设置** changes the workspace name and Chinese/English language. The sidebar and Agent detail rail resize and remember their widths. Theme controls select light, dark or system appearance. **对话**, **运行记录**, **用量** and **审计日志** provide separate views of conversation references, public Turns, token usage and product mutations. Date filters use the browser’s local dates. Usage totals cover all matching records, not just the displayed page. Missing usage remains unknown. Optional model prices are USD per million input/output tokens, frozen when a Session starts; displayed costs are estimates using those flat rates, not provider bills.

## Agents, capabilities and Sessions

**构建** has three entries: **Agent 模板** defines what an Agent does, **环境模板** defines its prepared workspace, and **资源库** supplies reusable resources. **接入与设置** contains **飞书接入** and **设置**. Web tasks and conversations start from **工作** and **活动**.

The Agent detail opens on **配置**. The editor groups **系统提示词**, **模型与环境**, **工具与知识** and **账号与行为**. **主指令** is the Agent’s own instruction text; optional **指令片段** are appended in selection order to produce one system prompt. Resource bindings use a shared searchable selector, show selected resources with version controls where applicable, and provide **查看或编辑** and **移除绑定** actions. Removing a binding does not delete the resource. Instruction fragments, Plugins and knowledge share an eight-resource limit; selectors prevent exceeding it. **管理连接** opens account management without discarding the Agent draft. Creating or editing a model, Skill, MCP, environment or capability inside the editor preserves the Agent draft; a newly created resource is automatically selected. Edits to shared resources also affect other Agents that use them. Drafts are stored separately per Agent and can be reopened from **未保存的草稿**. They stay in this browser tab across navigation and reload until saved or explicitly discarded with **放弃草稿**; provider secrets are never included. Environment lists show preparation summaries, descriptions and the Agents that reference each template.

**本次运行** in a Session shows its public Core Session/Environment references and the safe configuration summary recorded when creation was first attempted. Retries retain that summary; later template edits do not rewrite it. Skill references without a version pin are labeled as having used the default at creation, not as a known resolved version. Older Sessions without a summary say so. No full request or credential is exposed.

**模型** groups model IDs under Providers. Fetch a Provider’s OpenAI-shaped `GET /v1/models` catalog or enter IDs manually. Saving commits the Provider and selection together. Keys remain server-side; blank edits preserve the saved key. Discovery sends only that Provider’s key and never follows redirects. Models can be disabled without deleting historical references. Codex and Claude Code workspace Sessions use the selected HTTPS Provider URL/key and harness protocol. Text-only and MiniMax Code use Core’s configured model connection; the creation form explains this distinction. Unsupported combinations fail explicitly; consult the [Harness coverage ledger](../../contracts/agents-api/index.md).

**资源库** groups models, Skills, MCP, Plugins, instruction fragments, knowledge and credentials used by Agent templates:

- **Skills** supports SKILL.md creation, ZIP upload, versions, default selection, per-Agent version pins and bounded file previews. **浏览目录** searches the skills.sh index; source/skill can also be entered directly. Preview downloads use the maintained, pinned `skills@1.5.23` CLI in a temporary directory and then the normal public Skill upload. This requires `npx` and network access on the Parsar host. Imported archives are bounded to 32 MiB and 1,000 files; Core remains the authoritative Skill store.
- **MCP** provides the original static service directory and HTTPS endpoint configuration. Optional Vault/Credential bindings use official public MCP tools, with environment origin. No anonymous inline compatibility Plugin is generated.
- **凭据** creates Vaults and write-only bearer credentials, replaces tokens and deletes credentials. **OAuth 授权** uses the maintained MCP SDK for discovery, dynamic client registration, issuer binding and PKCE. Its callback is a local page; **保存凭据** stores the OAuth access token and optional refresh configuration through public Vault APIs. OAuth tokens never return to the browser. Providers must support the selected SDK authorization flow and loopback redirects. Authorize again when a provider requires renewed consent.
- Prompt and knowledge capabilities have immutable numbered versions, defaults, pins and deprecation. Prompt versions extend the frozen Agent instructions. Knowledge versions become initial workspace files. Standard Claude/Codex Plugin ZIPs become public Environment Plugins; declared `.mcp.json` servers are shown in the editor. Executables required by stdio servers must exist in the selected environment.
- Plugin capabilities may also include an optional prebuilt **前端扩展 JavaScript（可选）** bundle, at most 1 MiB. It stays in Parsar and is not sent to Core. Installed, enabled clients execute with the local page’s authority; install only trusted code. The shared `window.__PARSAR_PLUGIN_API__` exposes React, `createContext` and `definePlugin` as in Parsar. The slot protocol is [plugin-slots.ts](src/lib/plugin-slots.ts): `workspace.main`, `workspace.content`, `layout.header.actions`, `layout.nav.bottom`, `conversation.header.actions`, `conversation.input.dock`, `conversation.tool-card` and `agent.settings.section`. Tool-card matchers receive the public `mcp_call` item as `item`; extensions must use public item fields rather than an engine’s native payload. Other context props are the local Agent/Session references supplied at each slot. Disabling a capability removes its registered UI. Cross-workspace marketplace publishing depends on the deferred account/workspace model.

**Agent 模板** combines model, harness, instructions, capabilities and an optional default environment template. Create, copy, enable/disable and edit without allocating execution resources. Edits affect future Sessions. Optional business questions use the declared `ask_user` function: answers appear in the Session and **收件箱**, and return through public function-result events with stable request IDs. This is information gathering, not execution approval.

**环境模板** defines npm and Python packages, a Bash initialization script, environment variables and initial text files. Parsar runs only in Core-managed workspaces; there is no placement, machine or sandbox-provider selector. Each new Session installs files and packages and completes setup before starting the Agent; a setup failure stops execution. Package specifications may pin versions. Initial files use canonical `/workspace/` paths. Scripts run from the workspace and should use relative paths. System dependencies belong to the managed image, and restricted network execution is not supported, so neither is offered as an editable setting.

The application owns these editable templates and sends their preparation fields inline through the public Session Environment contract; it does not create a second Core Template resource. Environment variable values are write-only in product responses: `null` preserves an existing named value, a string replaces it, and removing the name deletes it. Keep credentials in variables rather than scripts or file contents; Agent tools can read selected variables. Web, Feishu, schedules and MCP dispatch all freeze the selected template into the same pending creation request. Updates affect only new Sessions, and retries preserve the frozen preparation. **本次运行** shows package names, variable names, initial file paths and whether setup was configured, without variable values or script contents.

A Session streams replies and tool activity, groups work steps, navigates user turns with Parsar’s original rail, supports follow-up/cancel and reopens durable history. Drafts and observed request/first-text timings survive reloads in the same browser tab. **文件** uploads inputs up to 5 MiB while idle and inserts their paths into the draft. Artifacts are paginated and downloadable after the live workspace is gone; ask the Agent to save deliverables under `./outputs`. Downloads remain binary. **对话** can rename or delete Sessions through public APIs; running work must be stopped before deletion.

Sandbox list/rebuild/renew and external HTTP/A2A Agent executors remain blocked by the public API boundary. No administrator endpoint, shared Core table or fabricated substitute is used.

## Tasks and projects

**项目** stores shared context and an optional HTTPS repository URL. Context joins each new task dispatch; a repository URL does not provision credentials or clone automatically. **任务** offers list/board views, search, project/Agent filters, priorities, comments and run links. Tasks can be planned before execution resources exist.

**分配执行** freezes the task brief, project context and Agent configuration before creating a public Session. **重新执行** creates another independent Session. **恢复执行** reuses the original frozen request and key after an uncertain response or restart. Pending creation, provisioning or active execution prevents overlapping dispatches.

Comments are durable product records. **提及已分配的 Agent** starts a task with that comment when no suitable run exists, or sends it into the existing Agent Session. New automatic dispatch requires an environment template. Failed or uncertain delivery stays visible with **重试发送**, preserving the comment’s request identity. Plain comments do not execute work.

Business states (**待办**, **进行中**, **受阻**, **待验收**, **已完成**) are separate from execution states. Dispatch sets **进行中**. The operator records acceptance after execution settles; **待验收** is optional and never gates engine permissions. Reopen completed tasks before dispatching again. Referenced resources and tasks with execution history cannot be deleted.

## Recurring work

**定时任务** uses a task template with either an interval of 5–525600 minutes or a five-field Cron expression and IANA time zone. **立即执行** also works on paused plans and retains a request ID across retries. Each occurrence freezes its template and creates an independent task/Session. Missed intervals coalesce after downtime; an unfinished previous occurrence skips the next interval. The Parsar server must remain running.

Transient creation failures recover the same occurrence. Definite rejection pauses the plan for correction; a manually recovered task is adopted on resume. Five consecutive observed failed/cancelled automatic runs pause the plan; a completed run resets the count. Re-enabling resets it. Manual runs do not change failure accounting. **执行历史** links tasks/Sessions and shows observed outcomes and delivery errors. Pausing does not cancel admitted work; deleting a plan preserves its tasks and is blocked by uncertain creation.

A plan can deliver to an enabled Feishu bot’s configured group. The worker posts an initial card, anchors one durable thread, and follows the same Session for results and replies. Changed/disabled routes surface a delivery error in history rather than silently redirecting output.

## Personal connections

**我的连接** manages the local operator's external account connections. The first supported service is Feishu. Its OAuth adapter supplies authorization, identity verification, refresh, revocation and connection fields; the common task lifecycle does not know CLI names, installation commands or vendor environment variables. In **Agent 模板**, **所需用户连接** declares the configured Feishu application and **授权范围**. The environment template owns tool installation, static settings and **连接变量映射** (`connection_env`). Each mapping binds one environment variable name to `access_token`, `client_id` or `region`; fixed and mapped variables cannot share a name, and refresh tokens/application secrets are not bindable. Agents with a connection requirement must select an environment with mappings, and environments with mappings require an Agent connection. The mapping is frozen with the task and appears as variable names in **本次运行**; no personal credential is stored in either template. The application's user scopes and refresh-token permission must be enabled in Feishu before consent can succeed.

A new task freezes its Agent/environment configuration and waits before Core Session creation if authorization is missing. The Web shows the original verification link, QR code and user code; Feishu replies with the link and user code. A durable server poller completes the official OAuth device flow, verifies the returned user identity and resumes the original task without another prompt. Denied or expired flows remain visible and require **重新连接** (or `/connect` in Feishu); **取消任务** or `/cancel` cancels waiting work. Polling and creation retries survive process restarts and use the original execution idempotency key. Refresh tokens and application secrets remain in the restricted product store and are never sent to Core, the browser or the model.

The local Web operator is a single principal, not a product login or a browser-specific account. Feishu tasks bind to the initiating sender's app-scoped open ID, and authorization must return that same identity. A new consent request retains already granted scopes for the same account/application, so switching Agents does not alternate between disjoint grants. Disconnect before requesting a smaller grant. An already verified connection is reused only for the same application and user; another group member cannot continue a task that uses the initiator's personal connection. Agent MCP clients cannot initiate Agents that require personal connections. Scheduled tasks use the local operator's connection; missing consent keeps their task waiting rather than starting unprivileged work.

Each new Session receives a short-lived user access token through the public Environment `env` contract and the official CLI's token-only mode. Tokens are refreshed server-side before new execution when needed. The snapshot's expiry is shown in **本次运行**; the public API cannot replace credentials in an existing Environment, so follow-ups after expiry require a new Session. This also means a long-running Turn can encounter token expiry and must report it. **断开连接** immediately blocks further product execution with that grant and requests provider revocation; uncertain revocation is retained and retried, not reported as completed. It does not erase conversation history or artifacts.

The Feishu adapter follows the [official CLI v1.0.98 device flow](https://github.com/larksuite/cli/blob/v1.0.98/internal/auth/device_flow.go), [token refresh](https://github.com/larksuite/cli/blob/v1.0.98/internal/auth/uat_client.go) and [revocation](https://github.com/larksuite/cli/blob/v1.0.98/internal/auth/revoke.go). Its lifecycle belongs to the product; no Core protocol or privileged endpoint is added.

## Feishu workspace case

Parsar is an OpenAgentCore application example; [the Feishu workspace](examples/feishu/) is a case assembled from ordinary Parsar templates. [environment.json](examples/feishu/environment.json) declares packages, static CLI identity settings, files and connection mappings, [setup.sh](examples/feishu/setup.sh) installs the checksum-verified official CLI binary, and [agent.json](examples/feishu/agent.json) defines Codex behavior. They introduce no server route, task executor or Core feature.

Configure a model, import the official `lark-shared` Skill and desired business Skills, and configure the Feishu application through the existing resource pages. Substitute their IDs below; `--app` is the Parsar bot configuration ID, not the external app ID. The script creates an environment and an Agent using the same `/app/runtimes` and `/app/agents` operations as the UI and prints the Agent link:

```bash
node example/parsar/examples/feishu/configure.mjs --url http://localhost:3001 --model MODEL_ID --app BOT_CONFIGURATION_ID --skills SHARED_SKILL_ID,DOMAIN_SKILL_ID
```

The default scope permits a read-only identity trial; use `--scopes` with the business permissions the Agent needs. In **Agent 模板**, start a conversation and authorize through **我的连接** when prompted. The first message can request `lark-cli api GET /open-apis/authen/v1/user_info --as user --json`. The official CLI's external-token mode disables `auth` management commands; the case instructions use the read-only API instead. Other tools can use the same connection lifecycle with their own variable mappings.

The node must already resolve and reach the service endpoints. Templates run as the hosted environment user and cannot repair node DNS, edit root-owned hosts files or override network policy. In the current mx trial, personal consent, automatic task dispatch and reuse in a new Session are verified, but CLI business calls remain blocked by the installation's private Feishu gateway DNS/network configuration. This case is not qualified as a completed business workflow on that installation.

## Feishu

**飞书接入** supports multiple enterprise/personal bots, Feishu/Lark regions, configured groups and an optional direct-message route. Secrets stay in the restricted local database. The official SDK owns authentication, normalization and long connections; no inbound public callback is needed.

1. **添加机器人** accepts an existing **App ID**/**App Secret**, or **扫码创建机器人** provisions an application through Feishu’s device flow. QR polling resumes after restart, is rate-limited, and never exposes the device code or resulting secret. A provisioned bot stays disabled until routes are configured.
2. In the developer console, enable the bot, long-connection event delivery and `im.message.receive_v1`, publish the app and add it to the target group. Grant mention receipt and send-as-bot permissions. Direct messages, message resources, reactions and optional group history also require their respective scopes. Missing history/reaction permissions do not discard an accepted task.
3. For private chat, select **回复私聊的 Agent** and click **启用私聊**. New private-chat bindings follow the Agent’s default environment; existing overrides are preserved. Re-enabling a bot with group routes explicitly warns that those groups resume too. Shared private-chat routes label the selection as a default and retain users’ existing Agent choices. The page shows **已连接，可以去飞书聊天了** only after the bot connection is established. Send a task in the bot’s Feishu private chat; personal-account consent is requested when required by the Agent. **高级设置：群聊、应用凭证与更多选项** contains application credentials, allowed groups, optional project, Agent and environment policy (**使用 Agent 默认环境** or an explicitly selected environment template); enable **启用飞书机器人**. Group members and permitted bot DM users are the execution access boundary. **允许群成员切换 Agent（共享机器人）** enables `/list` and `/select <name or ID>` for new tasks; selection is persisted per sender/chat. Default inheritance resolves when a new topic creates its task; existing topics retain their environment. Missing defaults and unsupported automatic environments reject explicitly, including `/select`; there is no fallback.
4. Mention the bot for a new group task. Follow-ups in the known thread do not require another mention. DMs continue the sender’s conversation; `/new` or `/reset` starts another. Other bots, unconfigured chats and `@all` are ignored. Existing threads retain their execution binding when routing changes.
5. Text, rich posts, images, files, audio/video resources, quoted message chains and merged forwards are normalized before dispatch. Context reads happen after durable ingress. Attachment limits are eight resources and 5 MiB total; inline images are further bounded by the public message-size limit. Follow-up files wait until the Session is idle. **新话题附带最近群消息（需要历史消息权限）** adds bounded recent context; quoted reads are capped at four levels. Attached bytes are released from delivery receipts after successful dispatch.
6. `/cancel`, `/review`, `/done`, `/reopen` and `/help` operate in the same thread. Cancellation takes priority over queued follow-ups and recovers an uncertain Session creation before cancelling it. `/answer <call_id> <text>`, or a plain reply when one business question is pending, submits a public function result. Execution permissions are always bypassed.

Accepted messages are committed to SQLite before the WebSocket ACK. Public request IDs and frozen payloads survive retries/restarts. The worker reconciles durable Turns/Items, edits the accepted card with progress and terminal status, sends a result card per terminal Turn, and follows results/questions with a text @ notification. Progress uses five-second polling; it is not a separate model/tool loop. Typing reactions are best effort. Complete history and Artifact downloads remain in the Web Session. Settled threads stop polling after a five-minute grace period and wake on new ingress.

**消息记录与排查** expands **最近投递**, which shows the latest 100 receipts plus unresolved records. **重试此投递** retains the original identity. Busy file writes retry after ten seconds; other transient failures use bounded backoff. A failed inbound receipt blocks later messages in that thread. Feishu UUIDs deduplicate for one hour; uncertain outbound sends are not replayed beyond 55 minutes and require manual thread inspection. Disabling a bot pauses ingress/delivery without cancelling Core work. App/route changes never send saved messages through another identity.

## Expose an Agent through MCP

An Agent’s **对外提供** tab creates/replaces/revokes a local MCP token and displays client configuration. The token is shown once; only its hash and 90-day expiry are stored. The endpoint is `http://127.0.0.1:<port>/mcp/agents/<agent-id>` and supports the maintained MCP SDK’s Streamable HTTP transport. It remains loopback-only and is separate from Core’s API.

`ask_agent` uses the Agent’s default environment template to create a normal public Session, returning `run_id` and a conversation link. `get_run` retrieves status and output for a run started through that Agent endpoint; repeat while pending. If initial observation fails after dispatch, `ask_agent` still returns the run identifier with `status: "unknown"`; recover through `get_run` instead of repeating the task. Business questions are answered in Parsar. Tokens cannot read another Agent’s runs. Account-based sharing and public hosting are not included.

## Ownership and local API

Core’s Project key stays server-side. Successful Session creation leaves a Core reference and product metadata; pending creation retains the frozen public request until confirmed. Core remains authoritative for execution, Items, Turns, Skills, Vaults, Credentials and Artifacts. SQLite stores product configuration, capability versions, task/comment records, schedule receipts, Feishu delivery state and temporary OAuth/registration state. Files live under `~/.oac/data/parsar-example/`; `OAC_EXAMPLE_DATA_DIR` overrides this with an absolute path. Core origin and Project-key fingerprint select the file. Back it up with the server stopped. Provider/bot secrets and pending OAuth credentials reside in it; the Core Project key does not. Builds/caches use `${OAC_DEV_HOME:-$HOME/.oac}`.

| Product route family | Caller and credential |
| --- | --- |
| `/app/providers`, `/app/models`, `/app/agents`, `/app/runtimes`, `/app/mcps`, `/app/sessions` | Same-origin local UI; product workers reuse operations in-process |
| `/app/projects`, `/app/tasks`, task `/runs` and `/activity` | Same-origin local UI and authorized Feishu/schedule operations |
| `/app/connections`, `/app/connections/feishu/<bot-id>`, `/app/sessions/<id>/connection` | Same-origin local operator; in-process Feishu dispatch freezes sender identity; responses never contain tokens |
| `/app/schedules`, plan `/run` and `/history` | Same-origin local UI; worker keeps durable occurrence identities |
| `/app/feishu/bots`, `/app/feishu/provision`, `/app/feishu/deliveries` | Same-origin local UI; credentials never returned |
| `/app/capabilities`, capability `/client.js`, `/app/skills/registry/preview` | Same-origin local UI and installed trusted frontend bundles |
| `/app/mcp-oauth/<operation-id>`, `/oauth/callback` | Local OAuth flow; random state, PKCE and same-origin completion POST |
| `/app/agents/<id>/mcp-token` | Same-origin local UI; issued token shown only in creation response |
| `/mcp/agents/<id>` | Local MCP client; Agent-scoped bearer token |
| `/app/settings`, `/app/audit` | Same-origin local UI; audit contains mutation metadata, never request bodies |
| Allowlisted `/v1` proxy | Same-origin local UI; server injects its Project key |

Core builds and distributes independently of this example. No `/core/v1` or machine API is available through the product proxy.

## Validation

```sh
make check-example
```

The gate runs TypeScript, Node proxy/product/worker tests, a production build and browser acceptance. Browser fixtures use ports 18180/18181 and isolated data under `~/.oac/tests/parsar-example/`. Install Chrome with `pnpm --filter @oac/parsar-example exec playwright install chrome` when needed. Tests exercise durable retries, duplicate ingress, Feishu normalization/media/context, QR state, schedule failures, OAuth PKCE/Vault persistence, MCP client authentication, frontend plugins and UI workflows. Fixture tests do not establish real model or Feishu service availability.

The opt-in live browser probe requires an already running example backed by an isolated real Project:

```sh
OAC_EXAMPLE_LIVE_URL=http://127.0.0.1:18180 \
OAC_EXAMPLE_LIVE_MODEL='<configured-model>' \
pnpm --filter @oac/parsar-example test:live
```

It executes a real model and leaves resources for inspection. Live Feishu acceptance requires a designated test bot with its scopes and an authorized chat; configuration alone does not verify delivery.
