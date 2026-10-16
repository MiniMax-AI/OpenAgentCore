import { userConnections } from "./connections.mjs";
import { feishuConnection } from "./feishu-connection.mjs";
import { agentMCPTokenAPI } from "./agent-mcp.mjs";
import { mcpOAuthAPI } from "./mcp-oauth.mjs";
import { previewRegistrySkill } from "./skills-directory.mjs";
import { feishuRegistrationAPI } from "./feishu-registration.mjs";
import { capabilityAPI } from "./capabilities.mjs";
import { randomUUID } from "node:crypto";
import { providerAPI, publicProvider } from "./providers.mjs";
import { runtimeProfile, publicRuntime } from "./runtime-profile.mjs";
import { scheduleAPI } from "./schedules.mjs";
import { feishuAPI } from "./feishu.mjs";
import { workAPI } from "./work.mjs";
import { sessionAPI } from "./sessions.mjs";
import { AppError, text, uuid } from "./store.mjs";

const kinds = new Set([
  "providers",
  "models",
  "mcps",
  "runtimes",
  "agents",
  "sessions",
]);
export function productAPI(store, core, fetchImpl = fetch, origin) {
  // Preserve earlier example configurations without fabricating execution records.
  for (const kind of ["templates", "instances"]) {
    for (const row of store.list(kind)) {
      if (!store.get("agents", row.id)) {
        const {
          id,
          name,
          revision,
          model_id,
          harness,
          instructions,
          skill_ids,
          mcp_ids,
        } = row;
        store.put("agents", {
          id,
          name,
          revision,
          model_id,
          harness,
          instructions,
          skill_ids,
          mcp_ids,
        });
      }
      store.remove(kind, row.id);
    }
  }
  const connections = userConnections(
    store,
    feishuConnection(store, fetchImpl),
  );
  const sessions = sessionAPI(store, core, connections);
  const publicRow = (kind, row) => {
    if (kind === "providers") return publicProvider(row);
    if (kind === "runtimes") return publicRuntime(row);
    if (kind === "sessions") {
      const { request, ...record } = row;
      return record;
    }
    return row;
  };
  const capabilities = capabilityAPI(store);
  const mcpTokens = agentMCPTokenAPI(store);
  const oauth = mcpOAuthAPI(store, core, origin, fetchImpl);
  const schedules = scheduleAPI(store);
  const feishu = feishuAPI(store);
  const registration = feishuRegistrationAPI(store, fetchImpl);
  const work = workAPI(store, core, sessions);
  const providers = providerAPI(store, fetchImpl);
  const touchProviders = (...ids) => {
    for (const id of new Set(ids.filter(Boolean))) {
      const provider = store.get("providers", id);
      if (provider)
        store.put("providers", {
          ...provider,
          revision: provider.revision + 1,
        });
    }
  };
  const requireReference = (kind, id) => {
    if (typeof id !== "string" || !store.get(kind, id))
      throw new AppError(400, "请选择有效的资源。");
    return id;
  };
  const references = (kind, ids) => {
    if (!Array.isArray(ids) || ids.length > 8)
      throw new AppError(400, "最多选择 8 个资源。");
    return [...new Set(ids.map((id) => requireReference(kind, id)))];
  };
  const handle = async (method, path, body) => {
    if (path === "/app/connections" || path.startsWith("/app/connections/"))
      return connections.api(method, path, body);
    const retryId = path.match(
      /^\/app\/sessions\/([a-f0-9-]{36})\/connection$/,
    )?.[1];
    if (retryId && method === "POST") {
      const row = store.get("sessions", retryId);
      if (!row?.connection_requirement || row.core_session_id || row.deleted_at)
        throw new AppError(409, "此会话无法重新发起连接，请开启新会话。");
      const login = await connections.begin(
        row.connection_requirement,
        row.connection_owner,
        true,
      );
      const current = store.get("sessions", retryId);
      if (
        !current ||
        current.deleted_at ||
        current.core_session_id ||
        current.connection_dispatch_pending
      )
        throw new AppError(409, "会话状态已改变，请刷新页面。");
      store.put("sessions", {
        ...current,
        connection_wait: login,
        connection_error: undefined,
      });
      return login;
    }
    if (/^\/app\/agents\/[^/]+\/mcp-token$/.test(path))
      return mcpTokens(method, path);
    if (path.startsWith("/app/mcp-oauth/")) return oauth(method, path, body);
    if (path === "/app/capabilities" || path.startsWith("/app/capabilities/"))
      return capabilities(method, path, body);
    if (path === "/app/settings") {
      const old = store.get("settings", "workspace") || {
        id: "workspace",
        name: "Parsar",
        revision: 0,
      };
      if (method === "GET") return old;
      if (method !== "PUT") throw new AppError(405, "Method not allowed.");
      if (body.revision !== old.revision)
        throw new AppError(409, "设置已更新，请重新加载。");
      const value = {
        ...old,
        name: text(body.name, "名称", 80, true),
        revision: old.revision + 1,
      };
      store.put("settings", value);
      return value;
    }
    if (path === "/app/audit" && method === "GET")
      return store.list("audit").sort((a, b) => b.created_at - a.created_at);
    if (path === "/app/schedules" || path.startsWith("/app/schedules/"))
      return schedules(method, path, body);
    if (path === "/app/skills/registry/preview" && method === "POST")
      return previewRegistrySkill(body);
    if (path.startsWith("/app/feishu/provision/"))
      return registration(method, path, body);
    if (path === "/app/feishu" || path.startsWith("/app/feishu/"))
      return feishu(method, path, body);
    if (/^\/app\/(projects|tasks)(?:\/|$)/.test(path))
      return work(method, path, body);
    if (path === "/app/providers/discover" && method === "POST")
      return providers.discover(body);
    const match = path.match(/^\/app\/([a-z]+)(?:\/([a-f0-9-]{36}))?$/);
    if (!match || !kinds.has(match[1]) || (match[2] && !uuid.test(match[2])))
      throw new AppError(404, "Not found.");
    const [, kind, id] = match;
    if (method === "GET") {
      if (!id) {
        const sources =
          kind === "sessions"
            ? new Map([
                ...store
                  .list("feishu_threads")
                  .map((row) => [row.session_id, "feishu"]),
                ...store.list("agent_mcp_runs").map((row) => [row.id, "mcp"]),
                ...store
                  .list("schedule_occurrences")
                  .map((row) => [row.session_id, "schedule"]),
              ])
            : null;
        return store
          .list(kind)
          .filter((row) => kind !== "sessions" || !row.deleted_at)
          .map((row) => ({
            ...publicRow(kind, row),
            ...(sources
              ? {
                  source: sources.get(row.id) || (row.task_id ? "task" : "web"),
                }
              : {}),
          }));
      }
      const value = store.get(kind, id);
      if (!value) throw new AppError(404, "记录不存在。");
      return kind === "providers"
        ? {
            ...publicProvider(value),
            models: store
              .list("models")
              .filter((model) => model.provider_id === id),
          }
        : publicRow(kind, value);
    }
    if (!id) throw new AppError(405, "Method not allowed.");
    if (kind === "sessions") {
      if (["PATCH", "DELETE"].includes(method)) {
        const row = store.get("sessions", id);
        if (!row || row.deleted_at) throw new AppError(404, "会话不存在。");
        if (
          !row.core_session_id &&
          row.connection_wait &&
          method === "DELETE"
        ) {
          store.put("sessions", {
            ...row,
            deleted_at: Date.now(),
            connection_wait: undefined,
          });
          const shared = store
            .list("sessions")
            .some(
              (r) =>
                r.id !== row.id &&
                !r.deleted_at &&
                r.connection_wait?.id === row.connection_wait.id,
            );
          const login = store.get("connection_logins", row.connection_wait.id);
          if (!shared && login?.status === "pending")
            store.put("connection_logins", {
              ...login,
              status: "cancelled",
              device_code: undefined,
              pending_tokens: undefined,
            });
          return { deleted: true };
        }
        if (!row.core_session_id)
          throw new AppError(409, "请先恢复创建，以确认实际会话。");
        if (method === "PATCH") {
          const name = text(body.name, "名称", 80, true);
          const current = await core(
            `/v1/agents/sessions/${row.core_session_id}`,
            "GET",
          );
          await core(`/v1/agents/sessions/${row.core_session_id}`, "POST", {
            metadata: { ...current.metadata, title: name },
          });
          const updated = { ...row, name };
          store.put("sessions", updated);
          return publicRow("sessions", updated);
        }
        try {
          await core(`/v1/agents/sessions/${row.core_session_id}`, "DELETE");
        } catch (error) {
          if (!(error instanceof AppError && error.status === 404)) throw error;
        }
        store.put("sessions", { ...row, deleted_at: Date.now() });
        return { deleted: true };
      }
      if (method !== "PUT") throw new AppError(405, "Method not allowed.");
      return sessions(id, body);
    }
    const previous = store.get(kind, id);
    if (method === "DELETE") {
      const used =
        store
          .list("feishu_bots")
          .flatMap((bot) => bot.routes)
          .some((row) => row.agent_id === id || row.runtime_id === id) ||
        store
          .list("tasks")
          .some((row) => row.agent_id === id || row.runtime_id === id) ||
        store.list("models").some((row) => row.provider_id === id) ||
        store
          .list("agents")
          .some(
            (row) =>
              row.model_id === id ||
              row.runtime_id === id ||
              row.mcp_ids.includes(id),
          ) ||
        store
          .list("sessions")
          .some((row) => row.agent_id === id || row.runtime_id === id);
      if (used) throw new AppError(409, "资源仍被引用，请先调整绑定。");
      store.transaction(() => {
        store.remove(kind, id);
        if (kind === "agents")
          for (const selection of store.list("feishu_selections")) {
            if (selection.agent_id === id)
              store.remove("feishu_selections", selection.id);
          }
        if (kind === "models") touchProviders(previous?.provider_id);
      });
      return {};
    }
    if (method !== "PUT") throw new AppError(405, "Method not allowed.");
    if (previous && body.revision !== previous.revision)
      throw new AppError(409, "配置已更新，请重新打开后编辑。");
    if (kind === "providers") return providers.save(id, body, previous);
    const value = {
      id,
      name: text(body.name, "名称", 80, true),
      revision: (previous?.revision || 0) + 1,
    };
    if (kind === "models") {
      value.enabled = body.enabled !== false;
      if (body.pricing !== undefined) {
        if (
          !body.pricing ||
          ![body.pricing.input, body.pricing.output].every(
            (price) =>
              typeof price === "number" &&
              Number.isFinite(price) &&
              price >= 0 &&
              price <= 1000000,
          )
        )
          throw new AppError(400, "价格需为非负的每百万 Token 美元金额。");
        value.pricing = {
          input: body.pricing.input,
          output: body.pricing.output,
        };
      }
      value.model = text(body.model, "模型 ID", 200, true).trim();
      value.provider_id = requireReference("providers", body.provider_id);
      if (
        store
          .list("models")
          .some(
            (row) =>
              row.id !== id &&
              row.provider_id === value.provider_id &&
              row.model === value.model,
          )
      )
        throw new AppError(409, "此 Provider 已有相同的模型 ID。");
    }
    if (kind === "mcps") {
      let url;
      try {
        url = new URL(body.url);
      } catch {
        throw new AppError(400, "请填写有效的 MCP URL。");
      }
      if (
        url.protocol !== "https:" ||
        url.username ||
        url.password ||
        url.hash ||
        url.search
      )
        throw new AppError(400, "MCP 使用不含凭据的 HTTPS 地址。");
      value.url = url.href;
      if (body.vault_id && !uuid.test(body.vault_id))
        throw new AppError(400, "凭据库标识无效。");
      if (
        body.credential_id &&
        (!body.vault_id || !uuid.test(body.credential_id))
      )
        throw new AppError(400, "凭据标识无效。");
      value.vault_id = body.vault_id || "";
      value.credential_id = body.credential_id || "";
      value.label = text(body.label, "MCP 标识", 64, true);
      if (!/^[a-zA-Z0-9_-]+$/.test(value.label))
        throw new AppError(400, "MCP 标识仅支持字母、数字、下划线和连字符。");
    }
    if (kind === "runtimes") {
      Object.assign(value, runtimeProfile(body, previous));
    }
    if (kind === "agents") {
      value.model_id = requireReference("models", body.model_id);
      if (!["codex", "claude_sdk", "mcode"].includes(body.harness))
        throw new AppError(400, "请选择执行引擎。");
      value.harness = body.harness;
      value.instructions = text(body.instructions, "指令", 100000);
      value.enabled = body.enabled !== false;
      value.ask_user = body.ask_user === true;
      value.connection_requirement = connections.requirement(
        body.connection_requirement,
      );
      if (value.ask_user && body.harness === "mcode")
        throw new AppError(400, "MiniMax Code 尚不支持公开 Function 工具。");
      value.runtime_id = body.runtime_id
        ? requireReference("runtimes", body.runtime_id)
        : "";
      value.capability_ids = references(
        "capabilities",
        body.capability_ids || [],
      );
      value.capability_versions = {};
      for (const capabilityId of value.capability_ids) {
        const version = body.capability_versions?.[capabilityId];
        if (
          version &&
          !store
            .get("capabilities", capabilityId)
            .versions.some((row) => row.version === version)
        )
          throw new AppError(400, "能力版本不存在。");
        if (version) value.capability_versions[capabilityId] = version;
      }
      value.mcp_ids = references("mcps", body.mcp_ids);
      if (
        !Array.isArray(body.skill_ids) ||
        body.skill_ids.length > 8 ||
        body.skill_ids.some(
          (id) =>
            typeof id !== "string" ||
            !/^skill_[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(id),
        )
      )
        throw new AppError(400, "技能配置无效。");
      value.skill_ids = [...new Set(body.skill_ids)];
      value.skill_versions = {};
      if (
        body.skill_versions !== undefined &&
        (!body.skill_versions ||
          typeof body.skill_versions !== "object" ||
          Array.isArray(body.skill_versions))
      )
        throw new AppError(400, "技能版本配置无效。");
      for (const skillId of value.skill_ids) {
        const version = body.skill_versions?.[skillId];
        if (
          version !== undefined &&
          version !== "" &&
          (typeof version !== "string" || !/^[1-9][0-9]*$/.test(version))
        )
          throw new AppError(400, "技能版本无效。");
        if (version) value.skill_versions[skillId] = version;
      }
    }
    store.transaction(() => {
      store.put(kind, value);
      if (kind === "models")
        touchProviders(previous?.provider_id, value.provider_id);
    });
    return publicRow(kind, value);
  };
  const handler = async (method, path, body) => {
    const result = await handle(method, path, body);
    if (method !== "GET")
      store.put("audit", {
        id: randomUUID(),
        method,
        path,
        created_at: Date.now(),
      });
    return result;
  };
  handler.tickConnections = async () => {
    await connections.tick();
    for (const row of store
      .list("sessions")
      .filter(
        (r) =>
          (r.connection_wait || r.connection_dispatch_pending) &&
          !r.core_session_id &&
          !r.deleted_at,
      )) {
      try {
        await sessions(row.id, {});
      } catch (error) {
        const current = store.get("sessions", row.id);
        if (current)
          store.put("sessions", {
            ...current,
            connection_error:
              error instanceof AppError
                ? error.message
                : "连接恢复暂时失败，将自动重试。",
          });
      }
    }
  };
  return handler;
}
