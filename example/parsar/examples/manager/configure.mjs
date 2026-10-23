import { readFile } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { createParsarClient } from "../../cli.mjs";

// An ordinary API client assembles this Agent; the server has no manager mode.
const args = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
  const name = process.argv[i], value = process.argv[i + 1];
  if (!['--url', '--agent-url', '--web-url', '--model', '--harness', '--agent-id', '--environment-id'].includes(name) || !value || args.has(name))
    throw new Error("Usage: node configure.mjs --url URL --model MODEL_ID [--agent-url URL] [--web-url URL] [--harness codex|claude_sdk] [--agent-id UUID --environment-id UUID]");
  args.set(name, value);
}
if (!args.has('--url') || !args.has('--model')) throw new Error("--url and --model are required.");
const harness = args.get('--harness') || 'codex';
if (!['codex', 'claude_sdk'].includes(harness)) throw new Error("This example requires the Node runtime in Codex or Claude Code.");
const token = process.env.PARSAR_TOKEN;
const api = createParsarClient(args.get('--url'), { token });
const agentURL = args.get('--agent-url') || args.get('--url');
createParsarClient(agentURL, { token });
const webURL = new URL(args.get('--web-url') || args.get('--url')).origin;
createParsarClient(args.get('--web-url') || args.get('--url'));
const agentId = args.get('--agent-id') || randomUUID();
const runtimeId = args.get('--environment-id') || randomUUID();
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/;
if (!uuid.test(agentId) || !uuid.test(runtimeId)) throw new Error("Resource IDs must be UUIDs.");
const models = await api('GET', '/app/models');
if (!models.some((model) => model.id === args.get('--model') && model.enabled !== false))
  throw new Error("Choose an enabled model from /app/models.");
// Refuse to overwrite existing resources. To update, use the normal GET/PUT flow.
for (const [kind, id] of [['agents', agentId], ['runtimes', runtimeId]]) {
  if ((await api('GET', `/app/${kind}`)).some((row) => row.id === id))
    throw new Error(`${kind}/${id} already exists. Inspect it before editing.`);
}
console.error(JSON.stringify({ agent_id: agentId, environment_id: runtimeId, recovery: 'If a write fails, GET these IDs before retrying. Existing resources are never deleted automatically.' }));
await api('PUT', `/app/runtimes/${runtimeId}`, {
  name: 'Parsar 管理环境',
  description: '通过 Parsar CLI 管理当前工作空间的配置。',
  environment: 'openai_hosted',
  env: { PARSAR_URL: agentURL, PARSAR_WEB_URL: webURL, ...(token === undefined ? {} : { PARSAR_TOKEN: token }) },
  files: [
    { path: '/workspace/parsar.mjs', content: await readFile(new URL('../../cli.mjs', import.meta.url), 'utf8') },
    { path: '/workspace/PARSAR.md', content: await readFile(new URL('./GUIDE.md', import.meta.url), 'utf8') },
  ],
});
await api('PUT', `/app/agents/${agentId}`, {
  name: 'parsar他爹',
  description: '用自然语言创建和调整 Agent、环境模板，复用已有资源并试运行。',
  harness,
  model_id: args.get('--model'),
  runtime_id: runtimeId,
  instructions: '你帮助用户管理 Parsar。开始工作前阅读 /workspace/PARSAR.md，使用 node /workspace/parsar.mjs 调用现有接口。先了解用户目标并查询已有资源，尽量复用；按用户要求直接创建或编辑，不增加重复资源。修改共享资源前说明影响，只修改用户委托的内容。不得读取或输出凭据，不得把管理 CLI 或管理地址放进你创建的普通业务 Agent。缺少必要业务信息才提问。完成后给出已保存资源的网页链接，说明哪些经过试运行；不可把仅保存配置称为运行成功。',
  skill_ids: [], mcp_ids: [], capability_ids: [],
  ask_user: true, enabled: true,
});
console.log(JSON.stringify({ agent_id: agentId, environment_id: runtimeId, agent_url: `${webURL}/#/agents/${agentId}` }, null, 2));
