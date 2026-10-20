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
| **资源库** | Building blocks, grouped by what uses them: models for execution profiles; Skills, MCP servers, Plugins, knowledge and instruction fragments for Agents; credentials for MCP. Resources are shared: editing one affects everything that uses it, and an item in use cannot be removed. |
| **执行配置** | How an Agent runs, like a local Codex profile: Harness, model, reasoning effort, web search and, under **高级**, reasoning summary, verbosity and service tier. Only values the selected Harness declares are offered. |
| **环境模板** | The prepared workspace: npm and Python packages, a Bash setup script, environment variables, Feishu bot variables and initial files. |
| **Agent** | What people talk to: name, description, instructions, one required execution profile, one required environment template, capabilities and behavior switches. **复制** creates an independent copy. |
| **飞书机器人** | One bot is bound to at most one Agent. It routes its chats to that Agent and is the Agent’s Feishu identity in every conversation, including Web chats. |
| **对话** | One public Core Session with its own Environment. It freezes the Agent and environment configuration when it starts. |

Configuration lives only in templates and Agents; a conversation never overrides it. Edits affect only new conversations. When the Agent, its execution profile or its environment template changes, an existing conversation says so and offers **用新配置开始新对话**.

## Web chat

**新对话** opens the home page: choose an Agent, describe the work and send. The sidebar lists recent conversations; **全部对话** opens the full list with search, Agent, source and date filters, plus **运行记录**, **用量** and **审计日志**. Date filters use the browser’s local dates. Usage totals cover all matching records; missing usage remains unknown. Optional model prices are USD per million input/output tokens, frozen when a conversation starts; costs are estimates, not provider bills.

A conversation streams replies and tool activity, groups work steps, navigates user turns with the turn rail, supports follow-ups and cancellation and reopens durable history. Messages sent while the Agent is working reach the running task; answer business questions on their card. Enter sends; Shift+Enter adds a line. Drafts survive reloads in the same browser tab. The folder button uploads inputs up to 5 MiB while idle, inserts their path into the draft and downloads Artifacts after the live workspace is gone; ask the Agent to save deliverables under `./outputs`. The info button shows the Agent, model, Harness, environment template, Feishu identity, source and the configuration recorded at creation, without variable values, script contents or credentials. When a creation response is lost, the conversation page offers **恢复创建**, which reuses the frozen request and idempotency key.

Optional business questions use the declared `ask_user` function. They appear in the conversation and return through public function-result events with stable request IDs. This is information gathering, not execution approval.

**设置** changes the workspace name, the Chinese/English language and the light, dark or system theme.

## Agents and resources

**Agent** lists Agents as cards with their execution profile, environment template and bound Feishu bot. The Agent page groups **基本信息**, **执行与环境**, **指令**, **能力** and **行为**. **主指令** is the Agent’s own instruction text; optional **指令片段** are appended in selection order to produce one system prompt. Instruction fragments, Plugins and knowledge share an eight-resource limit; selectors prevent exceeding it. In **执行配置**, the Harness narrows every option to its declared values ([Harness capabilities](../../contracts/agents-api/harness-capabilities.md)); switching Harness resets values the new one does not accept and says which. Empty options keep the Harness default; web search defaults to **关闭**. Web conversations and Feishu step records show searches as **搜索网页**. Creating or editing an execution profile, model, Skill, MCP, environment template or capability from the Agent page keeps unsaved fields, and a newly created resource is selected automatically. Unsaved Agent edits are restored when you return to the page in the same browser tab, and **保存** points to any missing required field.

**模型** groups model IDs under Providers. Fetch a Provider’s OpenAI-shaped `GET /v1/models` catalog or enter IDs manually. Keys remain server-side; blank edits preserve the saved key. Discovery sends only that Provider’s key and never follows redirects. Every Harness uses the selected HTTPS Provider: Responses for Codex, Anthropic for Claude Code and MiniMax Code. Unsupported combinations fail explicitly; consult the [Harness coverage ledger](../../contracts/agents-api/index.md).

- **Skills** supports SKILL.md creation, ZIP upload, versions, default selection, per-Agent version pins and bounded file previews. **浏览目录** searches the skills.sh index; previews use the pinned `skills@1.5.23` CLI in a temporary directory and the normal public Skill upload. This requires `npx` and network access on the Parsar host. Imported archives are bounded to 32 MiB and 1,000 files.
- **MCP** provides a static service directory and HTTPS endpoint configuration. Optional Vault credentials use official public MCP tools with environment origin.
- **凭据** creates Vaults and write-only bearer credentials. **OAuth 授权** uses the maintained MCP SDK for discovery, dynamic client registration, issuer binding and PKCE, then stores the token through public Vault APIs. OAuth tokens never return to the browser.
- Instruction fragments and knowledge have immutable numbered versions, defaults, pins and deprecation. Knowledge versions become initial workspace files. Standard Claude/Codex Plugin ZIPs become public Environment Plugins; declared `.mcp.json` servers are listed. Executables required by stdio servers must exist in the environment.

**环境模板** runs only in Core-managed workspaces. Each new conversation installs files and packages and completes setup before starting the Agent; a setup failure stops execution. Initial files use canonical `/workspace/` paths; scripts run from the workspace. Variable values are write-only in product responses: `null` preserves an existing value, a string replaces it and removing the name deletes it. **飞书机器人变量** map variable names to the bound bot’s **App ID**, **App Secret** or region; a conversation with such a template requires the Agent to have a bound bot. Parsar sends templates inline through the public Session Environment contract and does not create a Core Template resource.

**提前准备，新对话无需等待安装** keeps one prepared Session for each Agent that uses the template. Parsar creates it without input, so Core prepares its Environment ahead of time, and the next Web or Feishu conversation takes it over with its first message; the conversation keeps its own title. A prepared Session is replaced when the Agent, its execution profile, environment template, bot or model provider changes, after 12 hours, or once taken; turning the switch off discards it. A first message with images or files starts an ordinary Session. Each prepared Session holds a workspace while it waits.

## Feishu

**飞书接入** shows one card per bot. **添加机器人** offers **扫码创建**, which provisions an application through Feishu’s device flow, or **已有应用** with an App ID and App Secret. A new bot stays disabled until configured. Secrets stay in the restricted local database. The official SDK owns authentication, normalization and long connections; no inbound public callback is needed.

1. On the card, choose the **Agent**, turn on **私聊** and/or **群聊**, then **保存并启用**. Enabling requires credentials, an Agent with an environment template and at least one of the two. With **群聊** on, the bot answers mentions in every group it has joined; adding the bot to a group is all that is needed. Who can add the bot is controlled by the app’s availability in the developer console.
2. In the developer console, enable the bot, long-connection event delivery and `im.message.receive_v1`, publish the app and add it to the target groups. Grant mention receipt and send-as-bot permissions; direct messages, message resources and reactions need their own scopes. A missing reaction permission does not discard a message.
3. Private chat keeps one conversation per sender until `/new` or `/reset`. In groups, mentioning the bot starts a topic and each topic is one conversation shared by everyone who replies in it. Follow-ups without a mention arrive only when the app has the sensitive “read all group messages” scope (`im:message.group_msg`); otherwise mention the bot again in the topic. A topic reply that mentions only other people is addressed to them and is ignored. Other bots and `@all` are ignored, as are groups while **群聊** is off and private chats while **私聊** is off. Changing the bound Agent affects new topics and new private conversations.
4. Text, rich posts, images, files, audio and video, quoted messages and merged forwards are normalized before dispatch. Attachment limits are eight resources and 5 MiB total. Follow-up files wait until the conversation is idle. Quoted messages are read up to four levels; earlier group messages are not read.
5. `/cancel`, `/answer <call_id> <text>`, `/new` and `/help` work in the same thread. A plain reply answers a single pending business question. Cancellation takes priority over queued follow-ups and recovers an uncertain creation before cancelling it.

Accepted messages are committed to SQLite before the WebSocket ACK; request IDs and frozen payloads survive retries and restarts. The worker edits one progress card in place and finishes it with the result. Progress uses five-second polling. Complete history and Artifacts remain in the Web conversation. Settled threads stop polling after five minutes and wake on new messages. **消息记录** lists the latest 100 deliveries plus unresolved ones; **重试** keeps the original identity. A failed inbound message blocks later messages in that thread. Uncertain outbound sends are not replayed beyond 55 minutes. Disabling a bot pauses delivery without cancelling Core work.

### Bot identity

The Agent acts as its bound bot. With **飞书机器人变量**, Parsar puts the bot’s App ID and App Secret into the conversation’s Environment variables. The Feishu case maps them to `FEISHU_*` names and its setup script runs `lark-cli config init --app-secret-stdin`, so the CLI mints and refreshes tenant tokens itself and long conversations keep working. Do not map them to `LARKSUITE_CLI_APP_ID` or `LARKSUITE_CLI_APP_SECRET`: those switch lark-cli to environment credentials, which require a ready-made tenant token. Anyone who can talk to the bot can use the bot’s permissions, and tools in the Environment can read the secret. Grant the bot only the scopes its Agent needs and prefer one bot per Agent. A rotated secret applies to new conversations only.

### Feishu workspace case

[The Feishu workspace](examples/feishu/) is assembled from ordinary templates. [environment.json](examples/feishu/environment.json) declares packages, bot variables and files, [setup.sh](examples/feishu/setup.sh) installs the checksum-verified official CLI binary and configures it with the bot credentials, [profile.json](examples/feishu/profile.json) defines a Codex execution profile and [agent.json](examples/feishu/agent.json) the Agent. Configure a model, import the official `lark-shared` Skill and the business Skills you need, add a bot, then run:

```bash
node example/parsar/examples/feishu/configure.mjs --url http://localhost:3001 --model MODEL_ID --bot BOT_ID --skills SHARED_SKILL_ID,DOMAIN_SKILL_ID
```

`--bot` is the Parsar bot ID, not the Feishu App ID. The script creates the environment template, execution profile and Agent through the same `/app/runtimes`, `/app/profiles` and `/app/agents` operations as the UI, binds the bot and prints the Agent link. The first message can ask for `lark-cli api GET /open-apis/bot/v3/info --as bot --json`. The node must resolve and reach the Feishu open platform; templates cannot repair node DNS or network policy.

## Ownership and local API

Core’s Project key stays server-side. Successful conversation creation leaves a Core reference and product metadata; pending creation retains the frozen public request until confirmed. Core remains authoritative for execution, Items, Turns, Skills, Vaults and Artifacts. SQLite stores product configuration, capability versions, Feishu delivery state and temporary OAuth/registration state. Files live under `~/.oac/data/parsar-example/`; `OAC_EXAMPLE_DATA_DIR` overrides this with an absolute path. Core origin and Project-key fingerprint select the file. Back it up with the server stopped. Provider and bot secrets reside in it; the Core Project key does not. Builds and caches use `${OAC_DEV_HOME:-$HOME/.oac}`.

| Product route family | Caller and credential |
| --- | --- |
| `/app/providers`, `/app/models`, `/app/profiles`, `/app/agents`, `/app/runtimes`, `/app/mcps`, `/app/sessions` | Same-origin local UI; the Feishu worker reuses Session creation in-process |
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
