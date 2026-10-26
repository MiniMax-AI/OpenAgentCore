import { AppError, text, uuid } from "./store.mjs";

// Every kind of capability an Agent can bind, in one place. A kind resolves an
// Agent's bindings and says where each one goes: into the Core Agent (tools,
// instructions) or into the Session's Environment (skills, plugins, files).

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
    resolve: (_store, agent) =>
      (agent.skill_ids || []).map((skill_id) => ({ skill_id, version: agent.skill_versions?.[skill_id] })),
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
    contribute: (mcp, out, agent) => {
      out.agent.tools.push({
        type: "mcp",
        server_label: mcp.label,
        transport: { type: "http", server_url: mcp.url },
        connection_origin: "environment",
        // MiniMax Code admits only optional MCP initialization.
        required: agent.harness !== "mcode",
        ...(mcp.credential_id ? { credential_id: mcp.credential_id } : {}),
      });
      if (mcp.vault_id) out.session.vault_ids.push(mcp.vault_id);
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
      if (body.vault_id && !uuid.test(body.vault_id)) throw new AppError(400, "凭据库标识无效。");
      if (body.credential_id && (!body.vault_id || !uuid.test(body.credential_id)))
        throw new AppError(400, "凭据标识无效。");
      const label = text(body.label, "MCP 标识", 64, true);
      if (!/^[a-zA-Z0-9_-]+$/.test(label))
        throw new AppError(400, "MCP 标识仅支持字母、数字、下划线和连字符。");
      return { url: url.href, vault_id: body.vault_id || "", credential_id: body.credential_id || "", label };
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
export function contributions(store, agent) {
  const out = {
    agent: { tools: [], instructions: [] },
    session: { skills: [], plugins: [], files: [], vault_ids: [] },
    summary: { skills: [], mcps: [], capabilities: [] },
  };
  for (const [kind, definition] of Object.entries(capabilityKinds))
    for (const entry of definition.resolve(store, agent)) {
      definition.contribute(entry, out, agent);
      if (kind === "skills") out.summary.skills.push({ id: entry.skill_id, version: entry.version || null });
      else if (kind === "mcps") out.summary.mcps.push(entry.label);
      else out.summary.capabilities.push({ name: entry.name, kind: entry.kind, version: entry.version });
    }
  out.session.vault_ids = [...new Set(out.session.vault_ids)];
  return out;
}

// Agents that bind a capability; a bound capability cannot be deleted.
export function boundAgents(store, kind, id) {
  const definition = kind === "capabilities" ? capabilityKinds.prompt : capabilityKinds[kind];
  return store.list("agents").filter((agent) => definition.bound(agent).includes(id));
}
