# Manage Parsar

Use `node /workspace/parsar.mjs --help`. `PARSAR_URL` is already set to this workspace's Parsar API. The CLI uses Node 22.13+ without dependencies and prints JSON. If the deployment needs authentication, the CLI automatically reads `PARSAR_TOKEN`; never print, inspect, copy or include that token in commands, files, links or created resources. It is a Parsar gateway credential, not a Core key. All configuration changes go through the same API and validation as the Web UI. Do not edit databases or use Core administration endpoints.

If that server is unreachable or denies access, stop and report the configured address and error. Do not scan ports, try other services, bypass authentication, or inspect Core hosts, credentials or internal APIs. Connectivity must be fixed by the deployment operator.

## Discover and reuse

```sh
node /workspace/parsar.mjs GET /app/models
node /workspace/parsar.mjs GET /app/agents
node /workspace/parsar.mjs GET /app/runtimes
node /workspace/parsar.mjs GET /app/mcps
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

## Edit without losing settings

GET `/app/agents/<id>` or `/app/runtimes/<id>` first. Modify the returned object and PUT the full object back, including its current `revision`. PUT is replacement, not a partial update. Do not reconstruct only the fields you want to change. A 409 means someone else edited it: GET again and reconcile the user's intended change. Shared template edits affect all bound Agents in new conversations; list those Agents and explain the impact. Existing conversations retain their original configuration.

Writes are not automatically retried. If the connection fails, GET the same ID to find out whether the write succeeded; never create another UUID just because a response was lost. Keep partially created resources discoverable and report them if a later step fails. Do not delete or overwrite unrelated resources.

## Verify and return links

After saving an Agent, `POST /app/runtimes/<runtime_id>/trial` starts a real environment trial. Poll `GET /app/runtimes/<runtime_id>/trial` until `state` becomes `succeeded` or `failed`. This checks environment preparation and a first reply; it does not prove business tools or the Agent's whole task work. The trial uses an enabled Agent bound to that template, which may differ when the template is shared. To test the specific Agent, open a new conversation with it and run a small representative task when requested.

Return browser links using `PARSAR_WEB_URL` when set, otherwise `PARSAR_URL`. The authenticated API gateway may not serve Web pages; never put a token in a URL:

- Agent: `/#/agents/<agent_id>`
- New conversation: `/#/new?agent=<agent_id>`
- Environment templates: `/#/runtimes`

Report saved resources, reused resources and the exact validation performed. Never claim that a successful PUT means the Agent was tested successfully.
