import { sessionRestriction } from "../shared/session-profile.mjs";
import { zipSync, strToU8 } from "fflate";
import { AppError, text, uuid } from "./store.mjs";

function requestFor(store, id, body) {
  const agent = store.get("agents", body.agent_id);
  const runtime = store.get("runtimes", body.runtime_id);
  if (!agent || !runtime)
    throw new AppError(400, "请选择有效的 Agent 和运行时。");
  const model = store.get("models", agent.model_id);
  const mcps = agent.mcp_ids.map((id) => store.get("mcps", id));
  if (!model || mcps.some((mcp) => !mcp))
    throw new AppError(400, "Agent 绑定的资源已不可用。");
  if (new Set(mcps.map((mcp) => mcp.label)).size !== mcps.length)
    throw new AppError(400, "绑定的 MCP 服务标识不能重复。");
  const environment = { type: runtime.environment };
  const selfHosted = environment.type === "self_hosted";
  const restriction = sessionRestriction(agent, runtime);
  if (restriction) throw new AppError(400, restriction);
  let modelProvider;
  if (environment.type !== "none" && agent.harness !== "mcode") {
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
  if (selfHosted) {
    environment.workspace_directory = runtime.workspace_directory;
    environment.capability_directories = runtime.capability_directories || [];
  }
  const tools = [{ type: "web_search", mode: "disabled" }];
  if (environment.type === "none" || selfHosted) {
    tools.push(
      ...mcps.map((mcp) => ({
        type: "mcp",
        server_label: mcp.label,
        transport: { type: "http", server_url: mcp.url },
        connection_origin: "service",
        required: true,
      })),
    );
  } else {
    environment.skills = agent.skill_ids.map((skill_id) => ({
      type: "skill_reference",
      skill_id,
    }));
    if (mcps.length) {
      const name = "agent-mcp";
      const description = "Agent HTTP MCP bindings";
      const archive = zipSync({
        "agent/.codex-plugin/plugin.json": strToU8(
          JSON.stringify({ name, description, mcpServers: "./.mcp.json" }),
        ),
        "agent/.mcp.json": strToU8(
          JSON.stringify({
            mcpServers: Object.fromEntries(
              mcps.map((mcp) => [mcp.label, { type: "http", url: mcp.url }]),
            ),
          }),
        ),
      });
      environment.network = { access: "enabled" };
      environment.plugins = [
        {
          type: "inline",
          name,
          description,
          source: {
            type: "base64",
            media_type: "application/zip",
            data: Buffer.from(archive).toString("base64"),
          },
        },
      ];
    }
  }
  const name = text(body.name, "会话名称", 80, true);
  const input = selfHosted ? undefined : text(body.input, "消息", 100000, true);
  return {
    id,
    name,
    agent_id: agent.id,
    runtime_id: runtime.id,
    created_at: Math.floor(Date.now() / 1000),
    ...(selfHosted
      ? {
          self_hosted: {
            platform: runtime.platform,
            workspace_directory: runtime.workspace_directory,
          },
        }
      : {}),
    request: {
      agent: {
        model: model.model,
        instructions: agent.instructions,
        x_agents_core: { harness: agent.harness },
        multi_agent: { enabled: false },
        text: { verbosity: "medium" },
        tools,
      },
      environment,
      ...(input ? { input } : {}),
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

export function sessionAPI(store, core) {
  const running = new Map();
  const create = async (id, body) => {
    let row = store.get("sessions", id);
    if (row?.core_session_id) return row;
    if (!row) {
      row = requestFor(store, id, body);
      store.put("sessions", row);
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
      const { request, ...record } = row;
      record.core_session_id = session.id;
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
