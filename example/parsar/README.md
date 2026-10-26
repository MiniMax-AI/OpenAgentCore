# Parsar workspace

[简体中文](README.zh.md)

Parsar is an optional, independently started conversation workspace built on OpenAgentCore’s public `/v1` Agents API. People talk to Agents in two places: the local Web chat and Feishu. Core owns execution; Parsar owns Agents, environment templates, the resource library and Feishu delivery. Execution always uses bypass mode. Accounts and fine-grained permissions are deferred.

## Run

Use Node 22.13+ and pnpm 10.30.3. Configure [Core](../../docs/getting-started/install.md), a model connection and a dedicated Project key. Keep credentials in a private environment file outside the checkout:

```sh
export OPENAI_BASE_URL='http://127.0.0.1:8091/v1'
export OPENAI_API_KEY='<project-api-key>'
pnpm --filter @oac/parsar-example... install --frozen-lockfile
pnpm --filter @oac/parsar-example dev
```

Open http://127.0.0.1:18180. `OPENAI_BASE_URL` ends in `/v1`; remote endpoints require HTTPS. An SSH tunnel can expose a remote Core on loopback. `OAC_EXAMPLE_PORT` changes the local port. For production assets, run `pnpm --filter @oac/parsar-example build`, then `pnpm --filter @oac/parsar-example start`. The server binds to `OAC_EXAMPLE_HOST`, default `127.0.0.1`. Set it to one IPv4 address of the machine, such as its LAN address, to serve that network; Parsar has no sign-in, so everyone who can reach the address can use the workspace, its models and its Feishu bots. Links from other sites may open the homepage as a top-level browser navigation; API requests retain same-origin checks. The browser Host must be that address (`localhost` is also accepted on the loopback default), a local tunnel may use a different port, and write Origins must exactly match the Host, including its port. Use one server process per data directory.

## Product model

| Object | What it is |
| --- | --- |
| **资源库** | Building blocks, grouped by what uses them: models for Agent engines; Skills, MCP servers, Plugins, knowledge and instruction fragments for Agents; credentials for MCP. Resources are shared: editing one affects everything that uses it. Only live configuration blocks removal: an Agent that binds an item, an Agent that uses a template or model, or a bot bound to an Agent. Conversations do not; they keep the configuration snapshot they were created with and their full history. |
| **环境模板** | The prepared workspace: npm and Python packages, a Bash setup script, environment variables, the secrets it needs and initial files. |
| **Agent** | What people talk to: name, description, instructions, how it runs, one required environment template, capabilities and behavior switches. How it runs mirrors a local Codex configuration: Harness, model, reasoning effort, web search and, under **高级**, reasoning summary, verbosity and service tier. **复制** creates an independent copy. |
| **飞书机器人** | One bot is bound to at most one Agent. It routes its chats to that Agent and is the Agent’s Feishu identity in every conversation, including Web chats. |
| **对话** | One public Core Session with its own Environment, created from the Agent's saved Core Agent and its environment template's Core Environment Template. Core copies both when the Session starts. |

Configuration lives only in templates and Agents; a conversation never overrides it. Edits affect only new conversations. When the Agent or its environment template changes, an existing conversation says so and offers **用新配置开始新对话**.

## CLI and management Agent

[cli.mjs](cli.mjs) is a dependency-free Node 22.13+ client of the same `/app` and exposed `/v1` routes as the UI. It adds no product server endpoint or execution path. Set `PARSAR_URL` to the Parsar Web origin, not the Core URL. It inherits the deployment's trusted-network access: anyone who can reach Parsar can manage its resources. No Core key is passed to the CLI or Agent; do not expose this workspace publicly or grant the management environment to an untrusted Agent.

```sh
export PARSAR_URL=http://127.0.0.1:18180
node example/parsar/cli.mjs --help
node example/parsar/cli.mjs GET /app/agents
node example/parsar/cli.mjs uuid
node example/parsar/cli.mjs PUT /app/runtimes/REPLACE_WITH_UUID --file environment.json
```

The CLI emits JSON; `--file -` reads a JSON body from stdin. It never follows redirects or retries writes automatically. Generate and retain each resource ID before creating it. After an uncertain write, GET that ID before retrying. Editing uses GET followed by PUT of the complete object, preserving its `revision` and untouched fields; a 409 requires rereading and reconciling the change. Environment variable values returned as `null` retain their saved values when sent back. The client handles JSON APIs; binary downloads, multipart uploads and SSE remain available through the existing Web UI/client.

The same file is a JavaScript SDK:

```js
import { createParsarClient } from "./cli.mjs";
const parsar = createParsarClient(process.env.PARSAR_URL, { token: process.env.PARSAR_TOKEN });
const agents = await parsar("GET", "/app/agents");
```

The [management example](examples/manager/configure.mjs) creates **parsar他爹** as an ordinary Agent plus an ordinary environment template containing the CLI and its [operation guide](examples/manager/GUIDE.md). It uses an existing model and the Node runtime included with hosted Codex or Claude Code; it does not require MCP or a special Agent type. Start Parsar with `OAC_EXAMPLE_HOST` set to a local LAN IPv4 reachable by both the configuring process and the sandbox (see [Run](#run)); the default loopback listener cannot serve a hosted sandbox:

```sh
node example/parsar/examples/manager/configure.mjs \
  --url http://PARSAR_LAN_IP:18180 \
  --model MODEL_ID --harness codex
```

`--url` is the address reached by the configuring process; `--agent-url` is the same server's address reachable from the sandbox, defaulting to `--url`. Use different addresses only when your deployment explicitly provides a proxy between them. The script prints resource IDs before writes and refuses to overwrite existing resources. Use `--agent-id` and `--environment-id` to choose IDs explicitly. If a later step fails, inspect the reported IDs; partial resources are retained. Open the returned Agent link and start a conversation, for example “创建一个能分析 Excel 的 Agent，复用现有模型，配置好环境后给我链接。” Created resources appear in the ordinary lists and remain editable there. Trials verify environment preparation and a first reply, not the complete business task. Shared templates may be trialled with another bound Agent.

You can also ask “帮我给这个 Agent 开通飞书机器人。” The manager follows its operation guide to reuse a bot or start registration, returns the authorization link for you to confirm, then binds the Agent and checks connection and delivery status. Enter existing application secrets only on **飞书接入**, never in chat. New setups default to private chat; group chat requires an explicit request. Registration and connection alone do not prove that a test message was received and answered.

### Hosted sandboxes and an authenticated gateway

The managed microsandbox deployment blocks private networks except its Core connection ([node defaults](../../docs/getting-started/nodes.md)). A LAN Web address can therefore work on the host and still return `ECONNREFUSED` inside the Agent. Do not weaken Core isolation or substitute its administration endpoint. Provide a public HTTPS gateway to Parsar with its own credential.

[The Caddy example](examples/manager/Caddyfile) binds only to `127.0.0.1:18181`, requires a nonempty `PARSAR_TOKEN` on every forwarded request, exposes only `/app/*` and `/v1/*`, and removes the gateway credential before forwarding to Parsar. It rewrites Host and Origin to the exact `PARSAR_UPSTREAM` address expected by the existing server. It adds no Core access. The token grants the same workspace access as the internal Web UI; use a cryptographically random token, keep it in a private environment file, and give the management environment only to trusted Agents.

With [Caddy](https://caddyserver.com/docs/install) and [cloudflared](https://developers.cloudflare.com/tunnel/get-started/quick-tunnels/) installed on the Parsar host, set `PARSAR_UPSTREAM` to its existing IPv4 address and port (for example `10.0.0.2:18180`) and load `PARSAR_TOKEN` from private storage. Start Caddy, then the tunnel in another process:

```sh
caddy run --config example/parsar/examples/manager/Caddyfile --adapter caddyfile
cloudflared tunnel --url http://127.0.0.1:18181
```

Point the tunnel only at the authenticated gateway, never at the unauthenticated Parsar listener. Verify the public URL returns 401 without a token and with an incorrect token before use. The CLI reads `PARSAR_TOKEN` from its environment and sends it as a Bearer header; tokens require HTTPS (loopback HTTP is allowed for local tests), and redirects remain disabled. Generate a token, for example, with `node -e 'process.stdout.write(require("node:crypto").randomBytes(32).toString("hex"))'` redirected to a mode-0600 file; do not paste it into a conversation or URL.

Run the management example with `--url https://<tunnel-host> --web-url http://<Parsar-LAN-IP>:18180` and `PARSAR_TOKEN` loaded in the configuring process. The script stores the token as an environment value, which product reads redact, and sets `PARSAR_WEB_URL` for browser links. For an existing manager, update its environment's `PARSAR_URL`, `PARSAR_TOKEN`, `PARSAR_WEB_URL` and initial CLI/guide files through the ordinary GET/PUT flow, preserving the revision and other fields. Start a **new conversation** to use the updated environment; old conversations retain the previous address and files.

Quick Tunnel URLs last only for that tunnel process and may change after restart. Keep the gateway and tunnel running, update the manager environment after a URL change, and use a new conversation. This preview gateway serves API calls, not public browser sessions or SSE. Stop the tunnel to revoke public access, or replace its token and restart Caddy to rotate the credential. Gateway checks run with `PARSAR_TEST_CADDY=/path/to/caddy node --test example/parsar/tests/gateway.test.mjs` while port 18181 is free.

## Web chat

**新对话** opens the home page: choose an Agent, describe the work and send. The sidebar lists recent conversations; **全部对话** opens the full list with search, Agent, source and date filters, plus **运行**, **用量** and **审计日志**. **运行** also sits directly under **新对话** with the number of running conversations. It lists every running turn from Web and Feishu with its stage and waiting time, and the recently finished ones with their duration, automatic recoveries and plain-language failures; it also shows how many conversations are running, waiting for an answer or failed in the last five minutes. Running turns can be stopped and the latest failed turn of a conversation retried there. Date filters use the browser’s local dates. Usage totals cover all matching records; missing usage remains unknown. Optional model prices are USD per million input/output tokens, frozen when a conversation starts; costs are estimates, not provider bills.

A conversation streams replies and tool activity, groups work steps, navigates user turns with the turn rail, supports follow-ups and cancellation and reopens durable history. Messages sent while the Agent is working, from Web or Feishu, join the running task: **运行** shows it once, and in Feishu it keeps its progress card. Answer business questions on their card. Enter sends; Shift+Enter adds a line. Drafts survive reloads in the same browser tab. The folder button uploads inputs up to 50 MiB while idle through Core's Files API, inserts their path into the draft and downloads Artifacts after the live workspace is gone; ask the Agent to save deliverables under `./outputs`. The info button shows the Agent, model, Harness, environment template, Feishu identity, source and the configuration recorded at creation, without variable values, script contents or credentials. When a creation response is lost, the conversation page offers **恢复创建**, which reuses the frozen request and idempotency key.

When a follow-up response is lost while Core resumes the environment, Web keeps the input in the running ledger and confirms receipt with the original idempotency key before monitoring its outcome. Explicit request rejections still return an error; input that never starts is reported after its reservation expires.

To show thinking, set the Agent’s **高级 → 推理摘要** to `auto` and start a new conversation. When the Harness emits a native summary, **思考过程** appears directly in the conversation, separate from tool steps. While thinking is in progress it shows the latest text; after completion it defaults to a single row. Click to read the full summary, which is restored from history after reconnection. Existing conversations keep the configuration recorded at creation; enabling summaries does not add thinking to old history.

Parsar watches every running turn, from Web and Feishu alike. Before the first output it shows the stage (**正在准备工作环境**, **正在启动**) with the waiting time. Recovery is automatic and happens only while nothing has run: an Environment that fails to initialize, or a claimed prepared conversation whose first turn has not started within 45 seconds, is replaced once with the configuration the conversation was created with, sent inline, so later edits to the Agent or its template do not reach it; a turn without any output 45 seconds after it started is cancelled and its input resent once; a turn that fails before any output is resent once. Replaced turns are hidden from the conversation and never reported in Feishu. A final failure is explained in plain language with **重试**. **产物** lists the files recent conversations produced, linked to their conversation.

Optional business questions use the declared `ask_user` function. They appear in the conversation and return through public function-result events with stable request IDs. This is information gathering, not execution approval.

**设置** changes the workspace name, the Chinese/English language and the light, dark or system theme.

## Agents and resources

**Agent** lists Agents as cards with their engine, model, environment template and bound Feishu bot. The Agent page groups **基本信息**, **执行与环境**, **指令**, **能力** and **行为**. **主指令** is the Agent’s own instruction text; optional **指令片段** are appended in selection order to produce one system prompt. Selection limits follow Core's [Environment limits](../../contracts/agents-api/environments.md#plugins-and-capability-directories): up to 50 Skills and 50 Plugins, and up to 50 knowledge items and environment-template files together; instruction fragments and MCP servers have no count limit. Bind only what the Agent needs, since every Skill and tool adds to what the model reads. In **执行与环境**, the Harness narrows every option to its declared values ([Harness capabilities](../../contracts/agents-api/harness-capabilities.md)), which `make generate-harness-catalog` projects into [shared/harness-catalog.mjs](shared/harness-catalog.mjs); switching Harness resets values the new one does not accept and says which. Empty options keep the Harness default; web search defaults to **关闭**. Web conversations and Feishu step records show searches as **搜索网页**. Creating or editing a model, Skill, MCP, environment template or capability from the Agent page keeps unsaved fields, and a newly created resource is selected automatically. Unsaved Agent edits are restored when you return to the page in the same browser tab, and **保存** points to any missing required field. An existing Agent's page shows the last seven days: success rate, median first reply and automatic recoveries.

**模型** groups model IDs under Providers. Fetch a Provider’s OpenAI-shaped `GET /v1/models` catalog or enter IDs manually. Keys remain server-side; blank edits preserve the saved key. **测试** sends one tiny generation with the saved connection and shows the latency or the provider's error, never the key. Discovery sends only that Provider’s key and never follows redirects. Every Harness uses the selected HTTPS Provider: Responses for Codex, Anthropic for Claude Code and MiniMax Code. Unsupported combinations fail explicitly; consult the [Harness coverage ledger](../../contracts/agents-api/index.md).

**资源库** has three tabs: **能力**, **模型** and **凭据**.

- **能力** lists every capability Agents can bind in one table, with its type, version, the number of enabled Agents that bind it and the credentials it needs; filters narrow it to Skill, MCP, Plugin, 指令片段 or 知识. **新建能力** adds any kind: an HTTP MCP, one from the MCP directory, a written or uploaded Skill, one from the Skill directory, a Plugin, an instruction fragment or knowledge.
- **Skills** have versions, default selection, per-Agent version pins and bounded file previews. The Skill directory searches the skills.sh index; previews use the pinned `skills@1.5.23` CLI in a temporary directory and the normal public Skill upload, which requires `npx` and network access on the Parsar host. Imported archives are bounded to 32 MiB and 1,000 files.
- **MCP** servers are HTTPS endpoints using official public MCP tools with environment origin. One marked **需要凭据** uses the Vault credential each Agent that binds it chooses.
- Instruction fragments and knowledge have immutable numbered versions, defaults, pins and deprecation. Knowledge versions become initial workspace files. Standard Claude/Codex Plugin ZIPs become public Environment Plugins; declared `.mcp.json` servers are listed. Executables required by stdio servers must exist in the environment.
- A Skill or Plugin can list the variables it needs (**需要的密钥**). Each Agent that binds it chooses a secret for each in its **凭据** section, and a conversation receives the values as environment variables. An Agent cannot be saved until every credential its capabilities need is chosen; a choice nothing needs any more is dropped.
- **凭据** holds **密钥**, write-only values for those variables, and **MCP 凭据**: Vaults with write-only bearer credentials. **OAuth 授权** uses the maintained MCP SDK for discovery, dynamic client registration, issuer binding and PKCE, then stores the token through public Vault APIs. Secret values and OAuth tokens never return to the browser.

**环境模板** runs only in Core-managed workspaces. Each new conversation installs files and packages and completes setup before starting the Agent; a setup failure stops execution. Initial files use canonical `/workspace/` paths; scripts run from the workspace. Variable values are write-only in product responses: `null` preserves an existing value, a string replaces it and removing the name deletes it. **需要的密钥** lists variables the Agent supplies, as for Skills and Plugins. Each template is saved as a Core Environment Template and each Agent as a Core Agent. Parsar rewrites them when they or anything they bind change, and recreates them on next use if the Core Project changed. A conversation's Session references both and adds only what differs per conversation: the bot variables, the Agent's Skills, Plugins and knowledge files, and attachments ([template inheritance](../../contracts/agents-api/environments.md#inheritance)). Core may reuse an identical earlier preparation for up to 24 hours ([preparation reuse](../../contracts/agents-api/environments.md#preparation-reuse)), and a long-idle conversation can move to a new sandbox; in both cases only `/environment` (including `/workspace`) is kept and setup does not run again. Setup must therefore write everything it needs later under `/workspace` or the package directories, never under HOME. The Feishu workspace keeps lark-cli's configuration and encrypted credentials in `/workspace/.lark-cli` through `LARKSUITE_CLI_CONFIG_DIR` and `LARKSUITE_CLI_DATA_DIR`.

**提前准备，新对话无需等待安装** keeps one prepared Session for each Agent that uses the template. Parsar creates it without input, so Core prepares its Environment ahead of time, and the next Web or Feishu conversation takes it over with its first message; the conversation keeps its own title. A prepared Session is replaced when the Agent, its environment template, bot or model provider changes, after 12 hours, or once taken; turning the switch off discards it. A first message with images or files starts an ordinary Session. Each prepared Session holds a workspace while it waits.

**试运行** creates one real conversation with an Agent that uses the template, waits for its first reply, shows success with the time or the plain-language failure, and deletes the conversation.

## Feishu

**飞书接入** shows one card per bot. **添加机器人** offers **扫码创建**, which provisions an application through Feishu’s device flow with app-identity scopes pre-filled on the confirmation page, where you also name the app: `im:message.group_msg` and the messaging, Base, Calendar, Docs, Sheets, Slides, Wiki, Tasks, meetings and contact scopes the organization grants without review ([the list](server/feishu-registration.mjs)); scopes that need review are applied for only when an Agent needs them, or **已有应用** with an App ID and App Secret. A bot's name is its app's name in Feishu: Parsar reads it when the bot is created or added, and again whenever it connects. A new bot stays disabled until configured. Secrets stay in the restricted local database. The official SDK owns authentication, normalization and long connections; no inbound public callback is needed.

1. On the card, choose the **Agent**, turn on **私聊** and/or **群聊**, then **保存并启用**. Enabling requires credentials, an Agent with an environment template and at least one of the two. With **群聊** on, the bot answers mentions in every group it has joined; adding the bot to a group is all that is needed. Who can add the bot is controlled by the app’s availability in the developer console.
2. In the developer console, enable the bot, long-connection event delivery and `im.message.receive_v1`, publish the app and add it to the target groups. Grant mention receipt and send-as-bot permissions; direct messages, message resources and reactions need their own scopes. A missing reaction permission does not discard a message.
3. Private chat keeps one conversation per sender until `/clear`. In groups, mentioning the bot starts a topic and each topic is one conversation shared by everyone who replies in it. Follow-ups without a mention arrive only when the app has the “read all group messages” scope (`im:message.group_msg`). **扫码创建** requests it; for **已有应用**, enable it in the Feishu developer console. A topic reply that mentions only other people is addressed to them and is ignored. The bot serves its own tenant only: messages from external contacts are ignored. Other bots and `@all` are ignored, as are groups while **群聊** is off and private chats while **私聊** is off. Changing the bound Agent affects new topics and new private conversations.
4. Text, rich posts, images, files, audio and video, quoted messages and merged forwards are normalized before dispatch. Attachment limits are eight resources and 50 MiB total; files reach the workspace through Core's Files API. Follow-up files wait until the conversation is idle. Quoted messages are read up to four levels; earlier group messages are not read.
5. Commands: `/clear` clears the context: it stops any running work and the next message starts a new conversation, in a private chat or in the same group topic. `/stop` stops the current task; it takes priority over queued follow-ups and recovers an uncertain creation before stopping it. `/answer <call_id> <text>` answers a pending business question, and a plain reply answers when only one is pending. `/help` lists the commands.

Accepted messages are committed to SQLite before the WebSocket ACK; request IDs and frozen payloads survive retries and restarts. The worker edits one compact progress card in place, includes any pending business question in that card, and finishes it with the result. Each reply shows only the current exchange, including answers that resume the same Core Turn. The answer stays visible; while awaiting a business decision, the question takes priority and its supporting context is collapsed, except QR authorization instructions. The card has a colored header for its status. Its process is one timeline in the order things happened. Only commentary stays open as plain text; thinking folds behind its first sentence, and consecutive tool calls fold into one row (such as `运行 3 条命令` with their total time, failures or 进行中) in which each call folds again behind its command, showing parameters, output, error and exit code. While running, the header also says what is happening now and how many steps have run. A running card shows the latest four steps and folds the earlier ones into a tinted block above a divider; a finished card puts the answer first and folds the whole process into a tinted **处理过程** block; a failed card shows the failed step right under its cause. Long content is cut to marked, byte-bounded excerpts. Every step stays in the card: to stay within Feishu's card size limit, the oldest steps first drop their tool output, then shrink to one line, and only a turn with hundreds of steps summarizes its oldest ones in one line. Feishu messages never send people to the Web. Progress uses five-second polling. Complete history and Artifacts remain in the Web conversation. Settled threads stop polling after five minutes and wake on new messages. **消息记录** lists the latest 100 deliveries plus unresolved ones; **重试** keeps the original identity. A failed inbound message blocks later messages in that thread. Uncertain outbound sends are not replayed beyond 55 minutes. Disabling a bot pauses delivery without cancelling Core work.

### Bot identity

The Agent acts as its bound bot. Each Feishu message reaches it after a first line naming its `message_id`, `chat_id`, chat type and sender `open_id`, so a Skill can key work to the message; Parsar's own views hide that line. When the Agent's reply names scopes its tools reported missing, the card says **需要开通权限** and adds, under the reply, a link to applying for those scopes in the Feishu developer console; scopes the Agent only ran into while exploring are not asked for. Once an administrator approves the new version, replying continues. Every conversation of an Agent with a bound bot, Web ones included, receives the bot’s identity as `FEISHU_APP_ID`, `FEISHU_APP_SECRET` and `FEISHU_BRAND`; nothing needs to be mapped. A template, Skill or Plugin that needs the bot lists these names under **需要的密钥**: the library shows **飞书机器人** as its requirement, the Agent page shows the bot that provides it, and an Agent without a bot cannot be saved. Only the bound bot supplies these names. The Feishu case's setup script runs `lark-cli config init --app-secret-stdin`, so the CLI mints and refreshes tenant tokens itself and long conversations keep working. Do not pass them as `LARKSUITE_CLI_APP_ID` or `LARKSUITE_CLI_APP_SECRET`: those switch lark-cli to environment credentials, which require a ready-made tenant token. Anyone who can talk to the bot can use the bot’s permissions, and tools in the Environment can read the secret. Grant the bot only the scopes its Agent needs and prefer one bot per Agent. A rotated secret applies to new conversations only.

### Feishu workspace case

[The Feishu workspace](examples/feishu/) is assembled from ordinary templates. [environment.json](examples/feishu/environment.json) declares packages, the bot identity it needs and files, [setup.sh](examples/feishu/setup.sh) installs the checksum-verified official CLI binary and configures it with the bot credentials, [agent.json](examples/feishu/agent.json) defines a Codex Agent. Configure a model, import the official `lark-shared` Skill and the business Skills you need, add a bot, then run:

```bash
node example/parsar/examples/feishu/configure.mjs --url http://localhost:3001 --model MODEL_ID --bot BOT_ID --skills SHARED_SKILL_ID,DOMAIN_SKILL_ID
```

`--bot` is the Parsar bot ID, not the Feishu App ID. The script creates the environment template and Agent through the same `/app/runtimes` and `/app/agents` operations as the UI, binds the bot and prints the Agent link. The first message can ask for `lark-cli api GET /open-apis/bot/v3/info --as bot --json`. The node must resolve and reach the Feishu open platform; templates cannot repair node DNS or network policy.

## Ownership and local API

Core’s Project key stays server-side. Successful conversation creation leaves a Core reference and product metadata; pending creation retains the frozen public request until confirmed. Core remains authoritative for execution, Items, Turns, Skills, Vaults and Artifacts, and holds each Agent and environment template as a saved Agent and Environment Template written only by Parsar's server. SQLite stores product configuration, capability versions, Feishu delivery state and temporary OAuth/registration state. Files live under `~/.oac/data/parsar-example/`; `OAC_EXAMPLE_DATA_DIR` overrides this with an absolute path. Core origin and Project-key fingerprint select the file. Back it up with the server stopped. Provider and bot secrets reside in it; the Core Project key does not. Builds and caches use `${OAC_DEV_HOME:-$HOME/.oac}`.

| Product route family | Caller and credential |
| --- | --- |
| `/app/providers`, `/app/models`, `/app/agents`, `/app/runtimes`, `/app/mcps`, `/app/sessions`, `/app/artifacts` | Same-origin local UI; the Feishu worker reuses Session creation in-process |
| `/app/feishu/bots`, `/app/feishu/provision`, `/app/feishu/deliveries` | Same-origin local UI; credentials never returned |
| `/app/capabilities`, `/app/skills/registry/preview` | Same-origin local UI |
| `/app/mcp-oauth/<operation-id>`, `/oauth/callback` | Local OAuth flow; random state, PKCE and same-origin completion POST |
| `/app/settings`, `/app/audit` | Same-origin local UI; audit contains mutation metadata, never request bodies |
| Allowlisted `/v1` proxy | Same-origin local UI; server injects its Project key |

Core builds and distributes independently of this example. No `/core/v1` or machine API is available through the product proxy.

## Validation

```sh
make check-example
```

The gate runs TypeScript, Node proxy/product/Feishu tests, a production build and browser acceptance. Browser fixtures use ports 18180/18181 and isolated data under `~/.oac/tests/parsar-example/`; set `OAC_EXAMPLE_PORT` when 18180 is taken. Install Chrome with `pnpm --filter @oac/parsar-example exec playwright install chrome` when needed. Tests exercise durable retries, duplicate ingress, Feishu routing, shared topics, bot identity variables, QR state, OAuth PKCE/Vault persistence and UI workflows. Fixture tests do not establish real model or Feishu service availability.

The opt-in live browser probe requires an already running example backed by an isolated real Project:

```sh
OAC_EXAMPLE_LIVE_URL=http://127.0.0.1:18180 \
OAC_EXAMPLE_LIVE_MODEL='<configured-model>' \
pnpm --filter @oac/parsar-example test:live
```

It executes a real model and leaves resources for inspection. Live Feishu acceptance requires a designated test bot with its scopes and an authorized chat; configuration alone does not verify delivery.

### Chat media

Web conversations preview PNG, JPEG, GIF and WebP Session Artifacts beside the reply and provide file downloads. Save outputs under `./outputs`; Markdown image/file links to their `/workspace/outputs/` paths resolve to the persisted artifacts. Other formats, including SVG, remain downloads; external image URLs are not fetched. Feishu sends the artifacts the Agent's reply names as image or file messages, replying to the message the turn answers, with a 5 MiB limit per nonempty attachment; other artifacts stay in the Web conversation. Upload keys and message identities persist across retries; failed deliveries remain in **消息记录**. Audio and video are downloadable files, without inline playback.

Bot registration returns `verification_url` and a relative `qr_url`. Show the authorization link and `![飞书授权二维码](<qr_url>)` with the returned path substituted. The PNG is generated inside Parsar, and `GET /app/feishu/provision/<bot_id>/qrcode` rejects expired or completed registrations. Web and Feishu use the same image; neither sends authorization URLs to a third-party QR service.
