import { createHash, randomUUID } from "node:crypto";
import { runtimeEnvironment } from "./runtime-profile.mjs";
import { AppError, text, uuid } from "./store.mjs";
import { harnessOptions } from "../shared/harness-options.mjs";
import { turnsAPI } from "./turns.mjs";

// A warm Session matches a cold creation only when every frozen byte except the
// first input and the per-conversation metadata is the same.
const fingerprint = ({ input, metadata, ...request }) =>
  createHash("sha256").update(JSON.stringify(request)).digest("hex");
const warmAge = 12 * 3600_000;

export function sessionBot(store, agentId) {
  return store.list("feishu_bots").find((bot) => bot.agent_id === agentId);
}

export function prepareSession(store, id, body, origin = {}, warm = false) {
  const agent = store.get("agents", body.agent_id);
  if (!agent) throw new AppError(400, "请选择有效的 Agent。");
  if (agent.enabled === false)
    throw new AppError(400, "Agent 已停用，请先启用。");
  const runtime = store.get("runtimes", agent.runtime_id || "");
  if (!runtime) throw new AppError(400, "Agent 未配置环境模板。");
  const model = store.get("models", agent.model_id);
  const mcps = agent.mcp_ids.map((id) => store.get("mcps", id));
  if (!model || model.enabled === false || mcps.some((mcp) => !mcp))
    throw new AppError(400, "Agent 绑定的资源已不可用。");
  if (new Set(mcps.map((mcp) => mcp.label)).size !== mcps.length)
    throw new AppError(400, "绑定的 MCP 服务标识不能重复。");
  const botEnv = Object.entries(runtime.bot_env || {});
  const bot = sessionBot(store, agent.id);
  if (botEnv.length && !bot?.app_secret)
    throw new AppError(
      400,
      "环境模板需要飞书机器人身份，请先在飞书接入中把机器人绑定到这个 Agent。",
    );
  const environment = runtimeEnvironment(runtime);
  for (const [name, field] of botEnv)
    environment.env[name] = field === "brand" ? bot.brand || "feishu" : bot[field];
  const provider = store.get("providers", model.provider_id);
  if (!provider?.base_url?.startsWith("https://") || !provider.api_key)
    throw new AppError(
      400,
      "请先在模型页面配置所选 Provider 的 HTTPS Base URL 和 API Key。",
    );
  const modelProvider = {
    protocol: harnessOptions[agent.harness].protocol,
    base_url: provider.base_url,
    api_key: provider.api_key,
  };
  const tools = [
    { type: "web_search", mode: agent.web_search || "disabled" },
  ];
  if (agent.ask_user)
    tools.push({
      type: "function",
      name: "ask_user",
      description:
        "Ask the user a business question when essential information is missing. Never request execution permission or approval; execution is bypass-only.",
      parameters: {
        type: "object",
        properties: {
          question: { type: "string" },
          options: { type: "array", items: { type: "string" } },
        },
        required: ["question", "options"],
        additionalProperties: false,
      },
    });
  tools.push(
    ...mcps.map((mcp) => ({
      type: "mcp",
      server_label: mcp.label,
      transport: { type: "http", server_url: mcp.url },
      connection_origin: "environment",
      // MiniMax Code admits only optional MCP initialization.
      required: agent.harness !== "mcode",
      ...(mcp.credential_id ? { credential_id: mcp.credential_id } : {}),
    })),
  );
  {
    environment.skills = agent.skill_ids.map((skill_id) => ({
      type: "skill_reference",
      skill_id,
      ...(agent.skill_versions?.[skill_id]
        ? { version: agent.skill_versions[skill_id] }
        : {}),
    }));
    if (mcps.length) environment.network = { access: "enabled" };
  }
  const images = body.images || [];
  const files = body.files || [];
  if (
    !Array.isArray(images) ||
    images.length > 8 ||
    images.some(
      (url) =>
        typeof url !== "string" ||
        !/^data:image\/(png|jpeg|webp);base64,[A-Za-z0-9+/]+=*$/.test(url),
    ) ||
    JSON.stringify(images).length > 900000
  )
    throw new AppError(400, "图片格式或大小无效。");
  if (images.length && agent.harness === "mcode")
    throw new AppError(
      400,
      "当前运行配置不支持图片输入，请使用托管 Codex 或 Claude Code。",
    );
  if (
    !Array.isArray(files) ||
    files.length > 8 ||
    JSON.stringify(files).length > 7000000 ||
    files.some(
      (file) =>
        file.type !== "inline" ||
        !/^\/workspace\/inputs\/[a-zA-Z0-9_.-]+$/.test(file.path) ||
        typeof file.data !== "string" ||
        !/^[A-Za-z0-9+/]*={0,2}$/.test(file.data),
    )
  )
    throw new AppError(400, "附件格式或大小无效。");
  if (files.length && environment.type !== "openai_hosted")
    throw new AppError(400, "附件需要托管运行时。");
  if (files.length) environment.files.push(...files);
  const capabilities = (agent.capability_ids || []).map((id) => {
    const capability = store.get("capabilities", id);
    if (!capability || capability.deprecated)
      throw new AppError(400, "Agent 绑定的能力已停用或不存在。");
    const version = capability.versions.find(
      (row) =>
        row.version ===
        (agent.capability_versions?.[id] || capability.default_version),
    );
    if (!version) throw new AppError(400, "Agent 绑定的能力版本不存在。");
    return { ...capability, ...version };
  });
  for (const capability of capabilities) {
    if (capability.kind === "prompt") continue;
    if (environment.type !== "openai_hosted")
      throw new AppError(400, "Plugin 和知识文件需要托管运行时。");
    if (capability.kind === "knowledge")
      environment.files = [
        ...(environment.files || []),
        {
          type: "inline",
          path: `/workspace/knowledge/${capability.id}.md`,
          data: Buffer.from(capability.content).toString("base64"),
        },
      ];
    if (capability.kind === "plugin")
      environment.plugins = [
        ...(environment.plugins || []),
        {
          type: "inline",
          name: capability.name,
          description: capability.description,
          source: {
            type: "base64",
            media_type: "application/zip",
            data: capability.archive,
          },
        },
      ];
  }
  const instructions = [
    agent.instructions,
    ...capabilities
      .filter((row) => row.kind === "prompt")
      .map((row) => row.content),
    ...capabilities
      .filter((row) => row.kind === "knowledge")
      .map((row) => `Knowledge: ${row.name} — ./knowledge/${row.id}.md`),
  ]
    .filter(Boolean)
    .join("\n\n");
  const name = warm ? "" : text(body.name, "会话名称", 80, true);
  const input = warm ? "" : text(body.input, "消息", 100000, true);
  return {
    id,
    name,
    agent_id: agent.id,
    runtime_id: runtime.id,
    source: origin.source || "web",
    ...(origin.feishu_chat_type
      ? { feishu_chat_type: origin.feishu_chat_type }
      : {}),
    pricing: model.pricing,
    configuration: {
      agent_name: agent.name,
      agent_revision: agent.revision,
      runtime_revision: runtime.revision,
      bot_name: botEnv.length ? bot.name : undefined,
      model: model.model,
      harness: agent.harness,
      environment: runtime.environment,
      environment_name: runtime.name,
      packages: runtime.packages || { npm: [], python: [] },
      setup_configured: Boolean(runtime.setup_script),
      env_names: [
        ...Object.keys(runtime.env || {}),
        ...botEnv.map(([name]) => name),
      ],
      file_paths: (runtime.files || []).map((file) => file.path),
      skills: agent.skill_ids.map((id) => ({
        id,
        version: agent.skill_versions?.[id] || null,
      })),
      mcps: mcps.map((mcp) => mcp.label),
      capabilities: capabilities.map((row) => ({
        name: row.name,
        kind: row.kind,
        version: row.version,
      })),
    },
    created_at: Math.floor(Date.now() / 1000),
    request: {
      agent: {
        model: model.model,
        instructions,
        ...(agent.reasoning_effort || agent.reasoning_summary
          ? {
              reasoning: {
                ...(agent.reasoning_effort
                  ? { effort: agent.reasoning_effort }
                  : {}),
                ...(agent.reasoning_summary
                  ? { summary: agent.reasoning_summary }
                  : {}),
              },
            }
          : {}),
        ...(agent.service_tier ? { service_tier: agent.service_tier } : {}),
        x_agents_core: { harness: agent.harness },
        multi_agent: { enabled: false },
        text: { verbosity: agent.verbosity || "medium" },
        tools,
      },
      environment,
      ...(mcps.some((mcp) => mcp.vault_id)
        ? {
            vault_ids: [
              ...new Set(mcps.map((mcp) => mcp.vault_id).filter(Boolean)),
            ],
          }
        : {}),
      ...(input
        ? {
            input: images.length
              ? [
                  {
                    role: "user",
                    content: [
                      { type: "input_text", text: input },
                      ...images.map((image_url) => ({
                        type: "input_image",
                        image_url,
                      })),
                    ],
                  },
                ]
              : input,
          }
        : {}),
      x_agents_core: { model_provider: modelProvider },
      metadata: {
        application: "parsar-example",
        agent_id: agent.id,
        title: name,
        agent_name: agent.name,
      },
    },
  };
}

export function sessionAPI(store, core) {
  const running = new Map();
  const turns = turnsAPI(store, core);
  // Hand a prepared Session to this conversation when one matches its frozen
  // request. The request stays, so the monitor can still create the Session
  // normally if the prepared one does not start.
  const claim = (row) => {
    if (typeof row.request.input !== "string") return;
    const print = fingerprint(row.request);
    const warm = store
      .list("warm_sessions")
      .find((entry) => entry.agent_id === row.agent_id && entry.fingerprint === print);
    if (!warm) return;
    store.remove("warm_sessions", warm.id);
    setTimeout(() => void sessions.refreshWarm().catch(() => {}), 0);
    const claimed = { ...row, core_session_id: warm.core_session_id, pending_input: row.request.input };
    store.put("sessions", claimed);
    return turns.sendPrepared(claimed);
  };
  const create = async (id, body, origin) => {
    let row = store.get("sessions", id);
    if (row?.deleted_at) throw new AppError(410, "会话已删除。");
    if (row?.pending_input) return turns.sendPrepared(row);
    if (row?.core_session_id) return row;
    if (!row) {
      row = prepareSession(store, id, body, origin);
      store.put("sessions", row);
      const claimed = claim(row);
      if (claimed) return claimed;
    }
    try {
      // Freeze the request before sending. Retries, including after restart, reuse the same key and bytes.
      const session = await core(
        "/v1/agents/sessions",
        "POST",
        row.request,
        id,
      );
      if (!uuid.test(session.id))
        throw new AppError(502, "Core 返回了无效的会话。");
      // The frozen request stays until the first output, so a failed
      // Environment can be replaced with identical configuration.
      const record = { ...row, core_session_id: session.id };
      store.put("sessions", record);
      if (record.request.input !== undefined)
        turns.start(record, { id: `${id}:first`, kind: "first", input: record.request.input });
      return record;
    } catch (error) {
      if (
        error instanceof AppError &&
        [400, 401, 403, 404, 413, 422].includes(error.status)
      )
        store.remove("sessions", id);
      throw error;
    }
  };
  const sessions = (id, body, origin) => {
    if (!running.has(id))
      running.set(
        id,
        create(id, body, origin).finally(() => running.delete(id)),
      );
    return running.get(id);
  };
  // Keep one prepared Session for each Agent whose environment template asks for
  // it. Core prepares an Environment as soon as a Session exists, so the first
  // message skips setup. Overlapping refreshes are skipped.
  let refreshing = false;
  sessions.turns = turns;
  sessions.refreshWarm = async (now = Date.now()) => {
    if (refreshing) return;
    refreshing = true;
    try {
      await refresh(now);
    } finally {
      refreshing = false;
    }
  };
  const refresh = async (now) => {
    for (const agent of store.list("agents")) {
      let print;
      if (
        agent.enabled !== false &&
        store.get("runtimes", agent.runtime_id || "")?.prepare
      )
        try {
          print = fingerprint(
            prepareSession(store, randomUUID(), { agent_id: agent.id }, {}, true)
              .request,
          );
        } catch {
          print = undefined;
        }
      const entries = store
        .list("warm_sessions")
        .filter((entry) => entry.agent_id === agent.id);
      for (const entry of entries)
        if (entry.fingerprint !== print || now - entry.created_at > warmAge) {
          store.remove("warm_sessions", entry.id);
          if (entry.core_session_id)
            await core(`/v1/agents/sessions/${entry.core_session_id}`, "DELETE").catch(
              () => {},
            );
        }
      if (!print || entries.some((entry) => entry.fingerprint === print && now - entry.created_at <= warmAge))
        continue;
      const id = randomUUID();
      const { request } = prepareSession(store, id, { agent_id: agent.id }, {}, true);
      const session = await core("/v1/agents/sessions", "POST", {
        ...request,
        metadata: { ...request.metadata, title: "", warm: "true" },
      }, id);
      store.put("warm_sessions", {
        id,
        agent_id: agent.id,
        fingerprint: print,
        core_session_id: session.id,
        created_at: now,
      });
    }
    for (const entry of store.list("warm_sessions"))
      if (!store.get("agents", entry.agent_id)) {
        store.remove("warm_sessions", entry.id);
        await core(`/v1/agents/sessions/${entry.core_session_id}`, "DELETE").catch(() => {});
      }
  };
  return sessions;
}
