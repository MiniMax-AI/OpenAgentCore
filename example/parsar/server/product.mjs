import { providerAPI, publicProvider } from "./providers.mjs";
import { runtimeProfile } from "./runtime-profile.mjs";
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
export function productAPI(store, core, fetchImpl = fetch) {
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
  const sessions = sessionAPI(store, core);
  const publicRow = (kind, row) => {
    if (kind === "providers") return publicProvider(row);
    if (kind === "sessions") {
      const { request, ...record } = row;
      return record;
    }
    return row;
  };
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
  return async (method, path, body) => {
    if (path === "/app/providers/discover" && method === "POST")
      return providers.discover(body);
    // OrcaRouter account routes: catalog discovery and the two credential
    // adapters. They read and write the same provider record the ordinary
    // provider path uses, so nothing downstream knows which adapter ran.
    const orca = path.match(
      /^\/app\/providers\/([a-f0-9-]{36})\/orcarouter\/(catalog|connect|connect\/code|cancel|credential)$/,
    );
    if (orca) {
      const [, providerId, action] = orca;
      if (action === "catalog" && method === "POST")
        return providers.orca.catalog(providerId, body?.capability ?? "chat");
      if (action === "connect" && method === "POST")
        return providers.orca.startLogin(providerId);
      if (action === "connect/code" && method === "POST")
        return providers.orca.submitLogin(providerId, body);
      if (action === "cancel" && method === "POST") {
        providers.orca.cancelLogin(providerId);
        return providers.orca.view(providerId);
      }
      if (action === "credential" && method === "DELETE") {
        // An explicit clear removes the credential; a failed login never does.
        const row = store.get("providers", providerId);
        if (!row || row.provider !== "orcarouter")
          throw new AppError(404, "该 Provider 不是 OrcaRouter。");
        const { api_key, ...rest } = row;
        store.put("providers", {
          ...rest,
          credential_source: "",
          status: "ok",
          generation: (row.generation ?? 0) + 1,
          revision: row.revision + 1,
        });
        return providers.orca.view(providerId);
      }
      if (action === "credential" && method === "GET")
        return providers.orca.view(providerId);
      throw new AppError(405, "Method not allowed.");
    }
    const match = path.match(/^\/app\/([a-z]+)(?:\/([a-f0-9-]{36}))?$/);
    if (!match || !kinds.has(match[1]) || (match[2] && !uuid.test(match[2])))
      throw new AppError(404, "Not found.");
    const [, kind, id] = match;
    if (method === "GET") {
      if (!id) return store.list(kind).map((row) => publicRow(kind, row));
      const value = store.get(kind, id);
      if (!value) throw new AppError(404, "记录不存在。");
      return kind === "providers"
        ? {
            ...publicProvider(value),
            ...(value.provider === "orcarouter"
              ? { orcarouter: providers.orca.view(id) }
              : {}),
            models: store
              .list("models")
              .filter((model) => model.provider_id === id),
          }
        : publicRow(kind, value);
    }
    if (!id) throw new AppError(405, "Method not allowed.");
    if (kind === "sessions") {
      if (method !== "PUT") throw new AppError(405, "Method not allowed.");
      return sessions(id, body);
    }
    const previous = store.get(kind, id);
    if (method === "DELETE") {
      const used =
        store.list("models").some((row) => row.provider_id === id) ||
        store
          .list("agents")
          .some((row) => row.model_id === id || row.mcp_ids.includes(id)) ||
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
      value.label = text(body.label, "MCP 标识", 64, true);
      if (!/^[a-zA-Z0-9_-]+$/.test(value.label))
        throw new AppError(400, "MCP 标识仅支持字母、数字、下划线和连字符。");
    }
    if (kind === "runtimes") {
      Object.assign(value, runtimeProfile(body));
    }
    if (kind === "agents") {
      value.model_id = requireReference("models", body.model_id);
      if (!["codex", "claude_sdk", "mcode"].includes(body.harness))
        throw new AppError(400, "请选择执行引擎。");
      value.harness = body.harness;
      value.instructions = text(body.instructions, "指令", 100000);
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
    }
    store.transaction(() => {
      store.put(kind, value);
      if (kind === "models")
        touchProviders(previous?.provider_id, value.provider_id);
    });
    return value;
  };
}
