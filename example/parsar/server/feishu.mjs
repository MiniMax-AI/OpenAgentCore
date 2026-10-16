import { createHash, randomUUID } from "node:crypto";
import { taskStatuses } from "../shared/work-profile.mjs";
import { sessionRestriction } from "../shared/session-profile.mjs";
const stableId = (value) => {
  const hex = createHash("sha256").update(value).digest("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`;
};
const selectionId = (app, chat, sender) => stableId(`${app}:${chat}:${sender}`);
import { feishuContextReader } from "./feishu-context.mjs";
import { AppError, text, uuid, transientError } from "./store.mjs";
import { requireAutomaticRuntime } from "./sessions.mjs";

const stamp = () => Date.now();
export const publicFeishu = ({ app_secret, ...record }) => ({
  ...record,
  has_app_secret: !!app_secret,
});
export function feishuAPI(store) {
  return async (method, path, body) => {
    const retryMatch = path.match(
      /^\/app\/feishu\/deliveries\/(inbound|outbound)\/([a-f0-9-]{36})$/,
    );
    if (retryMatch && method === "PUT" && uuid.test(retryMatch[2])) {
      const kind = `feishu_${retryMatch[1]}`,
        row = store.get(kind, retryMatch[2]);
      if (!row) throw new AppError(404, "投递记录不存在。");
      if (row.state !== "failed")
        throw new AppError(409, "此记录不需要手动重试。");
      if (
        kind === "feishu_outbound" &&
        row.first_attempt_at &&
        stamp() - row.first_attempt_at > 55 * 60000
      )
        throw new AppError(
          409,
          "飞书去重窗口已过。请人工检查原话题，不能安全自动重发。",
        );
      store.put(kind, {
        ...row,
        state: "queued",
        attempts: 0,
        error: undefined,
        next_at: 0,
      });
      return {};
    }
    if (path === "/app/feishu/bots" && method === "GET")
      return store.list("feishu_bots").map((row) => ({
        ...publicFeishu(row),
        health: store.get("feishu_health", row.id) || { state: "stopped" },
      }));
    if (path === "/app/feishu/deliveries" && method === "GET")
      return [
        ...store
          .list("feishu_inbound")
          .map((row) => ({ ...row, direction: "inbound" })),
        ...store
          .list("feishu_outbound")
          .map((row) => ({ ...row, direction: "outbound" })),
      ]
        .sort((a, b) => b.created_at - a.created_at)
        .filter(
          (row, index) =>
            index < 100 ||
            ["queued", "submitting", "failed"].includes(row.state),
        )
        .map(({ text, payload, event, resources, media, context, ...row }) => {
          const taskId =
            row.task_id || store.get("feishu_threads", row.thread_id)?.task_id;
          return {
            ...row,
            task_id: taskId && store.get("tasks", taskId) ? taskId : undefined,
          };
        });
    const botMatch = path.match(/^\/app\/feishu\/bots\/([a-f0-9-]{36})$/);
    if (!botMatch || !uuid.test(botMatch[1]))
      throw new AppError(404, "Not found.");
    const id = botMatch[1];
    const old = store.get("feishu_bots", id) || {
      id,
      name: "",
      revision: 0,
      enabled: false,
      app_id: "",
      app_secret: "",
      routes: [],
    };
    if (method === "GET") return publicFeishu(old);
    if (method === "DELETE") {
      if (old.enabled) throw new AppError(409, "请先停用机器人。");
      if (
        [
          ...store.list("feishu_inbound"),
          ...store.list("feishu_outbound"),
        ].some(
          (row) =>
            row.app_id === old.app_id &&
            !["delivered", "rejected"].includes(row.state),
        )
      )
        throw new AppError(409, "机器人仍有未完成投递，请先处理。");
      if (store.list("schedules").some((row) => row.delivery?.bot_id === id))
        throw new AppError(409, "机器人仍被定时任务引用，请先修改投递目标。");
      store.remove("feishu_bots", id);
      return {};
    }
    if (method !== "PUT") throw new AppError(405, "Method not allowed.");
    if (old.revision !== body.revision)
      throw new AppError(409, "配置已更新，请重新加载。");
    if (body.brand !== undefined && !["feishu", "lark"].includes(body.brand))
      throw new AppError(400, "机器人地区无效。");
    const name = text(body.name || "Feishu", "机器人名称", 80, true);
    const app_id = text(body.app_id, "App ID", 100).trim();
    if (
      app_id &&
      store
        .list("feishu_bots")
        .some((row) => row.id !== id && row.app_id === app_id)
    )
      throw new AppError(409, "这个 App ID 已由另一机器人连接。");
    const secret = body.app_secret
      ? text(body.app_secret, "App Secret", 300, true).trim()
      : old.app_secret;
    if (app_id !== old.app_id && !body.app_secret)
      throw new AppError(400, "更换 App ID 时请同时填写 App Secret。");
    if (
      typeof body.enabled !== "boolean" ||
      !Array.isArray(body.routes) ||
      body.routes.length > 20
    )
      throw new AppError(400, "飞书配置无效；最多配置 20 个群。");
    const routes = body.routes.map((route) => {
      const chat_id = text(route.chat_id, "群 ID", 100, true).trim();
      if (chat_id !== "p2p" && !/^oc_[a-zA-Z0-9_-]+$/.test(chat_id))
        throw new AppError(400, "群 ID 应以 oc_ 开头，私聊规则填写 p2p。");
      const agent = store.get(
          "agents",
          text(route.agent_id, "Agent", 36, true),
        ),
        runtime = store.get(
          "runtimes",
          text(route.runtime_id || agent?.runtime_id || "", "环境模板", 36),
        );
      if (!agent || (route.runtime_id && !runtime))
        throw new AppError(400, "请选择群对应的 Agent 和环境模板。");
      if (body.enabled) {
        if (!runtime)
          throw new AppError(400, "Agent 未配置有效的默认环境模板。");
        requireAutomaticRuntime(store, runtime.id);
        const restriction = sessionRestriction(agent, runtime);
        if (restriction) throw new AppError(400, restriction);
      }
      if (route.project_id && !store.get("projects", route.project_id))
        throw new AppError(400, "项目不存在。");
      return {
        chat_id,
        agent_id: agent.id,
        runtime_id: route.runtime_id || "",
        project_id: route.project_id || "",
        allow_agent_selection: route.allow_agent_selection === true,
      };
    });
    if (new Set(routes.map((r) => r.chat_id)).size !== routes.length)
      throw new AppError(400, "每个群只能有一条分配规则。");
    if (
      body.enabled &&
      (!/^cli_[0-9a-fA-F]{16}$/.test(app_id) || !secret || !routes.length)
    )
      throw new AppError(
        400,
        "启用前请配置 App ID、App Secret 和至少一个允许的群。",
      );
    const value = {
      id,
      name,
      app_id,
      app_secret: secret,
      brand: body.brand || old.brand || "feishu",
      history_context: body.history_context === true,
      enabled: body.enabled,
      routes,
      revision: old.revision + 1,
    };
    store.put("feishu_bots", value);
    return publicFeishu(value);
  };
}

export function feishuCard(text) {
  const [title, ...body] = String(text).split("\n");
  return {
    config: { wide_screen_mode: true },
    header: { title: { tag: "plain_text", content: title.slice(0, 100) } },
    elements: [{ tag: "markdown", content: body.join("\n").trim() || title }],
  };
}

export async function createFeishuTransport(settings, receive) {
  const { Client, WSClient, EventDispatcher, normalize, Domain } = await import(
    "@larksuiteoapi/node-sdk"
  );
  // SDK diagnostics may contain authenticated WebSocket URLs and message bodies.
  const logger = { error() {}, warn() {}, info() {}, debug() {}, trace() {} };
  const options = {
    appId: settings.app_id,
    appSecret: settings.app_secret,
    domain: settings.brand === "lark" ? Domain.Lark : Domain.Feishu,
    logger,
  };
  const client = new Client(options);
  const ws = new WSClient({ ...options, handshakeTimeoutMs: 15000 });
  let readContext;
  return {
    ...feishuOperations(client),
    readContext: (row, initial) => readContext(row, initial),
    async connect() {
      const result = await client.request({
        method: "GET",
        url: "/open-apis/bot/v3/info",
      });
      const bot = result.bot || result.data?.bot;
      if (result.code !== 0 || !bot?.open_id)
        throw new Error("Cannot resolve Feishu bot identity");
      readContext = feishuContextReader(
        client,
        { openId: bot.open_id, name: bot.app_name || "Parsar" },
        settings.history_context,
      );
      const dispatcher = new EventDispatcher({ logger }).register({
        "im.message.receive_v1": async (event) => {
          if (
            event.sender?.sender_type !== "user" ||
            ![
              "text",
              "post",
              "image",
              "file",
              "audio",
              "media",
              "merge_forward",
            ].includes(event.message?.message_type)
          )
            return;
          const message = await normalize(event, {
            botIdentity: {
              openId: bot.open_id,
              name: bot.app_name || "Parsar",
            },
            stripBotMentions: true,
          });
          // Unlike the SDK's convenience Channel queue, the dispatcher awaits
          // this commit before acknowledging. Storage failure returns a retryable ACK.
          receive(message);
        },
      });
      await ws.start({ eventDispatcher: dispatcher });
    },
    async disconnect() {
      ws.close({ force: true });
    },
    getConnectionStatus() {
      return ws.getConnectionStatus();
    },
  };
}

export function feishuOperations(client) {
  const confirmed = (result) => {
    if (result.code !== 0)
      throw new Error("Feishu did not confirm the operation");
    return result.data;
  };
  return {
    async send(row) {
      const text =
        row.plain_text && /^ou_[a-zA-Z0-9_-]+$/.test(row.sender_id || "")
          ? `<at user_id="${row.sender_id}">user</at> ${row.payload}`
          : row.payload;
      const data = {
        msg_type: row.plain_text ? "text" : "interactive",
        content: JSON.stringify(row.plain_text ? { text } : feishuCard(text)),
        uuid: row.id,
      };
      const result = row.reply_to
        ? await client.im.message.reply({
            path: { message_id: row.reply_to },
            data: { ...data, reply_in_thread: row.chat_type !== "p2p" },
          })
        : await client.im.message.create({
            params: { receive_id_type: "chat_id" },
            data: { ...data, receive_id: row.chat_id },
          });
      const value = confirmed(result);
      if (!value?.message_id)
        throw new Error("Feishu did not return a message identifier");
      return { id: value.message_id };
    },
    async edit(id, text) {
      confirmed(
        await client.im.message.patch({
          path: { message_id: id },
          data: { content: JSON.stringify(feishuCard(text)) },
        }),
      );
    },
    async resource(row, resource) {
      return (
        await client.im.messageResource.get({
          path: {
            message_id: resource.message_id || row.message_id,
            file_key: resource.fileKey,
          },
          params: { type: resource.type === "image" ? "image" : "file" },
        })
      ).getReadableStream();
    },
    async beginTyping(row) {
      return confirmed(
        await client.im.messageReaction.create({
          path: { message_id: row.message_id },
          data: { reaction_type: { emoji_type: "Typing" } },
        }),
      )?.reaction_id;
    },
    async endTyping(row) {
      confirmed(
        await client.im.messageReaction.delete({
          path: {
            message_id: row.message_id,
            reaction_id: row.typing_reaction_id,
          },
        }),
      );
    },
  };
}

// The maintained SDK owns authentication, mention parsing and WebSocket lifecycle.
// SQLite owns delivery receipts across reconnects and process restarts.
export function feishuWorker(store, core, product, makeChannel, configId) {
  let channel,
    active,
    timer,
    stopping = false,
    running;
  const health = (state) =>
    store.put("feishu_health", {
      id: configId,
      state,
      updated_at: stamp(),
    });
  const allowed = (appId, chatId, chatType = "group") => {
    const config = store.get("feishu_bots", configId);
    return (
      config?.enabled &&
      config.app_id === appId &&
      config.routes.some((r) =>
        chatType === "p2p" ? r.chat_id === "p2p" : r.chat_id === chatId,
      )
    );
  };
  const threadFor = (appId, message) =>
    store
      .list("feishu_threads")
      .find(
        (row) =>
          row.app_id === appId &&
          row.chat_id === message.chatId &&
          (row.root_id ===
            (message.rootId || message.replyToMessageId || message.messageId) ||
            (message.threadId && row.thread_id === message.threadId) ||
            store
              .list("feishu_outbound")
              .some(
                (out) =>
                  out.thread_id === row.id &&
                  out.sent_message_id === message.replyToMessageId,
              )),
      );
  const receive = (appId, message) => {
    if (
      !allowed(appId, message.chatId, message.chatType) ||
      !["group", "p2p"].includes(message.chatType) ||
      (message.chatType === "group" &&
        !message.mentionedBot &&
        !threadFor(appId, message)) ||
      !message.senderId ||
      message.mentionAll
    )
      return;
    if (!message.content?.trim() || message.content.length > 40000) return;
    const id = stableId(`${appId}:${message.messageId}`);
    if (store.get("feishu_inbound", id)) return;
    const route = store
      .get("feishu_bots", configId)
      .routes.find((r) =>
        message.chatType === "p2p"
          ? r.chat_id === "p2p"
          : r.chat_id === message.chatId,
      );
    let thread =
      message.chatType === "p2p" &&
      !["/new", "/reset"].includes(message.content.trim())
        ? store
            .list("feishu_threads")
            .find(
              (row) =>
                row.app_id === appId &&
                row.chat_id === message.chatId &&
                row.sender_id === message.senderId &&
                row.chat_type === "p2p",
            )
        : threadFor(appId, message);
    if (
      thread &&
      !store.get("tasks", thread.task_id) &&
      !message.content.trim().startsWith("/") &&
      (store
        .get("feishu_inbound", thread.first_message_id)
        ?.text.startsWith("/") ||
        ["rejected", "delivered"].includes(
          store.get("feishu_inbound", thread.first_message_id)?.state,
        ))
    ) {
      thread = { ...thread, first_message_id: id };
    }
    const root =
      message.rootId || message.replyToMessageId || message.messageId;
    const threadId =
      thread?.id || stableId(`${appId}:${message.chatId}:${root}`);
    store.transaction(() => {
      if (thread)
        store.put("feishu_threads", { ...thread, settled_at: undefined });
      if (!thread)
        store.put("feishu_threads", {
          id: threadId,
          app_id: appId,
          chat_id: message.chatId,
          chat_type: message.chatType,
          root_id: root,
          thread_id: message.threadId,
          context_data: message.context,
          task_id: randomUUID(),
          session_id: randomUUID(),
          agent_id: route.agent_id,
          shared: route.allow_agent_selection,
          runtime_id: route.runtime_id,
          project_id: route.project_id,
          first_message_id: id,
          sender_id: message.senderId,
          created_at: stamp(),
        });
      store.put("feishu_inbound", {
        id,
        app_id: appId,
        chat_id: message.chatId,
        chat_type: message.chatType,
        thread_id: threadId,
        message_id: message.messageId,
        sender_id: message.senderId,
        text: message.content.trim(),
        raw_content_type: message.rawContentType,
        reply_to_message_id: message.replyToMessageId,
        occurred_at: message.createTime || stamp(),
        context_data: message.context,
        resources: (message.resources || []).map(
          ({ type, fileKey, fileName }) => ({ type, fileKey, fileName }),
        ),
        state: "queued",
        attempts: 0,
        created_at: stamp(),
        next_at: 0,
      });
    });
  };
  const queueReply = (id, thread, payload, notify = false) => {
    if (!store.get("feishu_outbound", id))
      store.put("feishu_outbound", {
        id,
        app_id: thread.app_id,
        chat_id: thread.chat_id,
        chat_type: thread.chat_type,
        thread_id: thread.id,
        task_id: thread.task_id,
        reply_to: thread.root_id,
        payload,
        notify,
        state: "queued",
        attempts: 0,
        created_at: stamp(),
        next_at: 0,
      });
  };
  const reject = (row, reason) => {
    store.put("feishu_inbound", {
      ...row,
      state: "rejected",
      media: undefined,
      context: undefined,
      error: reason,
    });
    const thread = store.get("feishu_threads", row.thread_id);
    if (thread) queueReply(stableId(`${row.id}:rejected`), thread, reason);
  };
  const cancel = async (row) => {
    let thread = store.get("feishu_threads", row.thread_id);
    if (!thread) return reject(row, "话题关联不可用，请在 Web 中检查。");
    let run =
      store.get("sessions", thread.session_id) ||
      store.list("sessions").find((value) => value.task_id === thread.task_id);
    if (run?.connection_requirement && row.sender_id !== thread.sender_id)
      return reject(row, "此任务使用发起者的个人连接，请新建自己的任务。");
    if (run?.connection_wait && !run.core_session_id) {
      await product("DELETE", `/app/sessions/${run.id}`, {});
      store.put("feishu_inbound", { ...row, state: "delivered" });
      queueReply(
        stableId(`${row.id}:cancel`),
        thread,
        "已取消等待授权的任务。",
      );
      return;
    }
    // Resolve an uncertain initial creation before cancelling its actual Session.
    if (run && !run.core_session_id)
      run = await product(
        "PUT",
        `/app/tasks/${thread.task_id}/runs/${run.id}`,
        {},
      );
    if (run && run.id !== thread.session_id) {
      thread = { ...thread, session_id: run.id };
      store.put("feishu_threads", thread);
    }
    store.transaction(() => {
      for (const previous of store.list("feishu_inbound").reverse()) {
        if (previous.id === row.id) break;
        if (
          previous.thread_id === row.thread_id &&
          !["delivered", "rejected"].includes(previous.state)
        )
          store.put("feishu_inbound", {
            ...previous,
            state: "rejected",
            error: "已被后续取消请求撤回，不会重新提交。",
          });
      }
      if (!run) {
        queueReply(
          stableId(`${row.id}:cancel`),
          thread,
          "已撤回此前等待处理的消息；此话题没有已启动的执行。",
        );
        store.put("feishu_inbound", {
          ...row,
          state: "delivered",
          error: undefined,
        });
      }
    });
    if (run) await inbound(row);
  };
  const mediaFor = async (row) => {
    if (row.media) return row.media;
    const thread = store.get("feishu_threads", row.thread_id);
    const context =
      row.context ||
      (await channel.readContext(row, thread?.first_message_id === row.id));
    row = { ...row, context };
    store.put("feishu_inbound", row);
    let input = context.text.replace(
      /<(?:file|audio|video) key="[^"]*"[^>]*\/>/g,
      "",
    );
    for (const resource of context.resources || [])
      if (resource.type === "image")
        input = input.split(`![image](${resource.fileKey})`).join("");
    const title =
      row.text
        .replace(/!\[image\]\([^)]*\)/g, "")
        .replace(/<(?:file|audio|video) key="[^"]*"[^>]*\/>/g, "")
        .trim() ||
      ((context.resources || []).some((resource) => resource.type === "image")
        ? "图片消息"
        : "文件消息");
    const media = {
      images: [],
      files: [],
      text: input.trim() || title,
      title,
      notice: context.notice,
    };
    if ((context.resources?.length || 0) > 8)
      throw new AppError(400, "每条消息最多支持 8 个附件。");
    let total = 0;
    for (const resource of context.resources || []) {
      const response = await channel.resource(row, resource);
      const chunks = [];
      for await (const chunk of response) {
        total += chunk.length;
        if (total > 5 * 1024 * 1024)
          throw new AppError(413, "附件合计不能超过 5 MiB。");
        chunks.push(chunk);
      }
      const bytes = Buffer.concat(chunks);
      if (resource.type === "image") {
        const mime = bytes
          .subarray(0, 8)
          .equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))
          ? "png"
          : bytes[0] === 255 && bytes[1] === 216
            ? "jpeg"
            : bytes.toString("ascii", 8, 12) === "WEBP"
              ? "webp"
              : "";
        if (!mime) throw new AppError(400, "图片需为 PNG、JPEG 或 WebP。");
        media.images.push(
          `data:image/${mime};base64,${bytes.toString("base64")}`,
        );
        if (JSON.stringify(media.images).length > 900000)
          throw new AppError(
            413,
            "图片超过公开消息接口大小限制，请缩小后重试。",
          );
      } else {
        const filename = (resource.fileName || "attachment")
          .replace(/[^a-zA-Z0-9_.-]/g, "_")
          .slice(-100);
        const path = `/workspace/inputs/${row.id}-${media.files.length}-${filename}`;
        media.files.push({
          type: "inline",
          path,
          data: bytes.toString("base64"),
        });
        media.text += `\nAttachment: ${path.replace("/workspace/", "./")}`;
      }
    }
    store.put("feishu_inbound", { ...row, media });
    return media;
  };
  const inbound = async (row) => {
    let thread = store.get("feishu_threads", row.thread_id);
    if (!thread) return reject(row, "话题关联不可用，请在 Web 中检查。");
    const route = store
      .get("feishu_bots", configId)
      .routes.find((value) =>
        row.chat_type === "p2p"
          ? value.chat_id === "p2p"
          : value.chat_id === row.chat_id,
      );
    if (["/new", "/reset", "/help"].includes(row.text)) {
      queueReply(
        stableId(`${row.id}:command`),
        thread,
        row.text === "/help"
          ? "发送任务开始工作。/list 列出 Agent；/select <名称或 ID> 切换；/new 或 /reset 新建私聊对话（群聊请另开话题）；/cancel 停止执行；/review 待验收；/done 完成任务；/reopen 重新打开；/answer <call_id> <回答> 回复问题。"
          : row.chat_type === "p2p"
            ? "新对话已就绪，请发送任务。"
            : "群聊请另开话题并提及机器人，以开始新对话。当前话题保持不变。",
      );
      store.put("feishu_inbound", { ...row, state: "delivered" });
      return;
    }
    if (row.text === "/list" || /^\/select(?:\s|$)/.test(row.text)) {
      if (!route?.allow_agent_selection)
        return reject(
          row,
          "此群使用固定 Agent；可在 Web 飞书配置中开启共享机器人切换。",
        );
      const agents = store.list("agents");
      let selection, reply;
      if (row.text === "/list") {
        reply =
          agents
            .map((agent) => `${agent.name} — ${agent.id}`)
            .join("\n")
            .slice(0, 5000) +
          "\n\n发送 /select <完整名称或 ID> 选择新任务的 Agent。选择仅影响你在此群发起的新任务；已有话题保持原会话。";
      } else {
        const target = row.text.slice(7).trim();
        const matches = agents.filter(
          (agent) => agent.id === target || agent.name === target,
        );
        if (matches.length !== 1)
          return reject(
            row,
            "请用 /list 查看 Agent，再用唯一名称或完整 ID 选择。",
          );
        const agent = matches[0],
          runtime = store.get("runtimes", route.runtime_id || agent.runtime_id);
        if (!runtime)
          return reject(
            row,
            "请为所选 Agent 配置默认环境模板，或为飞书接入指定环境模板。",
          );
        try {
          requireAutomaticRuntime(store, runtime.id);
        } catch (error) {
          return reject(row, error.message);
        }
        const restriction = sessionRestriction(agent, runtime);
        if (restriction) return reject(row, restriction);
        selection = {
          id: selectionId(row.app_id, row.chat_id, row.sender_id),
          agent_id: agent.id,
        };
        reply = `已选择 ${agent.name}。你在此群发起的新任务将使用这个 Agent；已有话题保持原会话。`;
      }
      store.transaction(() => {
        if (selection) store.put("feishu_selections", selection);
        queueReply(stableId(`${row.id}:selection`), thread, reply);
        store.put("feishu_inbound", {
          ...row,
          state: "delivered",
          error: undefined,
        });
      });
      return;
    }
    const media = await mediaFor(row);
    row = store.get("feishu_inbound", row.id);
    const first = thread.first_message_id === row.id;
    let task = store.get("tasks", thread.task_id);
    if (!task) {
      if (!first)
        return reject(row, "话题任务不可用，请重新开一个话题描述任务。");
      if (row.text.startsWith("/"))
        return reject(row, "请先 @机器人 描述任务，再在同一话题中发送命令。");
      if (!thread.assignment_resolved) {
        const selected =
          thread.shared && route?.allow_agent_selection
            ? store.get(
                "feishu_selections",
                selectionId(row.app_id, row.chat_id, row.sender_id),
              )
            : undefined;
        const agent = store.get(
          "agents",
          selected?.agent_id || thread.agent_id,
        );
        if (!agent) throw new AppError(400, "Agent 不存在。");
        const runtime = store.get(
          "runtimes",
          thread.runtime_id || agent.runtime_id,
        );
        if (!runtime)
          throw new AppError(400, "Agent 未配置有效的默认环境模板。");
        requireAutomaticRuntime(store, runtime.id);
        const restriction = sessionRestriction(agent, runtime);
        if (restriction) throw new AppError(400, restriction);
        thread = {
          ...thread,
          agent_id: agent.id,
          runtime_id: runtime.id,
          assignment_resolved: true,
        };
        store.put("feishu_threads", thread);
      }
      task = await product("PUT", `/app/tasks/${thread.task_id}`, {
        name: media.title.split("\n")[0].slice(0, 80),
        description: media.text,
        project_id: thread.project_id,
        agent_id: thread.agent_id,
        runtime_id: thread.runtime_id,
        status: "backlog",
        priority: "normal",
      });
    }
    if (first) {
      // A manually recovered task may already have the occurrence's Session.
      if (!store.get("sessions", thread.session_id)) {
        const recovered = store
          .list("sessions")
          .find((run) => run.task_id === task.id);
        if (recovered) {
          thread = { ...thread, session_id: recovered.id };
          store.put("feishu_threads", thread);
        }
      }
      if (!store.get("sessions", thread.session_id))
        requireAutomaticRuntime(store, task.runtime_id);
      const dispatched = await product(
        "PUT",
        `/app/tasks/${task.id}/runs/${thread.session_id}`,
        {
          revision: task.revision,
          images: media.images,
          files: media.files,
        },
      );
      queueReply(
        stableId(`${row.id}:accepted`),
        thread,
        `${media.notice ? media.notice + "\n" : ""}已接收任务「${task.name}」。${dispatched.connection_wait ? "正在等待你连接飞书账号，授权后自动继续。" : "正在交给 Agent 执行。"}\n请在本话题中直接追问；/cancel 取消执行，/review 提交验收，/done 确认完成，/reopen 重新打开。\n任务编号：${task.id}（在 Parsar「任务」中搜索）`,
      );
    } else {
      const run = store.get("sessions", thread.session_id);
      if (run?.deleted_at)
        return reject(row, "此话题会话已删除，请用 /new 或新话题开始。");
      if (run?.connection_requirement && row.sender_id !== thread.sender_id)
        return reject(row, "此任务使用发起者的个人连接，请新建自己的任务。");
      if (run?.connection_wait && !run.core_session_id) {
        if (row.text === "/connect")
          await product("POST", `/app/sessions/${run.id}/connection`, {});
        else if (row.text === "/cancel") {
          store.put("sessions", {
            ...run,
            deleted_at: Date.now(),
            connection_wait: undefined,
          });
          queueReply(
            stableId(`${row.id}:cancel`),
            thread,
            "已取消等待授权的任务。",
          );
        } else
          return reject(
            row,
            "任务正在等待连接，授权后会自动继续。链接过期可发送 /connect，取消可发送 /cancel。",
          );
        store.put("feishu_inbound", { ...row, state: "delivered" });
        return;
      }
      if (!run?.core_session_id)
        return reject(row, "执行会话未确认，请先在 Web 中恢复原执行。");
      const commands = {
        "/review": "review",
        "/done": "done",
        "/reopen": "backlog",
      };
      if (commands[row.text]) {
        if (row.state !== "submitting") {
          try {
            await product("PUT", `/app/tasks/${task.id}`, {
              ...task,
              status: commands[row.text],
            });
          } catch (error) {
            if (error instanceof AppError && error.status === 409)
              return reject(row, error.message);
            throw error;
          }
          store.put("feishu_inbound", { ...row, state: "submitting" });
        }
        queueReply(
          stableId(`${row.id}:command`),
          thread,
          `任务状态已更新：${taskStatuses[commands[row.text]]}。`,
        );
      } else {
        if (!row.event && task.status === "done" && row.text !== "/cancel")
          return reject(row, "任务已完成，请先发送 /reopen 重新打开。");
        if (
          row.text.startsWith("/") &&
          row.text !== "/cancel" &&
          !row.text.startsWith("/answer ")
        )
          return reject(
            row,
            "支持的命令：/list、/select、/cancel、/review、/done、/reopen。其他消息会交给 Agent。",
          );
        if (
          media.files.length &&
          !row.files_uploaded &&
          row.text !== "/cancel"
        ) {
          const session = await core(
            `/v1/agents/sessions/${run.core_session_id}`,
            "GET",
          );
          if (session.environment?.type !== "openai_hosted")
            throw new AppError(400, "附件需要托管运行时。");
          if (session.status !== "idle")
            throw new AppError(409, "等待当前执行结束后写入附件。");
          for (const file of media.files)
            await core(
              `/v1/agents/environments/${session.environment.id}/files`,
              "POST",
              file,
            );
          row = { ...row, files_uploaded: true };
          store.put("feishu_inbound", row);
        }
        let answer;
        if (!row.event && row.text !== "/cancel") {
          const session = await core(
            `/v1/agents/sessions/${run.core_session_id}`,
            "GET",
          );
          const questions = (session.required_actions || []).filter(
            (action) =>
              action.type === "function_call" && action.name === "ask_user",
          );
          const match = row.text.match(/^\/answer\s+(\S+)\s+([\s\S]+)$/);
          const question = match
            ? questions.find((action) => action.call_id === match[1])
            : questions.length === 1
              ? questions[0]
              : undefined;
          if (match && !question)
            return reject(row, "这个问题已结束或标识无效，请查看最新提问。");
          if (question)
            answer = {
              type: "agent.session.input.tool_result",
              call_id: question.call_id,
              turn_id: question.turn_id,
              success: true,
              output: match ? match[2] : row.text,
            };
        }
        const event =
          row.event ||
          answer ||
          (row.text === "/cancel"
            ? { type: "agent.session.input.cancel" }
            : {
                type: "agent.session.input.message",
                input: [
                  {
                    role: "user",
                    content: [
                      { type: "input_text", text: media.text },
                      ...media.images.map((image_url) => ({
                        type: "input_image",
                        image_url,
                      })),
                    ],
                  },
                ],
              });
        store.put("feishu_inbound", { ...row, state: "submitting", event });
        await core(
          `/v1/agents/sessions/${run.core_session_id}/events`,
          "POST",
          { events: [event] },
          row.id,
        );
        if (row.text === "/cancel")
          queueReply(
            stableId(`${row.id}:cancel`),
            thread,
            "已请求取消，等待 Agent 确认停止。",
          );
      }
    }
    store.transaction(() => {
      store.put("feishu_inbound", {
        ...row,
        state: "delivered",
        media: undefined,
        context: undefined,
        error: undefined,
      });
      if (!store.get("activity", row.id))
        store.put("activity", {
          id: row.id,
          task_id: task.id,
          type: "feishu",
          message: row.text,
          actor: row.sender_id,
          created_at: Math.floor(row.created_at / 1000),
        });
    });
  };
  const history = async (sessionId, resource, after) => {
    const rows = [],
      cursors = new Set(after ? [after] : []);
    for (;;) {
      const page = await core(
        `/v1/agents/sessions/${sessionId}/${resource}?order=asc&limit=100${after ? `&after=${encodeURIComponent(after)}` : ""}`,
        "GET",
      );
      rows.push(...page.data);
      if (!page.has_more) return rows;
      if (!page.last_id || cursors.has(page.last_id))
        throw new AppError(422, "Invalid history cursor");
      after = page.last_id;
      cursors.add(after);
    }
  };
  const observe = async (thread) => {
    const run = store.get("sessions", thread.session_id);
    if (run?.connection_wait && !run.deleted_at) {
      const login = run.connection_wait;
      const text =
        login.status === "pending"
          ? `请用发起任务的飞书账号完成授权，授权后任务自动继续。\n${login.verification_url}\n${login.user_code ? `验证码：${login.user_code}\n` : ""}链接过期可发送 /connect 重新连接。`
          : `${login.error || "授权已过期或被取消。"}\n发送 /connect 重新连接，原任务仍保留。`;
      queueReply(
        stableId(`${thread.id}:connection:${login.id}:${login.status}`),
        thread,
        text,
      );
      return;
    }
    if (!run?.core_session_id || run.deleted_at) return;
    const sid = run.core_session_id;
    const [session, turns] = await Promise.all([
      core(`/v1/agents/sessions/${sid}`, "GET"),
      history(sid, "turns", thread.turn_cursor),
    ]);
    if (["idle", "failed", "requires_action"].includes(session.status)) {
      for (const row of store
        .list("feishu_inbound")
        .filter(
          (row) =>
            row.thread_id === thread.id &&
            row.typing_reaction_id &&
            ["delivered", "rejected", "failed"].includes(row.state),
        )) {
        try {
          await channel.endTyping(row);
          store.put("feishu_inbound", {
            ...row,
            typing_reaction_id: undefined,
          });
        } catch {
          /* Reactions are best effort; retry removal on the next observation. */
        }
      }
    }
    for (const action of session.required_actions || []) {
      if (action.type !== "function_call" || action.name !== "ask_user")
        continue;
      let args = action.arguments;
      try {
        if (typeof args === "string") args = JSON.parse(args);
      } catch {
        args = {};
      }
      queueReply(
        stableId(`${thread.id}:question:${action.call_id}`),
        thread,
        `Agent 需要补充信息\n\n${typeof args?.question === "string" ? args.question : "请补充任务信息"}\n${Array.isArray(args?.options) ? args.options.filter((value) => typeof value === "string").join("\n") : ""}\n\n回复本话题即可回答，或发送 /answer ${action.call_id} <回答>`,
        true,
      );
    }
    const unseen = turns.filter(
      (turn) =>
        ["completed", "failed", "cancelled"].includes(turn.status) &&
        !store.get("feishu_outbound", stableId(`${thread.id}:${turn.id}`)),
    );
    if (session.status === "failed" && !turns.length && !thread.turn_cursor)
      queueReply(
        stableId(`${thread.id}:initial-failed`),
        thread,
        "执行环境准备失败或初始输入超时，请在 Web 任务中检查并重新执行。",
      );
    const progress = store.get(
      "feishu_outbound",
      stableId(`${thread.first_message_id}:accepted`),
    );
    if (progress?.sent_message_id && session.status !== "in_progress") {
      const latest = turns.at(-1);
      const text =
        session.status === "requires_action"
          ? "Agent 等待补充信息，请回复提问卡片。"
          : session.status === "failed" || latest?.status === "failed"
            ? "Agent 执行失败，请查看后续结果卡片。"
            : latest?.status === "cancelled"
              ? "Agent 已停止执行。"
              : latest?.status === "completed"
                ? "Agent 执行结束，结果见后续卡片。"
                : null;
      if (text && thread.progress_text !== text) {
        try {
          await channel.edit(progress.sent_message_id, text, thread);
          thread = { ...thread, progress_text: text };
          store.put("feishu_threads", thread);
        } catch {
          // A deleted or uneditable progress card must not block result delivery.
        }
      }
    }
    if (progress?.sent_message_id && session.status === "in_progress") {
      const items = await history(sid, "items", thread.item_cursor);
      const output = items
        .filter((item) => item.type === "message" && item.role === "assistant")
        .flatMap((item) => item.content || [])
        .filter((part) => part.type === "output_text")
        .map((part) => part.text)
        .join("\n");
      const text = `Agent 正在执行\n\n${output.slice(-4000)}\n\n已记录 ${items.filter((item) => item.type !== "message").length} 个执行步骤`;
      if (thread.progress_text !== text) {
        try {
          await channel.edit(progress.sent_message_id, text, thread);
          thread = { ...thread, progress_text: text };
          store.put("feishu_threads", thread);
        } catch {
          // A deleted or uneditable progress card must not block result delivery.
        }
      }
    }
    if (!unseen.length) {
      const pending = store
        .list("feishu_inbound")
        .some(
          (row) =>
            row.thread_id === thread.id &&
            ["queued", "submitting"].includes(row.state),
        );
      const settled_at =
        ["idle", "failed"].includes(session.status) && !pending
          ? thread.settled_at || stamp()
          : undefined;
      if (settled_at !== thread.settled_at)
        store.put("feishu_threads", { ...thread, settled_at });
      return;
    }
    const [items, artifacts] = await Promise.all([
      history(sid, "items", thread.item_cursor),
      history(sid, "artifacts", thread.artifact_cursor),
    ]);
    for (const turn of unseen) {
      const output = items
        .filter(
          (item) =>
            item.turn_id === turn.id &&
            item.type === "message" &&
            item.role === "assistant",
        )
        .flatMap((item) => item.content || [])
        .filter((part) => part.type === "output_text")
        .map((part) => part.text)
        .join("\n\n");
      const files = artifacts
        .filter((file) => file.turn_id === turn.id)
        .map((file) => file.name || file.path || file.id)
        .join("、");
      const summary =
        turn.status === "completed"
          ? "Agent 执行结束，请验收结果。"
          : turn.status === "cancelled"
            ? "Agent 已停止执行。"
            : "Agent 执行失败，请在 Web 会话中检查。";
      queueReply(
        stableId(`${thread.id}:${turn.id}`),
        thread,
        `${summary}\n\n${output.slice(0, 5000)}${output.length > 5000 ? "\n…完整结果请在 Web 查看。" : ""}${files ? `\n\n产物：${files}（在 Web 会话下载）` : ""}\n\n任务编号：${thread.task_id}（在 Parsar「任务」中搜索）`,
        true,
      );
    }
    // Advance only through terminal Turns. Keep output for an active Turn available.
    const terminal = turns.findIndex(
      (turn) => !["completed", "failed", "cancelled"].includes(turn.status),
    );
    const completed = terminal < 0 ? turns : turns.slice(0, terminal);
    const ids = new Set(completed.map((turn) => turn.id));
    const cursor = (rows, previous) => {
      let value = previous;
      for (const row of rows) {
        if (!ids.has(row.turn_id)) break;
        value = row.id;
      }
      return value;
    };
    if (completed.length)
      store.put("feishu_threads", {
        ...thread,
        turn_cursor: completed.at(-1).id,
        item_cursor: cursor(items, thread.item_cursor),
        artifact_cursor: cursor(artifacts, thread.artifact_cursor),
      });
  };
  const retry = (kind, row, error) => {
    const attempts = row.attempts + 1;
    const definite = !transientError(error);
    store.put(kind, {
      ...store.get(kind, row.id),
      attempts,
      state: definite ? "failed" : store.get(kind, row.id).state,
      error: definite
        ? error.message
        : "连接或请求未确认，将按原标识重试；失败记录保留以供检查。",
      next_at:
        stamp() +
        (error instanceof AppError && error.status === 409
          ? 10000
          : Math.min(300000, 1000 * 2 ** attempts)),
    });
    if (kind === "feishu_inbound" && definite) {
      const thread = store.get("feishu_threads", row.thread_id);
      if (thread)
        queueReply(
          stableId(`${row.id}:failed`),
          thread,
          "这条消息尚未成功处理。请在 Parsar 的飞书页面检查投递记录，并用原标识重试；不要重复创建任务。",
        );
    }
  };
  const deliver = async (current) => {
    for (const row of store.list("feishu_outbound").reverse()) {
      if (!current()) return;
      if (
        !allowed(row.app_id, row.chat_id, row.chat_type) ||
        row.state !== "queued" ||
        row.next_at > stamp()
      )
        continue;
      // Never replay an uncertain send outside the Feishu’s one-hour deduplication window.
      if (
        row.first_attempt_at &&
        stamp() - row.first_attempt_at >= 55 * 60000
      ) {
        store.put("feishu_outbound", {
          ...row,
          state: "failed",
          error: "回传结果未确认且幂等窗口已过，请人工检查飞书话题。",
        });
        continue;
      }
      store.put("feishu_outbound", {
        ...row,
        first_attempt_at: row.first_attempt_at || stamp(),
      });
      try {
        const thread = store.get("feishu_threads", row.thread_id);
        const root = row.reply_to || thread?.root_id;
        const result = await channel.send({
          ...row,
          reply_to: root,
          context_data: thread?.context_data,
          sender_id: thread?.sender_id,
        });
        if (thread && !thread.root_id)
          store.put("feishu_threads", {
            ...thread,
            root_id: result.id,
          });
        store.transaction(() => {
          store.put("feishu_outbound", {
            ...store.get("feishu_outbound", row.id),
            state: "delivered",
            sent_message_id: result.id,
            error: undefined,
          });
          if (row.notify && thread?.sender_id) {
            const id = stableId(`${row.id}:ping`);
            if (!store.get("feishu_outbound", id))
              store.put("feishu_outbound", {
                ...row,
                id,
                notify: false,
                plain_text: true,
                payload: row.payload.split("\n")[0],
                reply_to: result.id,
                state: "queued",
                attempts: 0,
                first_attempt_at: undefined,
                next_at: 0,
              });
          }
        });
      } catch (error) {
        retry("feishu_outbound", row, error);
      }
    }
  };
  const tick = async () => {
    const config = store.get("feishu_bots", configId);
    for (const occurrence of store.list("schedule_occurrences")) {
      if (
        occurrence.delivery?.bot_id !== configId ||
        occurrence.delivery_thread_id ||
        occurrence.delivery_error
      )
        continue;
      if (
        !config?.enabled ||
        occurrence.delivery.app_id !== config.app_id ||
        !config.routes.some(
          (route) => route.chat_id === occurrence.delivery.chat_id,
        )
      )
        store.put("schedule_occurrences", {
          ...occurrence,
          delivery_error:
            "飞书机器人已停用或投递路由已变更，结果保留在任务中。",
        });
    }
    if (!config?.enabled) {
      if (channel) await channel.disconnect();
      channel = undefined;
      active = undefined;
      health("stopped");
      return;
    }
    if (
      !active ||
      active.revision !== config.revision ||
      channel.getConnectionStatus().state === "failed"
    ) {
      if (channel) await channel.disconnect();
      channel = undefined;
      active = undefined;
      health("connecting");
      const next = await makeChannel(config, (message) =>
        receive(config.app_id, message),
      );
      channel = next;
      try {
        await channel.connect();
        active = config;
      } catch {
        await channel.disconnect();
        channel = undefined;
        health("error");
        return;
      }
    }
    active = config;
    const current = () =>
      store.get("feishu_bots", configId)?.revision === active.revision;
    if (!current()) return;
    health(channel.getConnectionStatus().state);
    for (const occurrence of store.list("schedule_occurrences")) {
      if (
        occurrence.delivery_thread_id ||
        occurrence.delivery_error ||
        occurrence.delivery?.bot_id !== configId ||
        occurrence.delivery.app_id !== config.app_id ||
        !allowed(config.app_id, occurrence.delivery.chat_id)
      )
        continue;
      const id = stableId(`schedule:${occurrence.id}`);
      if (store.get("feishu_threads", id)) continue;
      const task = store.get("tasks", occurrence.task_id);
      const thread = {
        id,
        app_id: config.app_id,
        chat_id: occurrence.delivery.chat_id,
        chat_type: "group",
        task_id: occurrence.task_id,
        session_id: occurrence.session_id,
        agent_id: task?.agent_id,
        runtime_id: task?.runtime_id,
        first_message_id: id,
        created_at: stamp(),
      };
      store.transaction(() => {
        store.put("feishu_threads", thread);
        store.put("schedule_occurrences", {
          ...occurrence,
          delivery_thread_id: thread.id,
        });
        queueReply(
          stableId(`${id}:accepted`),
          thread,
          `定时任务已开始\n\n${task?.name || occurrence.task_id}`,
        );
      });
    }
    await deliver(current);
    const rows = store.list("feishu_inbound").reverse();
    const cancellations = new Map();
    for (const row of rows)
      if (
        row.text === "/cancel" &&
        ["queued", "submitting"].includes(row.state)
      )
        cancellations.set(row.thread_id, row);
    const blocked = new Set(
      rows.filter((row) => row.state === "failed").map((row) => row.thread_id),
    );
    for (const row of cancellations.values()) {
      if (!current()) return;
      if (!allowed(row.app_id, row.chat_id, row.chat_type)) continue;
      if (row.next_at <= stamp()) {
        try {
          await cancel(row);
        } catch (error) {
          retry("feishu_inbound", row, error);
        }
      }
      if (store.get("feishu_inbound", row.id).state === "delivered")
        blocked.delete(row.thread_id);
      else blocked.add(row.thread_id);
    }
    for (const original of rows) {
      if (!current()) return;
      let row = store.get("feishu_inbound", original.id);
      if (
        !allowed(row.app_id, row.chat_id, row.chat_type) ||
        row.text === "/cancel" ||
        ["delivered", "failed", "rejected"].includes(row.state) ||
        blocked.has(row.thread_id)
      )
        continue;
      if (row.next_at <= stamp()) {
        try {
          if (!row.typing_attempted && !row.text.startsWith("/")) {
            store.put("feishu_inbound", { ...row, typing_attempted: true });
            try {
              const receipt = await channel.beginTyping(row);
              if (receipt)
                store.put("feishu_inbound", {
                  ...store.get("feishu_inbound", row.id),
                  typing_reaction_id: receipt,
                });
            } catch {
              /* Missing reaction scope must not prevent task execution. */
            }
            row = store.get("feishu_inbound", row.id);
          }
          await inbound(row);
        } catch (error) {
          retry("feishu_inbound", row, error);
        }
      }
      if (
        !["delivered", "rejected"].includes(
          store.get("feishu_inbound", row.id).state,
        )
      )
        blocked.add(row.thread_id);
    }
    for (const thread of store.list("feishu_threads")) {
      if (!current()) return;
      if (!allowed(thread.app_id, thread.chat_id, thread.chat_type)) continue;
      if (thread.settled_at && stamp() - thread.settled_at > 5 * 60000)
        continue;
      try {
        await observe(thread);
      } catch (error) {
        health("core_unavailable");
        if (
          !(error instanceof AppError) ||
          error.status >= 500 ||
          error.status === 429
        )
          break;
      }
    }
    await deliver(current);
  };
  const cycle = async () => {
    if (stopping) return;
    running = tick().catch(() => health("error"));
    await running;
    if (!stopping) timer = setTimeout(cycle, 5000);
  };
  return {
    receive,
    tick,
    start() {
      void cycle();
    },
    async stop() {
      stopping = true;
      clearTimeout(timer);
      await running;
      await channel?.disconnect();
    },
  };
}

export function feishuWorkers(store, core, product, makeChannel) {
  const workers = new Map();
  let timer,
    stopping = false,
    running;
  const tick = async () => {
    const ids = new Set(store.list("feishu_bots").map((row) => row.id));
    for (const [id, worker] of workers)
      if (!ids.has(id)) {
        await worker.stop();
        workers.delete(id);
      }
    for (const id of ids) {
      if (!workers.has(id))
        workers.set(id, feishuWorker(store, core, product, makeChannel, id));
      try {
        await workers.get(id).tick();
      } catch {
        store.put("feishu_health", { id, state: "error", updated_at: stamp() });
      }
    }
  };
  const cycle = async () => {
    if (stopping) return;
    running = tick();
    try {
      await running;
    } finally {
      if (!stopping) timer = setTimeout(cycle, 5000);
    }
  };
  return {
    tick,
    start() {
      void cycle();
    },
    async stop() {
      stopping = true;
      clearTimeout(timer);
      await running;
      await Promise.all([...workers.values()].map((worker) => worker.stop()));
    },
  };
}
