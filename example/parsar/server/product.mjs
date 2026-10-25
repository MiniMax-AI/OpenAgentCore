import { mcpOAuthAPI } from "./mcp-oauth.mjs";
import { previewRegistrySkill } from "./skills-directory.mjs";
import { feishuRegistrationAPI } from "./feishu-registration.mjs";
import { capabilityAPI } from "./capabilities.mjs";
import { randomUUID } from "node:crypto";
import { providerAPI, publicProvider } from "./providers.mjs";
import { runtimeProfile, publicRuntime } from "./runtime-profile.mjs";
import { feishuAPI } from "./feishu.mjs";
import { trialAPI } from "./trials.mjs";
import { sessionAPI } from "./sessions.mjs";
import { AppError, text, uuid } from "./store.mjs";
import {
  harnessOptions,
  executionOptionFields,
  unsupportedOption,
} from "../shared/harness-options.mjs";
import { maxFiles, maxPlugins, maxSkills } from "../shared/environment-limits.mjs";

const optionLabels = {
  reasoning_effort: "推理强度",
  reasoning_summary: "推理摘要",
  web_search: "网页搜索方式",
  verbosity: "输出详细度",
  service_tier: "服务层级",
};
const kinds = new Set([
  "providers",
  "models",
  "mcps",
  "runtimes",
  "agents",
  "sessions",
]);
export function productAPI(store, core, fetchImpl = fetch, origin) {
  const sessions = sessionAPI(store, core);
  const publicRow = (kind, row) => {
    if (kind === "providers") return publicProvider(row);
    if (kind === "runtimes") return publicRuntime(row);
    if (kind === "sessions") {
      const { request, ...record } = row;
      return { ...record, turn: sessions.turns.summary(record.id) };
    }
    return row;
  };
  const capabilities = capabilityAPI(store);
  const oauth = mcpOAuthAPI(store, core, origin, fetchImpl);
  const feishu = feishuAPI(store);
  const registration = feishuRegistrationAPI(store, fetchImpl);
  const providers = providerAPI(store, fetchImpl);
  const trials = trialAPI(store, core);
  const sessionAfter = (run) => async (id, body) => {
    await run(id, body);
    return publicRow("sessions", store.get("sessions", id));
  };
  const actions = {
    "POST sessions/messages": sessionAfter(sessions.turns.send),
    "POST sessions/retry": sessionAfter(sessions.turns.retry),
    "GET agents/health": (id) => {
      if (!store.get("agents", id)) throw new AppError(404, "Agent 不存在。");
      return sessions.turns.health(id);
    },
    "POST models/test": (id) => providers.test(id),
    "POST runtimes/trial": (id) => trials.start(id),
    "GET runtimes/trial": (id) => trials.get(id),
  };
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
    if (!Array.isArray(ids)) throw new AppError(400, "请选择有效的资源。");
    return [...new Set(ids.map((id) => requireReference(kind, id)))];
  };
  const handle = async (method, path, body) => {
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
    // Files every recent conversation produced, newest first. Core keeps
    // artifacts with the Session, so listing them wakes no sandbox.
    if (path === "/app/artifacts" && method === "GET") {
      const recent = store
        .list("sessions")
        .filter((row) => row.core_session_id && !row.deleted_at)
        .sort((a, b) => b.created_at - a.created_at)
        .slice(0, 50);
      const rows = [];
      for (let i = 0; i < recent.length; i += 5)
        for (const [row, page] of await Promise.all(
          recent.slice(i, i + 5).map(async (row) => [
            row,
            await core(`/v1/agents/sessions/${row.core_session_id}/artifacts?limit=20&order=desc`, "GET").catch(() => ({ data: [] })),
          ]),
        ))
          for (const file of page.data || [])
            rows.push({
              id: file.id,
              path: file.path,
              size_bytes: file.size_bytes,
              created_at: file.created_at,
              session_id: row.id,
              core_session_id: row.core_session_id,
              conversation: row.name,
            });
      return rows.sort((a, b) => b.created_at - a.created_at);
    }
    if (path === "/app/audit" && method === "GET")
      return store.list("audit").sort((a, b) => b.created_at - a.created_at);
    if (path === "/app/skills/registry/preview" && method === "POST")
      return previewRegistrySkill(body);
    if (path.startsWith("/app/feishu/provision/"))
      return registration(method, path, body);
    if (path === "/app/feishu" || path.startsWith("/app/feishu/"))
      return feishu(method, path, body);
    if (path === "/app/providers/discover" && method === "POST")
      return providers.discover(body);
    if (path === "/app/turns" && method === "GET") return sessions.turns.overview();
    // Actions on one resource: `/app/{kind}/{id}/{action}`.
    const action = path.match(/^\/app\/([a-z]+)\/([a-f0-9-]{36})\/([a-z]+)$/);
    const handler = action && actions[`${method} ${action[1]}/${action[3]}`];
    if (action && handler) return handler(action[2], body);
    const match = path.match(/^\/app\/([a-z]+)(?:\/([a-f0-9-]{36}))?$/);
    if (!match || !kinds.has(match[1]) || (match[2] && !uuid.test(match[2])))
      throw new AppError(404, "Not found.");
    const [, kind, id] = match;
    if (method === "GET") {
      if (!id)
        return store
          .list(kind)
          .filter((row) => kind !== "sessions" || !row.deleted_at)
          .map((row) => publicRow(kind, row));
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
      return publicRow("sessions", await sessions(id, body));
    }
    const previous = store.get(kind, id);
    if (method === "DELETE") {
      const used =
        store.list("feishu_bots").some((row) => row.agent_id === id) ||
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
      if (!harnessOptions[body.harness])
        throw new AppError(400, "请选择执行引擎。");
      value.harness = body.harness;
      if (!body.model_id) throw new AppError(400, "请选择模型。");
      value.model_id = requireReference("models", body.model_id);
      for (const field of executionOptionFields)
        value[field] = typeof body[field] === "string" ? body[field] : "";
      value.web_search ||= "disabled";
      const unsupported = unsupportedOption(value);
      if (unsupported)
        throw new AppError(
          400,
          `${harnessOptions[value.harness].label} 不支持所选的${optionLabels[unsupported]}。`,
        );
      value.description = text(body.description || "", "描述", 500);
      value.instructions = text(body.instructions, "指令", 100000);
      value.enabled = body.enabled !== false;
      value.ask_user = body.ask_user === true;
      if (!body.runtime_id) throw new AppError(400, "请选择环境模板。");
      value.runtime_id = requireReference("runtimes", body.runtime_id);
      value.capability_ids = references(
        "capabilities",
        body.capability_ids || [],
      );
      // Knowledge items become initial files beside the template's own files.
      const kinds = value.capability_ids.map(
        (capabilityId) => store.get("capabilities", capabilityId).kind,
      );
      if (kinds.filter((kind) => kind === "plugin").length > maxPlugins)
        throw new AppError(400, `最多选择 ${maxPlugins} 个 Plugin。`);
      if (
        kinds.filter((kind) => kind === "knowledge").length +
          (store.get("runtimes", value.runtime_id).files || []).length >
        maxFiles
      )
        throw new AppError(
          400,
          `知识和环境模板文件合计最多 ${maxFiles} 个。`,
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
      if (Array.isArray(body.skill_ids) && body.skill_ids.length > maxSkills)
        throw new AppError(400, `最多选择 ${maxSkills} 个 Skill。`);
      if (
        !Array.isArray(body.skill_ids) ||
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
  handler.sessions = sessions;
  return handler;
}
