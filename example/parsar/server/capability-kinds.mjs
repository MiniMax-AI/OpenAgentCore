import { AppError, text } from "./store.mjs";
import { botVariables, environmentVariableName } from "../shared/bot-bindings.mjs";
import { harnessOptions } from "../shared/harness-options.mjs";

// Every kind of capability an Agent can bind, in one place. A kind resolves an
// Agent's bindings, says which credentials each needs, and where each one goes:
// into the Core Agent (tools, instructions) or into the Session's Environment
// (skills, plugins, files, variables).

// Variable names a capability needs the Agent to supply from a secret.
export function credentialNames(value) {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > 20 || value.some((name) => !environmentVariableName(name)))
    throw new AppError(400, "凭据需求是有效的环境变量名，最多 20 个，且不能使用运行环境保留的名称。");
  return [...new Set(value)];
}

const libraryEntry = (store, agent, id) => {
  const capability = store.get("capabilities", id);
  if (!capability || capability.deprecated)
    throw new AppError(400, "Agent 绑定的能力已停用或不存在。");
  const version = capability.versions.find(
    (row) => row.version === (agent.capability_versions?.[id] || capability.default_version),
  );
  if (!version) throw new AppError(400, "Agent 绑定的能力版本不存在。");
  return { ...capability, ...version };
};
const library = (kind, contribute) => ({
  bound: (agent) => agent.capability_ids || [],
  resolve: (store, agent) =>
    (agent.capability_ids || [])
      .map((id) => libraryEntry(store, agent, id))
      .filter((row) => row.kind === kind),
  contribute,
});

export const capabilityKinds = {
  skills: {
    bound: (agent) => agent.skill_ids || [],
    // Skills live in Core; the binding names a Skill and optionally a version.
    // Parsar keeps only the credentials a Skill needs.
    resolve: (store, agent) =>
      (agent.skill_ids || []).map((skill_id) => ({
        skill_id,
        version: agent.skill_versions?.[skill_id],
        credentials: store.get("skill_credentials", skill_id)?.credentials || [],
      })),
    contribute: (entry, out) =>
      out.session.skills.push({ type: "skill_reference", skill_id: entry.skill_id, ...(entry.version ? { version: entry.version } : {}) }),
  },
  mcps: {
    bound: (agent) => agent.mcp_ids || [],
    resolve: (store, agent) => {
      const mcps = (agent.mcp_ids || []).map((id) => store.get("mcps", id));
      if (mcps.some((mcp) => !mcp)) throw new AppError(400, "Agent 绑定的资源已不可用。");
      if (new Set(mcps.map((mcp) => mcp.label)).size !== mcps.length)
        throw new AppError(400, "绑定的 MCP 服务标识不能重复。");
      return mcps;
    },
    // An MCP that needs authentication uses the Vault credential the Agent chose for it.
    contribute: (mcp, out, agent) => {
      const credential = mcp.requires_credential && agent.mcp_credentials?.[mcp.id];
      if (mcp.requires_credential && !credential)
        throw new AppError(400, `请在 Agent 中为 MCP「${mcp.name}」选择凭据。`);
      out.agent.tools.push({
        type: "mcp",
        server_label: mcp.label,
        transport: { type: "http", server_url: mcp.url },
        connection_origin: "environment",
        // Required only where the Harness declares required HTTP MCP servers.
        required: harnessOptions[agent.harness]?.mcp_http_required === true,
        ...(credential ? { credential_id: credential.credential_id } : {}),
      });
      if (credential) out.session.vault_ids.push(credential.vault_id);
    },
    // An HTTPS address without credentials; authentication comes from a Vault credential.
    validate: (body) => {
      let url;
      try {
        url = new URL(body.url);
      } catch {
        throw new AppError(400, "请填写有效的 MCP URL。");
      }
      if (url.protocol !== "https:" || url.username || url.password || url.hash || url.search)
        throw new AppError(400, "MCP 使用不含凭据的 HTTPS 地址。");
      const label = text(body.label, "MCP 标识", 64, true);
      if (!/^[a-zA-Z0-9_-]+$/.test(label))
        throw new AppError(400, "MCP 标识仅支持字母、数字、下划线和连字符。");
      return { url: url.href, label, requires_credential: body.requires_credential === true };
    },
  },
  prompt: library("prompt", (row, out) => out.agent.instructions.push(row.content)),
  knowledge: library("knowledge", (row, out) => {
    out.agent.instructions.push(`Knowledge: ${row.name} — ./knowledge/${row.id}.md`);
    out.session.files.push({
      type: "inline",
      path: `/workspace/knowledge/${row.id}.md`,
      data: Buffer.from(row.content).toString("base64"),
    });
  }),
  plugin: library("plugin", (row, out) =>
    out.session.plugins.push({
      type: "inline",
      name: row.name,
      description: row.description,
      source: { type: "base64", media_type: "application/zip", data: row.archive },
    }),
  ),
};

// What an Agent's bindings add to its Core Agent and to each of its Sessions.
export const boundBot = (store, agent) =>
  store.list("feishu_bots").find((bot) => bot.agent_id === agent.id && bot.app_secret);

// What needs credentials: every bound capability and the Agent's environment template.
function needers(store, agent) {
  const out = [];
  for (const [kind, definition] of Object.entries(capabilityKinds))
    for (const entry of definition.resolve(store, agent)) out.push({ kind, entry });
  const runtime = store.get("runtimes", agent.runtime_id || "");
  if (runtime) out.push({ kind: "runtimes", entry: runtime });
  return out;
}

// What an Agent's bindings add to its Core Agent and to each of its Sessions.
// A bound bot's identity reaches every conversation; any other variable comes
// from the secret the Agent chose for it.
export function contributions(store, agent) {
  const out = {
    agent: { tools: [], instructions: [] },
    session: { skills: [], plugins: [], files: [], vault_ids: [], env: {} },
    summary: { skills: [], mcps: [], capabilities: [] },
  };
  const bot = boundBot(store, agent);
  for (const { kind, entry } of needers(store, agent)) {
    if (kind !== "runtimes") capabilityKinds[kind].contribute(entry, out, agent);
    for (const name of entry.credentials || []) {
      const label = entry.name || entry.skill_id;
      if (Object.hasOwn(botVariables, name)) {
        if (!bot) throw new AppError(400, `「${label}」需要飞书机器人身份，请先在飞书接入中给这个 Agent 绑定机器人。`);
        continue;
      }
      const secret = store.get("secrets", agent.credentials?.[name] || "");
      if (!secret) throw new AppError(400, `请在 Agent 中为「${label}」所需的 ${name} 选择密钥。`);
      out.session.env[name] = secret.value;
    }
    if (kind === "skills") out.summary.skills.push({ id: entry.skill_id, version: entry.version || null });
    else if (kind === "mcps") out.summary.mcps.push(entry.label);
    else if (kind !== "runtimes") out.summary.capabilities.push({ name: entry.name, kind: entry.kind, version: entry.version });
  }
  if (bot)
    for (const [name, field] of Object.entries(botVariables))
      out.session.env[name] = field === "brand" ? bot.brand || "feishu" : bot[field];
  out.session.vault_ids = [...new Set(out.session.vault_ids)];
  return out;
}

// The secrets and MCP credentials an Agent must choose for its bindings now.
export function requiredCredentials(store, agent) {
  const env = new Set(), mcps = new Set();
  for (const { kind, entry } of needers(store, agent)) {
    for (const name of entry.credentials || []) if (!Object.hasOwn(botVariables, name)) env.add(name);
    if (kind === "mcps" && entry.requires_credential) mcps.add(entry.id);
  }
  return { env, mcps };
}

// Agents that bind a capability; a bound capability cannot be deleted.
export function boundAgents(store, kind, id) {
  const definition = kind === "capabilities" ? capabilityKinds.prompt : capabilityKinds[kind];
  return store.list("agents").filter((agent) => definition.bound(agent).includes(id));
}
