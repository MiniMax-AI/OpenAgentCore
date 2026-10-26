import { createHash, randomUUID } from "node:crypto";
import { AppError, text, uuid } from "./store.mjs";
import { harnessOptions } from "../shared/harness-options.mjs";
import { boundBot, contributions } from "./capability-kinds.mjs";
import { templateFiles } from "./core-resources.mjs";
import { turnsAPI } from "./turns.mjs";

// A warm Session matches a cold creation only when every frozen byte except the
// first input and the per-conversation metadata is the same, including the
// content of the Core Agent and Template it references.
const fingerprint = ({ request: { input, metadata, ...request }, core_digests }) =>
  createHash("sha256").update(JSON.stringify({ request, core_digests })).digest("hex");
const warmAge = 12 * 3600_000;

// A Session references the Agent's saved Core Agent and its environment
// template's Core Template, and adds only what differs per conversation: the
// bot identity, the Agent's Skills, Plugins and knowledge, and attachments.
export async function prepareSession(store, resources, id, body, origin = {}, warm = false) {
  const stored = store.get("agents", body.agent_id);
  if (!stored) throw new AppError(400, "请选择有效的 Agent。");
  if (stored.enabled === false) throw new AppError(400, "Agent 已停用，请先启用。");
  const storedRuntime = store.get("runtimes", stored.runtime_id || "");
  if (!storedRuntime) throw new AppError(400, "Agent 未配置环境模板。");
  const agent = await resources.ensure("agents", stored.id);
  const runtime = await resources.ensure("runtimes", storedRuntime.id);
  const model = store.get("models", agent.model_id);
  const bound = contributions(store, agent);
  const bot = boundBot(store, agent);
  const provider = store.get("providers", model.provider_id);
  if (!provider?.base_url?.startsWith("https://") || !provider.api_key)
    throw new AppError(
      400,
      "请先在模型页面配置所选 Provider 的 HTTPS Base URL 和 API Key。",
    );
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
  // Session files replace the Template's set, so they include the Template's own.
  const extraFiles = [...files, ...bound.session.files];
  // Variables overlay the Template's own: the bound bot's identity and the
  // secrets the Agent chose for what its capabilities need.
  const env = bound.session.env;
  const environment = {
    type: "openai_hosted",
    environment_template_id: runtime.core_template_id,
    ...(Object.keys(env).length ? { env } : {}),
    ...(bound.session.skills.length ? { skills: bound.session.skills } : {}),
    ...(bound.session.plugins.length ? { plugins: bound.session.plugins } : {}),
    ...(extraFiles.length ? { files: [...templateFiles(runtime), ...extraFiles] } : {}),
  };
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
    core_digests: [agent.core_digest, runtime.core_digest],
    configuration: {
      agent_name: agent.name,
      agent_revision: agent.revision,
      runtime_revision: runtime.revision,
      bot_name: bot?.name,
      model: model.model,
      harness: agent.harness,
      environment: runtime.environment,
      environment_name: runtime.name,
      packages: runtime.packages || { npm: [], python: [] },
      setup_configured: Boolean(runtime.setup_script),
      env_names: [...new Set([...Object.keys(runtime.env || {}), ...Object.keys(env)])],
      file_paths: (runtime.files || []).map((file) => file.path),
      ...bound.summary,
    },
    created_at: Math.floor(Date.now() / 1000),
    request: {
      agent_id: agent.core_agent_id,
      environment,
      ...(bound.session.vault_ids.length ? { vault_ids: bound.session.vault_ids } : {}),
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
      // The Provider key stays in Parsar's model settings and travels per Session.
      x_agents_core: {
        model_provider: {
          protocol: harnessOptions[agent.harness].protocol,
          base_url: provider.base_url,
          api_key: provider.api_key,
        },
      },
      metadata: {
        application: "parsar-example",
        agent_id: agent.id,
        title: name,
        agent_name: agent.name,
      },
    },
  };
}

export function sessionAPI(store, core, resources) {
  const running = new Map();
  const turns = turnsAPI(store, core);
  // Hand a prepared Session to this conversation when one matches its frozen
  // request. The request stays, so the monitor can still create the Session
  // normally if the prepared one does not start.
  const claim = (row) => {
    if (typeof row.request.input !== "string") return;
    const print = fingerprint(row);
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
      row = await prepareSession(store, resources, id, body, origin);
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
            await prepareSession(store, resources, randomUUID(), { agent_id: agent.id }, {}, true),
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
      const { request } = await prepareSession(store, resources, id, { agent_id: agent.id }, {}, true);
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
