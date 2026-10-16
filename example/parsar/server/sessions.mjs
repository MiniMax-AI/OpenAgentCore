import { runtimeEnvironment } from "./runtime-profile.mjs";
import { sessionRestriction } from "../shared/session-profile.mjs";
import { AppError, text, uuid } from "./store.mjs";

export function prepareSession(store, id, body, owner = { kind: "local" }) {
  const agent = store.get("agents", body.agent_id);
  const runtime = store.get("runtimes", body.runtime_id);
  if (!agent || !runtime)
    throw new AppError(400, "请选择有效的 Agent 和运行时。");
  if (agent.enabled === false)
    throw new AppError(400, "Agent 已停用，请先启用。");
  const model = store.get("models", agent.model_id);
  const mcps = agent.mcp_ids.map((id) => store.get("mcps", id));
  if (!model || model.enabled === false || mcps.some((mcp) => !mcp))
    throw new AppError(400, "Agent 绑定的资源已不可用。");
  if (new Set(mcps.map((mcp) => mcp.label)).size !== mcps.length)
    throw new AppError(400, "绑定的 MCP 服务标识不能重复。");
  if (
    Object.keys(runtime.connection_env || {}).length &&
    !agent.connection_requirement
  )
    throw new AppError(
      400,
      "环境模板需要账号连接，请在 Agent 模板中选择所需用户连接。",
    );
  if (
    agent.connection_requirement &&
    !Object.keys(runtime.connection_env || {}).length
  )
    throw new AppError(400, "请在环境模板中配置连接变量映射。");
  const environment = runtimeEnvironment(runtime);
  const restriction = sessionRestriction(agent, runtime);
  if (restriction) throw new AppError(400, restriction);
  let modelProvider;
  if (agent.harness !== "mcode") {
    const provider = store.get("providers", model.provider_id);
    if (!provider?.base_url?.startsWith("https://") || !provider.api_key)
      throw new AppError(
        400,
        "请先在模型页面配置所选 Provider 的 HTTPS Base URL 和 API Key。",
      );
    modelProvider = {
      protocol: agent.harness === "codex" ? "responses" : "anthropic",
      base_url: provider.base_url,
      api_key: provider.api_key,
    };
  }
  const tools = [{ type: "web_search", mode: "disabled" }];
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
      required: true,
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
  const name = text(body.name, "会话名称", 80, true);
  const input = text(body.input, "消息", 100000, true);
  return {
    id,
    name,
    agent_id: agent.id,
    runtime_id: runtime.id,
    pricing: model.pricing,
    configuration: {
      agent_name: agent.name,
      model: model.model,
      harness: agent.harness,
      environment: runtime.environment,
      environment_name: runtime.name,
      packages: runtime.packages || { npm: [], python: [] },
      setup_configured: Boolean(runtime.setup_script),
      env_names: [
        ...Object.keys(runtime.env || {}),
        ...Object.keys(runtime.connection_env || {}),
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
    connection_env: runtime.connection_env || {},
    connection_requirement: agent.connection_requirement,
    connection_owner: agent.connection_requirement ? owner : undefined,
    created_at: Math.floor(Date.now() / 1000),
    request: {
      agent: {
        model: model.model,
        instructions,
        x_agents_core: { harness: agent.harness },
        multi_agent: { enabled: false },
        text: { verbosity: "medium" },
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
      ...(modelProvider
        ? { x_agents_core: { model_provider: modelProvider } }
        : {}),
      metadata: {
        application: "parsar-example",
        agent_id: agent.id,
        title: name,
        agent_name: agent.name,
      },
    },
  };
}

export function sessionAPI(store, core, connections) {
  const running = new Map();
  const create = async (id, body) => {
    let row = store.get("sessions", id);
    if (row?.deleted_at) throw new AppError(410, "会话已取消。");
    if (row?.core_session_id) return row;
    if (!row) {
      row = prepareSession(store, id, body, connections?.owner(id));
      store.put("sessions", row);
    }
    try {
      if (connections && row.connection_requirement) {
        row = await connections.prepare(row);
        if (store.get("sessions", id)?.deleted_at)
          throw new AppError(410, "会话已取消。");
        store.put("sessions", row);
        if (row.connection_wait) {
          const { request, ...record } = row;
          return record;
        }
      }
      if (row.connection) {
        row = { ...row, connection_dispatch_pending: true };
        store.put("sessions", row);
      }
      // Freeze the request before sending. Retries, including after restart, reuse the same key and bytes.
      const session = await core(
        "/v1/agents/sessions",
        "POST",
        row.request,
        id,
      );
      if (!uuid.test(session.id))
        throw new AppError(502, "Core 返回了无效的会话。");
      const { request, ...record } = row;
      record.core_session_id = session.id;
      delete record.connection_dispatch_pending;
      delete record.connection_error;
      store.put("sessions", record);
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
  return (id, body) => {
    if (!running.has(id))
      running.set(
        id,
        create(id, body).finally(() => running.delete(id)),
      );
    return running.get(id);
  };
}

export async function requireSessionSettled(core, record) {
  if (record.deleted_at) return;
  if (record.connection_wait)
    throw new AppError(409, "任务正在等待连接，请先完成授权或取消等待。");
  if (!record.core_session_id)
    throw new AppError(409, "上次执行尚未确认，请先恢复创建。");
  const prefix = `/v1/agents/sessions/${record.core_session_id}`;
  const [session, turns] = await Promise.all([
    core(prefix, "GET"),
    core(`${prefix}/turns?order=desc&limit=1`, "GET"),
  ]);
  if (
    session.status !== "failed" &&
    !(
      session.status === "idle" &&
      ["completed", "failed", "cancelled"].includes(turns.data?.[0]?.status)
    )
  )
    throw new AppError(
      409,
      "Agent 仍在准备、执行或等待处理，请先打开会话继续或取消。",
    );
}

export function requireAutomaticRuntime(store, id) {
  const runtime = store.get("runtimes", id || "");
  if (!runtime || runtime.environment !== "openai_hosted")
    throw new AppError(400, "请选择有效的托管环境模板。");
  return runtime;
}
