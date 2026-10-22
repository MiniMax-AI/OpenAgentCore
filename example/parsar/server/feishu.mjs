import { createHash, randomUUID } from "node:crypto";
const stableId = (value) => {
  const hex = createHash("sha256").update(value).digest("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`;
};
import { feishuContextReader } from "./feishu-context.mjs";
import { explainFailure, turnsAPI } from "./turns.mjs";
import { typicalWait, waitingHeadline } from "../shared/turn-status.mjs";
import { AppError, text, uuid, transientError } from "./store.mjs";

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
          const sessionId = store.get("feishu_threads", row.thread_id)?.session_id;
          return {
            ...row,
            session_id:
              sessionId && store.get("sessions", sessionId)?.core_session_id
                ? sessionId
                : undefined,
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
      agent_id: "",
      direct: false,
      groups: false,
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
    if (typeof body.enabled !== "boolean")
      throw new AppError(400, "飞书配置无效。");
    const agent_id = text(body.agent_id || "", "Agent", 36);
    const agent = agent_id && store.get("agents", agent_id);
    if (agent_id && !agent) throw new AppError(400, "Agent 不存在。");
    if (
      agent_id &&
      store
        .list("feishu_bots")
        .some((row) => row.id !== id && row.agent_id === agent_id)
    )
      throw new AppError(409, "这个 Agent 已绑定另一个机器人。");
    const direct = body.direct === true;
    // Group membership is the group boundary: any group the bot joins can mention it.
    const groups = body.groups === true;
    if (
      body.enabled &&
      (!/^cli_[0-9a-fA-F]{16}$/.test(app_id) ||
        !secret ||
        !agent ||
        (!direct && !groups))
    )
      throw new AppError(
        400,
        "启用前请配置 App ID、App Secret、Agent，并开启私聊或群聊。",
      );
    if (body.enabled && !store.get("runtimes", agent.runtime_id || ""))
      throw new AppError(400, "Agent 未配置环境模板。");
    const value = {
      id,
      name,
      app_id,
      app_secret: secret,
      brand: body.brand || old.brand || "feishu",
      enabled: body.enabled,
      agent_id,
      direct,
      groups,
      revision: old.revision + 1,
    };
    store.put("feishu_bots", value);
    return publicFeishu(value);
  };
}

const cardTones = {
  in_progress: ["处理中", "blue"],
  requires_action: ["需要你补充信息", "orange"],
  completed: ["已完成", "green"],
  cancelled: ["已停止", "grey"],
  failed: ["执行失败", "red"],
};
const grey = (text) => `<font color='grey'>${text}</font>`;

function panel({ title, content }) {
  return {
    tag: "collapsible_panel",
    expanded: false,
    vertical_spacing: "8px",
    padding: "4px 12px 8px 12px",
    border: { color: "grey", corner_radius: "6px" },
    header: {
      title: { tag: "markdown", content: grey(title) },
      width: "auto_when_fold",
      padding: "4px 0px 4px 0px",
      icon: {
        tag: "standard_icon",
        token: "down-small-ccm_outlined",
        size: "16px 16px",
      },
      icon_position: "right",
      icon_expanded_angle: -180,
    },
    elements: [{ tag: "markdown", content }],
  };
}

// Outbound payloads are either plain text or the serialized card built here.
function statusCard(status, body, panels = []) {
  const [title, template] = cardTones[status] || cardTones.failed;
  return JSON.stringify({
    schema: "2.0",
    config: { update_multi: true },
    header: { title: { tag: "plain_text", content: title }, template },
    body: {
      elements: [{ tag: "markdown", content: body }, ...panels.map(panel)],
    },
  });
}

export function feishuCard(payload) {
  const text = String(payload);
  if (text.startsWith("{")) return JSON.parse(text);
  const [title, ...rest] = text.split("\n");
  const body = rest.join("\n").trim();
  if (title === "执行失败" && body) return JSON.parse(statusCard("failed", body));
  return {
    schema: "2.0",
    config: { update_multi: true },
    body: { elements: [{ tag: "markdown", content: text }] },
  };
}

export function progressCard(text, notice) {
  return statusCard(
    "in_progress",
    `**${text}**${notice ? `\n${grey(notice)}` : ""}`,
  );
}

export function questionCard(question, options = []) {
  return statusCard(
    "requires_action",
    [
      question,
      options.map((option) => `- ${option}`).join("\n"),
      grey("直接回复这条消息即可回答。"),
    ]
      .filter(Boolean)
      .join("\n\n"),
  );
}

function step(item) {
  if (item.type === "command_execution" || typeof item.command === "string") {
    const command = String(item.command || "")
      .replace(/\s+/g, " ")
      .trim()
      .replace(/^\/bin\/(?:ba|z)?sh -lc (['"])([\s\S]*)\1$/, "$2");
    return ["运行命令", command.length > 56 ? `${command.slice(0, 55)}…` : command];
  }
  if (item.type === "mcp_call")
    return ["调用工具", [item.server_label, item.name].filter(Boolean).join(" · ")];
  if (item.type === "function_call")
    return [item.name === "ask_user" ? "向你提问" : "调用函数", item.name || ""];
  if (item.type === "web_search_call")
    return ["搜索网页", item.action?.query || item.action?.url || ""];
  if (item.type === "file_change") return ["修改文件", ""];
  return ["执行步骤", item.name || item.type || ""];
}

function stepLine(item) {
  const [label, detail] = step(item);
  const mark =
    item.status === "failed"
      ? "<font color='red'>✗</font>"
      : item.status === "completed" || item.status === undefined
        ? "<font color='green'>✓</font>"
        : grey("○");
  const seconds =
    typeof item.duration_ms === "number"
      ? grey(` ${Math.max(0, Math.round(item.duration_ms / 1000))}s`)
      : "";
  return `${mark} ${label}${detail ? ` \`${detail.replaceAll("`", "'")}\`` : ""}${seconds}`;
}

// Newcomers see the current step or the answer; collapsed panels keep the details.
export function formatTurnCard(status, items = [], error, turn) {
  const assistant = items.filter(
    (item) => item.type === "message" && item.role === "assistant",
  );
  const text = (item) =>
    (item.content || [])
      .filter((part) => part.type === "output_text")
      .map((part) => (part.text || "").trim())
      .filter(Boolean)
      .join("\n");
  const messages = assistant.map(text).filter(Boolean);
  const reasoning = items
    .filter((item) => item.type === "reasoning")
    .flatMap((item) => item.summary || item.content || [])
    .map((part) => (typeof part === "string" ? part : part?.text || "").trim())
    .filter(Boolean)
    .join("\n");
  const steps = items.filter(
    (item) => item.type !== "message" && item.type !== "reasoning",
  );
  const thinking = [
    reasoning,
    ...(status === "in_progress" ? messages : messages.slice(0, -1)),
  ]
    .filter(Boolean)
    .join("\n\n");
  // Both panels are always present so experienced users know where to look.
  const panels = [
    {
      title: "思考过程",
      content: thinking ? thinking.slice(-1800) : grey("这一轮没有返回思考内容。"),
    },
    {
      title: `执行记录 · ${steps.length} 步`,
      content: steps.length
        ? [
            steps.length > 12 ? grey(`前面还有 ${steps.length - 12} 步`) : "",
            steps.slice(-12).map(stepLine).join("\n"),
          ]
            .filter(Boolean)
            .join("\n\n")
        : grey("这一轮没有运行命令或调用工具。"),
    },
  ];
  if (status === "in_progress") {
    const last = items.filter((item) => item.role !== "user").at(-1);
    // Before any output, the Turn's stage tells where the time goes.
    const typical =
      !last && turn?.expected_s && turn.stage !== "retrying"
        ? `\n${grey(typicalWait.replace("{{value0}}", turn.expected_s))}`
        : "";
    const current = !last
      ? waitingHeadline(turn)
      : last.type === "message"
        ? "正在撰写回复"
        : last.type === "reasoning"
          ? "正在思考"
          : last.status === "in_progress"
            ? `正在${step(last)[0]}`
            : "正在思考下一步";
    const done = steps.filter((item) => item.status !== "in_progress").length;
    return statusCard(
      "in_progress",
      `**${current}**${typical}${done ? `\n${grey(`已完成 ${done} 步`)}` : ""}`,
      panels,
    );
  }
  const answer = (messages.at(-1) || "").slice(0, 5000);
  const body =
    status === "completed"
      ? answer || "已完成处理。"
      : status === "cancelled"
        ? answer || "已按你的要求停止。"
        : [
            answer || "这次没有完成。",
            error ? `<font color='red'>${error}</font>` : "",
            grey("直接回复即可重试。"),
          ]
            .filter(Boolean)
            .join("\n\n");
  return statusCard(status, body, panels);
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
    readContext: (row) => readContext(row),
    async connect() {
      const result = await client.request({
        method: "GET",
        url: "/open-apis/bot/v3/info",
      });
      const bot = result.bot || result.data?.bot;
      if (result.code !== 0 || !bot?.open_id)
        throw new Error("Cannot resolve Feishu bot identity");
      readContext = feishuContextReader(client, {
        openId: bot.open_id,
        name: bot.app_name || "Parsar",
      });
      const dispatcher = new EventDispatcher({ logger }).register({
        "im.message.receive_v1": async (event) => {
          if (
            event.sender?.sender_type !== "user" ||
            // The bot serves its own tenant; external contacts are ignored.
            (event.tenant_key &&
              event.sender.tenant_key &&
              event.sender.tenant_key !== event.tenant_key) ||
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
  const sessions = product.sessions;
  // The Turn ledger, bound to this worker's Core client like every other call here.
  const ledger = turnsAPI(store, core);
  const allowed = (appId, chatType = "group") => {
    const config = store.get("feishu_bots", configId);
    return Boolean(
      config?.enabled &&
        config.app_id === appId &&
        (chatType === "p2p" ? config.direct : config.groups),
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
      !allowed(appId, message.chatType) ||
      !["group", "p2p"].includes(message.chatType) ||
      (message.chatType === "group" &&
        !message.mentionedBot &&
        // A topic reply that mentions only other people is addressed to them.
        (message.mentions?.length || !threadFor(appId, message))) ||
      !message.senderId ||
      message.mentionAll
    )
      return;
    if (!message.content?.trim() || message.content.length > 40000) return;
    const id = stableId(`${appId}:${message.messageId}`);
    if (store.get("feishu_inbound", id)) return;
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
      !store.get("sessions", thread.session_id) &&
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
          session_id: randomUUID(),
          agent_id: store.get("feishu_bots", configId).agent_id,
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
  const queueReply = (
    id,
    thread,
    payload,
    notify = false,
    replyTo = thread.root_id,
  ) => {
    if (!store.get("feishu_outbound", id))
      store.put("feishu_outbound", {
        id,
        app_id: thread.app_id,
        chat_id: thread.chat_id,
        chat_type: thread.chat_type,
        thread_id: thread.id,
        reply_to: replyTo,
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
    const thread = store.get("feishu_threads", row.thread_id);
    if (!thread) return reject(row, "话题关联不可用，请在 Web 中检查。");
    let run = store.get("sessions", thread.session_id);
    // Resolve an uncertain initial creation before cancelling its actual Session.
    if (run && !run.core_session_id && !run.deleted_at)
      run = await sessions(run.id, {});
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
      (await channel.readContext(row));
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
    if (["/new", "/reset", "/help"].includes(row.text)) {
      queueReply(
        stableId(`${row.id}:command`),
        thread,
        row.text === "/help"
          ? "直接发送消息即可对话。/new 开始新对话（群聊请另开话题并 @机器人）；/cancel 停止执行；/answer <call_id> <回答> 回复 Agent 的提问。"
          : row.chat_type === "p2p"
            ? "新对话已就绪，请直接发送消息。"
            : "群聊请另开话题并 @机器人，开始新对话。当前话题保持不变。",
      );
      store.put("feishu_inbound", { ...row, state: "delivered" });
      return;
    }
    const media = await mediaFor(row);
    row = store.get("feishu_inbound", row.id);
    const rotateP2PThread = () => {
      thread = {
        ...thread,
        root_id: row.message_id,
        agent_id: store.get("feishu_bots", configId).agent_id,
        session_id: randomUUID(),
        session_ids: [
          ...new Set(
            [...(thread.session_ids || []), thread.session_id].filter(Boolean),
          ),
        ],
        first_message_id: row.id,
        turn_cursor: undefined,
        item_cursor: undefined,
        artifact_cursor: undefined,
        settled_at: undefined,
        active_outbound_id: undefined,
        progress_text: undefined,
      };
      store.put("feishu_threads", thread);
    };
    if (
      thread.first_message_id !== row.id &&
      row.chat_type === "p2p" &&
      !row.text.startsWith("/") &&
      store.get("sessions", thread.session_id)?.deleted_at
    )
      rotateP2PThread();
    const progress = (text) => {
      const progressId = stableId(`${row.id}:accepted`);
      thread = {
        ...thread,
        active_outbound_id: progressId,
        progress_text: text,
      };
      store.put("feishu_threads", thread);
      queueReply(
        progressId,
        thread,
        text,
        false,
        row.chat_type === "p2p" ? row.message_id : thread.root_id,
      );
    };
    const thinking = progressCard(
      thread.first_message_id === row.id ? "正在准备工作环境" : "正在思考",
      media.notice,
    );
    if (thread.first_message_id === row.id) {
      if (row.text.startsWith("/"))
        return reject(row, "请先发送消息开始对话，再使用命令。");
      await sessions(
        thread.session_id,
        {
          name: media.title.split("\n")[0].slice(0, 80),
          agent_id: thread.agent_id,
          input: media.text,
          images: media.images,
          files: media.files,
        },
        { source: "feishu", feishu_chat_type: row.chat_type },
      );
      progress(thinking);
    } else {
      let run = store.get("sessions", thread.session_id);
      if (!run || run.deleted_at)
        return reject(
          row,
          row.chat_type === "p2p"
            ? "此对话已不可用，请发送 /new 开始新对话。"
            : "此对话已不可用，请另开话题并 @机器人。",
        );
      if (!run.core_session_id) run = await sessions(run.id, {});
      if (
        row.text.startsWith("/") &&
        row.text !== "/cancel" &&
        !row.text.startsWith("/answer ")
      )
        return reject(
          row,
          "支持的命令：/new、/cancel、/answer、/help。其他消息会交给 Agent。",
        );
      try {
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
        // Messages go through the Turn ledger; answers and cancellations go to Core directly.
        if (event.type === "agent.session.input.message")
          await ledger.deliver(run, event.input, { id: row.id, key: row.id, source: "feishu" });
        else
          await core(
            `/v1/agents/sessions/${run.core_session_id}/events`,
            "POST",
            { events: [event] },
            row.id,
          );
      } catch (error) {
        if (
          row.chat_type === "p2p" &&
          row.text !== "/cancel" &&
          (error?.code === "environment_unavailable" ||
            /environment is no longer available/i.test(error?.message || ""))
        ) {
          rotateP2PThread();
          row = {
            ...row,
            state: "queued",
            event: undefined,
            files_uploaded: undefined,
          };
          store.put("feishu_inbound", row);
          return inbound(row);
        }
        throw error;
      }
      if (row.text === "/cancel")
        queueReply(
          stableId(`${row.id}:cancel`),
          thread,
          "已请求取消，等待 Agent 确认停止。",
        );
      else progress(thinking);
    }
    store.put("feishu_inbound", {
      ...row,
      state: "delivered",
      media: undefined,
      context: undefined,
      error: undefined,
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
        questionCard(
          typeof args?.question === "string" ? args.question : "请补充信息",
          Array.isArray(args?.options)
            ? args.options.filter((value) => typeof value === "string")
            : [],
        ),
        true,
      );
    }
    const recovering = ledger.recovering(run.id);
    // A Turn the monitor cancelled or retried is never reported; a failed or
    // stopped Turn waits until the monitor has decided not to retry it.
    const unseen = turns.filter(
      (turn) =>
        ["completed", "failed", "cancelled"].includes(turn.status) &&
        !ledger.superseded(run.id, turn.id) &&
        (turn.status === "completed" || !recovering) &&
        !store.get("feishu_outbound", stableId(`${thread.id}:${turn.id}`)),
    );
    if (session.status === "failed" && !turns.length && !thread.turn_cursor && !recovering)
      queueReply(
        stableId(`${thread.id}:initial-failed:${sid}`),
        thread,
        `执行失败\n\n${ledger.summary(run.id)?.failure || "执行环境准备失败，请重新发送消息重试。"}`,
      );
    const progressId =
      thread.active_outbound_id ||
      stableId(`${thread.first_message_id}:accepted`);
    let progress = store.get("feishu_outbound", progressId);
    if (
      progress?.sent_message_id &&
      session.status === "requires_action" &&
      !unseen.length
    ) {
      const text = statusCard("requires_action", "请回复上方的提问。");
      if (thread.progress_text !== text) {
        try {
          await channel.edit(progress.sent_message_id, text, thread);
          thread = { ...thread, progress_text: text };
          store.put("feishu_threads", thread);
          store.put("feishu_outbound", { ...progress, payload: text });
        } catch {
          // A deleted or uneditable progress card must not block result delivery.
        }
      }
    }
    if (progress && (session.status === "in_progress" || recovering) && !unseen.length) {
      const items = (await history(sid, "items", thread.item_cursor)).filter(
        (item) => !ledger.superseded(run.id, item.turn_id),
      );
      const text = formatTurnCard("in_progress", items, undefined, ledger.summary(run.id));
      if (progress.sent_message_id) {
        if (thread.progress_text !== text) {
          try {
            await channel.edit(progress.sent_message_id, text, thread);
            thread = { ...thread, progress_text: text };
            store.put("feishu_threads", thread);
            store.put("feishu_outbound", { ...progress, payload: text });
          } catch {
            // A deleted or uneditable progress card must not block result delivery.
          }
        }
      } else if (progress.state === "queued" && progress.payload !== text) {
        progress = { ...progress, payload: text };
        store.put("feishu_outbound", progress);
        thread = { ...thread, progress_text: text };
        store.put("feishu_threads", thread);
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
      const turnItems = items.filter((item) => item.turn_id === turn.id);
      const text = formatTurnCard(
        turn.status,
        turnItems,
        turn.status === "failed"
          ? ledger.summary(run.id)?.failure || explainFailure(turn.error)
          : turn.error?.message,
      );
      const receiptId = stableId(`${thread.id}:${turn.id}`);
      if (progress?.sent_message_id) {
        try {
          await channel.edit(progress.sent_message_id, text, thread);
          store.put("feishu_outbound", { ...progress, payload: text });
          store.put("feishu_outbound", {
            id: receiptId,
            app_id: thread.app_id,
            chat_id: thread.chat_id,
            chat_type: thread.chat_type,
            thread_id: thread.id,
            reply_to: progress.reply_to || thread.root_id,
            payload: text,
            notify: false,
            state: "delivered",
            sent_message_id: progress.sent_message_id,
            attempts: 1,
            created_at: stamp(),
            next_at: 0,
          });
          thread = {
            ...thread,
            progress_text: text,
            active_outbound_id: undefined,
          };
          progress = undefined;
          continue;
        } catch {
          // A deleted or uneditable progress card falls back to sending a result card.
        }
      } else if (progress && progress.state === "queued") {
        store.put("feishu_outbound", { ...progress, payload: text });
        store.put("feishu_outbound", {
          id: receiptId,
          app_id: thread.app_id,
          chat_id: thread.chat_id,
          chat_type: thread.chat_type,
          thread_id: thread.id,
          reply_to: progress.reply_to || thread.root_id,
          payload: text,
          notify: false,
          state: "delivered",
          sent_message_id: progress.id,
          attempts: 1,
          created_at: stamp(),
          next_at: 0,
        });
        thread = {
          ...thread,
          progress_text: text,
          active_outbound_id: undefined,
        };
        progress = undefined;
        continue;
      }
      queueReply(receiptId, thread, text);
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
          `执行失败\n\n消息处理未完成（${error.message || "服务不可用"}），请重新发送消息重试。`,
        );
    }
  };
  const deliver = async (current) => {
    for (const row of store.list("feishu_outbound").reverse()) {
      if (!current()) return;
      if (
        !allowed(row.app_id, row.chat_type) ||
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
        });
      } catch (error) {
        retry("feishu_outbound", row, error);
      }
    }
  };
  const tick = async () => {
    const config = store.get("feishu_bots", configId);
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
      if (!allowed(row.app_id, row.chat_type)) continue;
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
        !allowed(row.app_id, row.chat_type) ||
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
      if (!allowed(thread.app_id, thread.chat_type)) continue;
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
