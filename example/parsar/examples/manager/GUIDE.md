# Manage Parsar

Use `node /workspace/parsar.mjs --help`. `PARSAR_URL` is already set to this workspace's Parsar API. The CLI uses Node 22.13+ without dependencies and prints JSON. If the deployment needs authentication, the CLI automatically reads `PARSAR_TOKEN`; never print, inspect, copy or include that token in commands, files, links or created resources. It is a Parsar gateway credential, not a Core key. All configuration changes go through the same API and validation as the Web UI. Do not edit databases or use Core administration endpoints.

If that server is unreachable or denies access, stop and report the configured address and error. Do not scan ports, try other services, bypass authentication, or inspect Core hosts, credentials or internal APIs. Connectivity must be fixed by the deployment operator.

## Discover and reuse

```sh
node /workspace/parsar.mjs GET /app/models
node /workspace/parsar.mjs GET /app/agents
node /workspace/parsar.mjs GET /app/runtimes
node /workspace/parsar.mjs GET /app/mcps
node /workspace/parsar.mjs GET /app/feishu/bots
node /workspace/parsar.mjs GET /app/capabilities
node /workspace/parsar.mjs GET '/v1/skills?limit=100'
```

Reuse suitable existing models, Skills and templates. Never invent resource IDs. For paginated Core lists, continue with `after=<last id>` while `has_more` is true. Model entries contain `provider_id`; `/app/providers` exposes provider names and URLs, not their keys. `codex` uses Responses; `claude_sdk` and `mcode` use Anthropic. Choose an existing working combination or ask when its compatibility is unknown. Do not change provider credentials to make an example work.

## Create an environment template

Generate one ID and keep it in your notes before sending the write:

```sh
node /workspace/parsar.mjs uuid
```

Write a JSON file, then call `PUT /app/runtimes/<that UUID> --file environment.json`:

```json
{
  "name": "Excel 分析环境",
  "description": "读取 Excel，生成分析报告。",
  "environment": "openai_hosted",
  "packages": { "npm": [], "python": ["openpyxl"] },
  "setup_script": "mkdir -p /workspace/outputs",
  "env": {},
  "bot_env": {},
  "files": [],
  "prepare": false
}
```

Use only needed packages and files. Initial files are `{ "path": "/workspace/name", "content": "text" }`. Setup runs in the hosted workspace before the Agent starts; it is executable code, so review it as part of the requested configuration. Keep persistent files under `/workspace`; HOME is not persistent. Environment preparation may be reused. Never put the management CLI or its address in ordinary business templates unless the user explicitly asks for management access.

Environment variable values are write-only: GET returns `null`, and PUT with `null` keeps the saved value. Retain untouched names. Removing a name removes its value. Do not ask users to paste secrets into chat; direct them to the existing configuration UI. A template with `bot_env` needs an Agent bound to a Feishu bot before it can run.

## Create an Agent

Generate another UUID. Use real `model_id` and `runtime_id` values in an `agent.json` file, then call `PUT /app/agents/<UUID> --file agent.json`:

```json
{
  "name": "Excel 分析助手",
  "description": "分析上传的 Excel，生成可下载的报告。",
  "harness": "codex",
  "model_id": "<existing model UUID>",
  "runtime_id": "<environment template UUID>",
  "instructions": "根据用户目标分析上传的 Excel。缺少关键业务信息时提问。报告注明数据来源和计算方法，交付物写入 ./outputs。",
  "skill_ids": [],
  "mcp_ids": [],
  "capability_ids": [],
  "ask_user": true,
  "enabled": true
}
```

An Agent is a reusable configuration, not a live session. Its environment template is required. `skill_ids` reference `/v1/skills`; `mcp_ids` and `capability_ids` reference the product lists. Omit optional execution settings to keep native defaults. Creation does not prove a model or tool works.

## Guide Feishu bot onboarding

When a user asks to open or connect a Feishu bot, guide the whole setup rather than merely linking to settings. Use only Parsar's `/app/feishu/*` API through this CLI. The user performs Feishu login, scanning, authorization, application publication and any administrator approval. Never request their password or App Secret in chat, read credentials from the environment, or call Feishu's registration service directly.

### Choose the Agent and reuse a bot

Read `/app/agents`, `/app/runtimes` and `/app/feishu/bots`. Identify the intended business Agent from the user's goal; if it does not exist, create it using the workflow above. Do not bind the management Agent unless the user explicitly wants management access through that bot. Each bot binds one Agent and each Agent binds at most one bot. Reuse the intended Agent's existing bot; do not replace another binding, rotate credentials or create a duplicate to resolve a conflict. Ask only for unresolved choices such as which Agent or whether to use an existing Feishu application.

For a new setup, default to private chat only and tell the user. Enable group chat only when requested: it permits replies to mentions in any group the bot joins, not a list of selected group IDs. Preserve the chat choices of an existing bot unless the user asks to change them. Binding a bot supplies that bot's identity to new Agent conversations, including Web conversations when the environment maps `bot_env`; do not copy credentials into files or ordinary Agent instructions.

### Register a new application

Generate and retain one UUID as `bot_id` before starting. Start registration with `POST /app/feishu/provision/<bot_id>` and JSON `{"action":"begin","name":"<chosen bot name>"}`. The response has `status`, `verification_url`, `qr_url`, `expires_at` (Unix milliseconds) and `interval` (seconds). Present the returned `verification_url` unchanged as a clickable authorization link, followed by `![飞书授权二维码](<qr_url>)` using the exact relative `qr_url` returned (replace the placeholder, without angle brackets). Both Web and Feishu display this as an image. Explain when it expires and ask the user to scan/confirm. Never invent an image URL, encode the link yourself or substitute an ASCII QR. Show it only to the requesting user; do not send the link to a third-party QR image service. Do not substitute the general **扫码创建** button: that starts another registration with a different ID.

Keep the same `bot_id` while waiting. End the reply with the link and QR image, then resume when the user returns; do not spin in a shell polling loop while waiting for a person. Once the user confirms, send `POST /app/feishu/provision/<bot_id>` with `{"action":"poll"}`. A user's confirmation is not proof of success: continue only when the returned `status` is `success`. While `pending`, wait at least the returned `interval` before another poll, respect any increased interval, and tell the user what is still pending. The server saves the registration across restarts. A successful poll creates the bot with the same ID and leaves it disabled; it returns neither the device code nor the App Secret.

For a registration 409 (busy), wait the polling interval and poll the same ID; there is no GET registration-state endpoint. If a response is lost, poll the retained ID first. If polling reports that the registration does not exist, repeat `begin` with that same ID. Do not create a different ID for an uncertain request. When `expires_at` has passed, `begin` with the same ID can obtain a new authorization link. For `access_denied` or another terminal error, explain the result and stop; if the user requests a fresh attempt before expiration, use a new ID only after confirming the previous registration failed and no bot was created. Never repeatedly retry denied authorization or claim that opening a link completed it.

### Connect an existing application

Send the user to **飞书接入 → 添加机器人 → 已有应用** at `PARSAR_WEB_URL/#/feishu` (use the browser-link rule below). They enter the App ID and App Secret there. After they return, read `/app/feishu/bots` again and identify the saved bot by its name or App ID; do not ask for the secret. If it already exists, reuse its ID and saved credentials without asking the user to enter them again.

### Bind and enable

After registration succeeds or the existing application is saved, GET `/app/feishu/bots/<bot_id>` immediately before editing. PUT the full returned object to the same path, retaining its current `revision`, `app_id`, `brand`, name and other settings. Change only the intended `agent_id`, agreed `direct`/`groups` settings and `enabled: true`. Omit `app_secret`; the server retains it. Require `has_app_secret: true`, an existing Agent with an environment template, and at least one chat channel before enabling. On 409, read again and reconcile; never overwrite a concurrent edit. A GET for an unknown bot ID returns an empty draft with revision 0, so it does not prove registration succeeded.

### Check connection and verify delivery

Read `/app/feishu/bots` for the selected bot's `health.state` (the single-bot GET does not include health). `connected` confirms the connection, not message delivery. If it is anything other than `connected` (including `reconnecting` or `core_unavailable`), explain that setup is not ready and direct the user to **飞书接入**. For application-side setup, guide the user to check the Feishu developer console for bot capability, long-connection event subscription, `im.message.receive_v1`, required messaging permissions, application publication and availability. Do not assume a scan granted every business permission; requests to access documents or other resources require the relevant Skills and permissions separately.

Ask the user to send a harmless test message in private chat, or mention the bot in the intended group if group chat was requested. Do not send unsolicited messages to other people. Before that test, record the current delivery IDs; afterwards read `/app/feishu/deliveries`, match the bot's `app_id` and the new inbound record's `thread_id`, and inspect the new inbound and outbound records. An existing private chat may reuse its thread ID. Claim end-to-end success only after the new outbound record is `delivered` and its conversation has a completed reply to this test message, and the user confirms seeing the reply (follow its product `session_id` to `/app/sessions/<id>` and the referenced public Core Session/Turns/Items). Old successful deliveries, a saved configuration, a successful environment trial or a connected WebSocket are not proof of this test. If no matching test arrives, report “configured/connected, waiting for your test message.” Link to `/#/feishu`, the Agent and the verified conversation when available; report failures without exposing secrets.

## Edit without losing settings

GET `/app/agents/<id>` or `/app/runtimes/<id>` first. Modify the returned object and PUT the full object back, including its current `revision`. PUT is replacement, not a partial update. Do not reconstruct only the fields you want to change. A 409 means someone else edited it: GET again and reconcile the user's intended change. Shared template edits affect all bound Agents in new conversations; list those Agents and explain the impact. Existing conversations retain their original configuration.

Writes are not automatically retried. If the connection fails, GET the same ID to find out whether the write succeeded; never create another UUID just because a response was lost. Keep partially created resources discoverable and report them if a later step fails. Do not delete or overwrite unrelated resources.

## Verify and return links

After saving an Agent, `POST /app/runtimes/<runtime_id>/trial` starts a real environment trial. Poll `GET /app/runtimes/<runtime_id>/trial` until `state` becomes `succeeded` or `failed`. This checks environment preparation and a first reply; it does not prove business tools or the Agent's whole task work. The trial uses an enabled Agent bound to that template, which may differ when the template is shared. To test the specific Agent, open a new conversation with it and run a small representative task when requested.

Return browser links using `PARSAR_WEB_URL` when set, otherwise `PARSAR_URL`. The authenticated API gateway may not serve Web pages; never put a token in a URL:

- Agent: `/#/agents/<agent_id>`
- New conversation: `/#/new?agent=<agent_id>`
- Environment templates: `/#/runtimes`
- Feishu setup: `/#/feishu`

Report saved resources, reused resources and the exact validation performed. Never claim that a successful PUT means the Agent was tested successfully.

## Share images and files

Save generated files under `/workspace/outputs/` so Core publishes them as Session Artifacts. Use Markdown `![description](/workspace/outputs/picture.png)` for images and `[filename](/workspace/outputs/report.pdf)` for files. Web previews PNG, JPEG, GIF and WebP and offers downloads for other formats. Feishu delivers each artifact as an image or file message; each attachment must be nonempty and at most 5 MiB. Other formats, including SVG, are files, not inline previews. Do not embed arbitrary remote image URLs or data URIs: save the image as an artifact first. Authorization QR images use the returned `qr_url` instead and never require copying tokens into files. A failed attachment delivery is visible in **飞书接入 → 消息记录**; do not claim the user received it without confirmation.
