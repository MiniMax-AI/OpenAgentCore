import { artifactContentPath, imageMime, maxMediaBytes, qrImages, qrImagePattern } from "../shared/media.mjs";
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
const sendExpired = (row) => row.first_attempt_at && stamp() - row.first_attempt_at >= 55 * 60000;
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
        sendExpired(row)
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
        .map(({ text, payload, event, resources, media, uploaded_media, context, ...row }) => {
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
    const app_id = text(body.app_id, "App ID", 100).trim();
    // The name comes from Feishu once the bot connects; until then it shows the App ID.
    const name = app_id === old.app_id && old.name ? old.name : app_id || "Feishu";
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

// Budget serialized UTF-8 bytes, including JSON escaping, for Feishu cards.
function excerpt(value, limit) {
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2) || "";
  if (Buffer.byteLength(JSON.stringify(text)) <= limit) return text;
  const prefix = [];
  let size = 0;
  for (const char of text) {
    size += Buffer.byteLength(JSON.stringify(char)) - 2;
    if (size > limit - 60) break;
    prefix.push(char);
  }
  return prefix.join("") + "\n…（内容较长，已截取）";
}

// A `block` panel is a tinted, rounded area that holds folded history.
function panel({ title, content, elements, block = false }) {
  return {
    tag: "collapsible_panel",
    expanded: false,
    vertical_spacing: "4px",
    padding: block ? "0px 12px 8px 12px" : "0px 0px 4px 0px",
    ...(block && { background_color: "grey-50", border: { color: "grey-50", corner_radius: "8px" } }),
    header: {
      title: { tag: "markdown", content: title },
      width: "fill",
      // The header ignores the panel padding, so a block indents its own title.
      padding: block ? "8px 12px 8px 12px" : "2px 0px 2px 0px",
      icon: { tag: "standard_icon", token: "down-small-ccm_outlined", size: "16px 16px" },
      icon_position: "right",
      icon_expanded_angle: -180,
    },
    elements: elements || [{ tag: "markdown", content }],
  };
}

// Feishu rejects cards above about 30 KB of JSON.
const cardBudget = 28000;
const bytes = (value) => Buffer.byteLength(JSON.stringify(value));

// The colored header carries the status and, while running, what is
// happening now on the same line, so the body starts with the content.
function statusCard(status, body, elements = [], subtitle = "") {
  const [title, template] = cardTones[status] || cardTones.failed;
  // A card needs a body; with nothing else to show, what is happening is the body.
  if (!body && !elements.length && subtitle) [body, subtitle] = [`**${subtitle}**`, ""];
  const heading = subtitle ? `${title} · ${subtitle}` : title;
  return JSON.stringify({
    schema: "2.0",
    config: { update_multi: true, summary: { content: heading } },
    header: { title: { tag: "plain_text", content: heading }, template },
    body: {
      padding: "12px 16px 12px 16px",
      vertical_spacing: "6px",
      elements: [...(body ? [{ tag: "markdown", content: body }] : []), ...elements],
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
  return statusCard("in_progress", notice ? grey(notice) : "", [], text);
}

function questionText(action, includeOptions = true) {
  let args = action.arguments;
  try { if (typeof args === "string") args = JSON.parse(args); } catch { args = {}; }
  return [
    typeof args?.question === "string" ? args.question : "请补充信息",
    includeOptions && Array.isArray(args?.options) ? args.options.filter((x) => typeof x === "string").map((x) => `- ${x}`).join("\n") : "",
  ].filter(Boolean).join("\n\n");
}

function step(item) {
  if (item.type === "command_execution" || typeof item.command === "string") {
    const command = String(item.command || "").replace(/\s+/g, " ").trim()
      .replace(/^\/bin\/(?:ba|z)?sh -lc (['"])([\s\S]*)\1$/, "$2");
    return ["运行", command];
  }
  if (item.type === "mcp_call") return ["调用工具", [item.server_label, item.name].filter(Boolean).join(" · ")];
  if (item.type === "function_call") return ["调用工具", item.name || ""];
  if (item.type === "web_search_call") return ["搜索网页", item.action?.query || item.action?.url || ""];
  if (item.type === "file_change") return ["修改文件", ""];
  return ["执行步骤", item.name || item.type || ""];
}

// Wide (CJK and full-width) characters take two columns in a card; general
// punctuation such as the ellipsis is narrow.
const wide = (char) => {
  const code = char.codePointAt(0);
  return code >= 0x1100 && !(code >= 0x2000 && code <= 0x206f);
};
const columns = (text) => [...text].reduce((sum, char) => sum + (wide(char) ? 2 : 1), 0);
const clip = (value, limit) => {
  const text = String(value ?? "").replace(/\s+/g, " ").trim();
  if (columns(text) <= limit) return text;
  let kept = "";
  for (const char of text) {
    if (columns(kept + char) > limit - 1) break;
    kept += char;
  }
  return `${kept}…`;
};
// Markdown and Feishu tags stay literal inside a one-line title.
const literal = (value) => value.replace(/[<>]/g, "").replaceAll("`", "'");
// Thinking and tool rows stay on one line: the detail gets what the label
// and state leave of the row.
const rowColumns = 52;
const oneLine = (label, detail, state = "") => {
  const room = rowColumns - columns(label) - columns(state.replace(/<[^>]*>/g, "")) - 2;
  return [grey(label), detail ? literal(clip(detail, Math.max(8, room))) : "", state].filter(Boolean).join(" ");
};

const toolState = (item) =>
  item.status === "failed"
    ? "<font color='red'>失败</font>"
    : item.status === "in_progress"
      ? grey("进行中")
      : item.status === "incomplete"
        ? grey("未完成")
        : typeof item.duration_ms === "number"
          ? grey(`${(item.duration_ms / 1000).toFixed(1)}s`)
          : "";

// One tool call: the command is the title, its result folds below it.
function toolRow(item) {
  const [label, detail] = step(item);
  const title = oneLine(label, detail, toolState(item));
  const fields = [
    // A shortened title keeps the whole command one click away.
    ["命令", item.command && title.includes("…") ? detail : undefined],
    ["参数", item.command ? undefined : item.arguments ?? item.action],
    ["输出", item.output], ["错误", item.error], ["退出码", item.exit_code],
  ].filter(([, value]) => value !== undefined && value !== null && value !== "");
  if (!fields.length) return { tag: "markdown", content: title };
  // Plain text keeps tool output literal, including code fences and mention markup.
  return panel({
    title,
    elements: [{ tag: "div", text: { tag: "plain_text", content: excerpt(fields.map(([name, value]) => `${name}\n${excerpt(value, 1200)}`).join("\n\n"), 1600) } }],
  });
}

// Consecutive tool calls fold into one row; each call folds again inside it.
function toolsRow(items) {
  const failed = items.filter((item) => item.status === "failed").length;
  const state = items.some((item) => item.status === "in_progress")
    ? grey("进行中")
    : failed
      ? `<font color='red'>失败 ${failed} 次</font>`
      : items.every((item) => typeof item.duration_ms === "number")
        ? grey(`${(items.reduce((sum, item) => sum + item.duration_ms, 0) / 1000).toFixed(1)}s`)
        : "";
  const [label, detail] = step(items[0]);
  const title = items.length === 1
    ? oneLine(label, detail, state)
    : [grey(items.every((item) => step(item)[0] === "运行") ? `运行 ${items.length} 条命令` : `调用 ${items.length} 次工具`), state].filter(Boolean).join(" ");
  return panel({ title, elements: items.map(toolRow) });
}

// Only commentary stays open; thinking and tool calls fold behind one line.
function timelineRow(entry) {
  if (entry.type === "say") return { tag: "markdown", content: excerpt(entry.text, 800) };
  if (entry.type === "think")
    return panel({ title: oneLine("思考", entry.text.split("\n").find((line) => line.trim()) || ""), content: excerpt(entry.text, 1500) });
  return toolsRow(entry.items);
}

// The oldest rows leave the card first when it would grow past Feishu's
// limit; a single row that is still too large sheds its oldest calls.
function fitRows(rows, budget, unit = "步") {
  const kept = [...rows];
  let dropped = 0;
  while (kept.length > 1 && bytes(kept) > budget) {
    kept.shift();
    dropped++;
  }
  const [only] = kept;
  if (kept.length === 1 && bytes(kept) > budget && only.elements?.length > 1)
    kept[0] = { ...only, elements: fitRows(only.elements, budget - (bytes(only) - bytes(only.elements)), "次调用") };
  return dropped ? [{ tag: "markdown", content: grey(`更早的 ${dropped} ${unit}见网页对话`) }, ...kept] : kept;
}

// A tool-result answer resumes the same Core Turn. Display only the exchange
// after that answer, not every earlier question and command in the Turn.
function exchangeItems(items) {
  const questions = new Set(items.filter((x) => x.type === "function_call" && x.name === "ask_user").map((x) => x.call_id));
  const boundary = items.findLastIndex((x) => x.type === "message" && x.role === "user" || x.type === "function_call_output" && questions.has(x.call_id));
  return items.slice(boundary + 1);
}

const recentRows = 4;

export function formatTurnCard(status, items = [], error, turn, questions = []) {
  items = exchangeItems(items);
  const text = (item) => (item.content || []).filter((p) => p.type === "output_text").map((p) => (p.text || "").trim()).filter(Boolean).join("\n");
  const messages = items.filter((x) => x.type === "message" && x.role === "assistant");
  const latest = messages.at(-1);
  // Feishu reads any Markdown image as an image key and rejects the whole
  // card, so images become text; the files themselves are sent as messages.
  const readable = (value) => value
    .replace(/!\[[^\]\n]*\]\((\/app\/feishu\/provision\/[a-f0-9-]{36}\/qrcode)\)/g, "（授权二维码见图片消息）")
    .replace(/!?\[([^\]]*)\]\((?:\/workspace\/|\.\/)?outputs\/[^)]*\)/g, "$1（见附件）")
    .replace(/!\[([^\]\n]*)\]\([^)\n]*\)/g, (_, alt) => alt ? `（图片：${alt}）` : "（图片）");
  const authorization = messages.filter((item) => qrImages(text(item)).length);
  const authorizationText = authorization.map((item) => readable(text(item))).join("\n\n");
  const answer = latest?.phase === "commentary" || authorization.includes(latest) ? "" : latest ? readable(text(latest)) : "";
  const outputs = new Map(items.filter((x) => x.type === "function_call_output").map((x) => [x.call_id, x]));
  // Thinking, commentary and tool calls in the order they happened.
  const entries = items.flatMap((x) => {
    if (x.type === "reasoning") {
      const thought = readable((x.summary || x.content || []).map((p) => typeof p === "string" ? p : p?.text || "").filter(Boolean).join("\n\n").trim());
      return thought ? [{ type: "think", text: thought }] : [];
    }
    if (x.type === "message") {
      const said = x.role === "assistant" && !authorization.includes(x) && (status === "in_progress" || x !== latest || x.phase === "commentary") ? readable(text(x)) : "";
      return said ? [{ type: "say", text: said }] : [];
    }
    if (["agent_message", "function_call_output"].includes(x.type) || (x.type === "function_call" && x.name === "ask_user")) return [];
    const output = x.type === "function_call" ? outputs.get(x.call_id) : undefined;
    return [{ type: "tool", item: output ? { ...x, status: output.status, output: output.output, error: output.error } : x }];
  });
  const grouped = entries.reduce((rows, entry) => {
    const last = rows.at(-1);
    if (entry.type === "tool" && last?.type === "tools") last.items.push(entry.item);
    else rows.push(entry.type === "tool" ? { type: "tools", items: [entry.item] } : entry);
    return rows;
  }, []);
  const rows = grouped.map(timelineRow);
  const tools = entries.filter((entry) => entry.type === "tool").map((entry) => entry.item);
  if (status === "in_progress") {
    const last = items.filter((x) => x.role !== "user" && x.type !== "function_call_output").at(-1);
    const current = !last ? waitingHeadline(turn) : last.type === "message" ? "正在整理回复" : last.type === "reasoning" ? "正在思考" : last.status === "in_progress" ? `正在${step(last)[0]}` : "正在整理结果";
    const typical = !last && turn?.expected_s && turn.stage !== "retrying" ? typicalWait.replace("{{value0}}", turn.expected_s) : "";
    const progress = rows.length ? `${rows.length} 步` : typical;
    const subtitle = [current, progress].filter(Boolean).join(" · ");
    const body = authorizationText ? excerpt(authorizationText, 8000) : "";
    const earlier = rows.slice(0, -recentRows), recent = rows.slice(-recentRows);
    const budget = cardBudget - bytes(body);
    const shown = fitRows(recent, budget);
    // Earlier steps sit in a tinted block above a rule that separates the latest ones.
    const fold = earlier.length
      ? [panel({ title: grey(`更早的 ${earlier.length} 步`), elements: fitRows(earlier, budget - bytes(shown)), block: true }), { tag: "hr" }]
      : [];
    return statusCard(status, body, [...fold, ...shown], subtitle);
  }
  const context = questions.some((action) => questionText(action, false).trim() === answer.trim()) ? "" : answer;
  // Put the decision first; keep its explanation available without filling the chat.
  // Authorization instructions stay visible alongside the separately sent QR.
  const foldContext = status === "requires_action" && questions.length && context;
  const body = excerpt(status === "requires_action"
    ? [...questions.map((action) => questionText(action)), authorizationText, foldContext ? "" : context, grey("直接回复这条消息即可继续。")].filter(Boolean).join("\n\n")
    : status === "completed" ? [authorizationText, answer].filter(Boolean).join("\n\n") || "已完成处理。"
    : status === "cancelled" ? [authorizationText, answer].filter(Boolean).join("\n\n") || "已按你的要求停止。"
    : [authorizationText, answer || "这次没有完成。", error ? `<font color='red'>${error}</font>` : "", grey("直接回复即可重试。")].filter(Boolean).join("\n\n"), status === "requires_action" ? 8000 : 12000);
  const elements = [];
  if (foldContext) elements.push(panel({ title: grey("补充说明"), content: excerpt(context, 4000) }));
  // A failure shows the step that failed right under its cause.
  const failedTool = status === "failed" ? tools.findLast((item) => item.status === "failed") : undefined;
  if (failedTool) elements.push(toolRow(failedTool));
  if (rows.length) {
    const budget = cardBudget - bytes(body) - bytes(elements);
    elements.push(panel({ title: grey(`处理过程 · ${rows.length} 步`), elements: fitRows(rows, budget), block: true }));
  }
  return statusCard(status, body, elements.length ? [{ tag: "hr" }, ...elements] : []);
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
      // The name people see in Feishu.
      this.name = bot.app_name;
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
    async upload(bytes, name) {
      const image = !!imageMime(bytes);
      const key = image ? "image_key" : "file_key";
      const failure = (status, code) => {
        // Feishu common error codes: generic-error-code; 99991400 is rate limiting.
        const temporary = status >= 500 || [408, 409, 425, 429].includes(status) || [1500, 1503, 1551, 99991400].includes(code);
        const definite = !temporary && (status >= 400 || (Number.isInteger(code) && code !== 0));
        return new AppError(definite ? 422 : 503, definite
          ? `飞书拒绝上传附件${Number.isInteger(code) ? `（错误码 ${code}）` : ""}，请检查机器人上传权限、文件格式和大小后重试。`
          : "飞书附件上传暂未确认，将重试。");
      };
      let result;
      try {
        // The generated upload methods discard error codes. Keep the SDK's full envelope.
        result = await client.request({
          method: "POST", url: image ? "/open-apis/im/v1/images" : "/open-apis/im/v1/files",
          headers: { "Content-Type": "multipart/form-data" },
          data: image ? { image_type: "message", image: bytes } : { file_type: "stream", file_name: name, file: bytes },
        });
      } catch (error) { throw failure(error.response?.status, error.response?.data?.code); }
      if (result?.code !== 0 || typeof result.data?.[key] !== "string" || !result.data[key])
        throw failure(200, result?.code);
      return { msg_type: image ? "image" : "file", content: JSON.stringify({ [key]: result.data[key] }) };
    },
    async send(row) {
      const text =
        row.plain_text && /^ou_[a-zA-Z0-9_-]+$/.test(row.sender_id || "")
          ? `<at user_id="${row.sender_id}">user</at> ${row.payload}`
          : row.payload;
      const data = {
        msg_type: row.plain_text ? "text" : "interactive",
        content: JSON.stringify(row.plain_text ? { text } : feishuCard(text)),
        ...row.uploaded_media,
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
      const result = await client.im.message.patch({
        path: { message_id: id },
        data: { content: JSON.stringify(feishuCard(text)) },
      }).catch((error) => {
        if (error.response?.data?.code) return error.response.data;
        throw error;
      });
      // Documented PATCH errors: recalled, outside the edit window, deleted.
      if ([230011, 230031, 230110].includes(result.code))
        throw new AppError(410, "飞书卡片已不可更新。", "feishu_card_unavailable");
      confirmed(result);
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
      message.content.trim() !== "/clear"
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
  const queueMedia = (thread, items, artifacts = []) => {
    const sources = artifacts.map((file) => ({
      id: `artifact:${file.id}`,
      path: artifactContentPath(file.session_id, file.id),
      name: file.path.split("/").at(-1) || "attachment",
    }));
    for (const item of items.filter((item) => item.type === "message" && item.role === "assistant")) {
      const text = (item.content || []).filter((part) => part.type === "output_text").map((part) => part.text).join("\n");
      for (const path of qrImages(text))
        sources.push({ id: `${item.turn_id}:${path}`, path, name: "feishu-authorization.png" });
    }
    for (const source of sources) {
      const id = stableId(`${thread.id}:media:${source.id}`);
      if (store.get("feishu_outbound", id)) continue;
      store.transaction(() => {
        queueReply(id, thread, "");
        store.put("feishu_outbound", { ...store.get("feishu_outbound", id), media: source });
      });
    }
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
    // A new Session for this chat or topic; the next message starts it.
    const rotateThread = () => {
      thread = {
        ...thread,
        root_id: row.chat_type === "p2p" ? row.message_id : thread.root_id,
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
        display_after: undefined,
        answered_call_ids: [],
        awaiting_user_input: false,
        observed_through: undefined,
      };
      store.put("feishu_threads", thread);
    };
    if (["/clear", "/help"].includes(row.text)) {
      if (row.text === "/clear") {
        // Work in the cleared conversation would continue unseen; stop it.
        const previous = row.chat_type === "p2p"
          ? store.list("feishu_threads").filter((t) => t.id !== thread.id && t.app_id === thread.app_id && t.chat_id === thread.chat_id && t.sender_id === thread.sender_id && t.chat_type === "p2p").at(-1)
          : thread;
        const run = previous && store.get("sessions", previous.session_id);
        if (run?.core_session_id)
          await core(`/v1/agents/sessions/${run.core_session_id}/events`, "POST", { events: [{ type: "agent.session.input.cancel" }] }, `${row.id}:clear`).catch(() => {});
        // A private chat already got a fresh thread on receipt; a topic keeps its root.
        if (row.chat_type !== "p2p" && thread.first_message_id !== row.id) rotateThread();
      }
      queueReply(
        stableId(`${row.id}:command`),
        thread,
        row.text === "/help"
          ? "直接发送消息即可对话。/clear 清空上下文，开始新对话；/stop 停止当前任务；/answer <call_id> <回答> 回复 Agent 的提问。"
          : row.chat_type === "p2p"
            ? "已清空上下文，直接发送消息即可开始新对话。"
            : "已清空这个话题的上下文，接着在话题里发消息即可开始新对话。",
      );
      store.put("feishu_inbound", { ...row, state: "delivered" });
      return;
    }
    const media = await mediaFor(row);
    row = store.get("feishu_inbound", row.id);

    if (
      thread.first_message_id !== row.id &&
      row.chat_type === "p2p" &&
      !row.text.startsWith("/") &&
      store.get("sessions", thread.session_id)?.deleted_at
    )
      rotateThread();
    const progress = (text) => {
      const progressId = stableId(`${row.id}:accepted`);
      thread = {
        ...thread,
        active_outbound_id: progressId,
        progress_text: text,
        display_after: row.display_after,
        answered_call_ids: row.event?.type === "agent.session.input.tool_result"
          ? [...new Set([...(thread.answered_call_ids || []), row.event.call_id])]
          : thread.answered_call_ids,
        awaiting_user_input: row.event?.type === "agent.session.input.message",
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
            ? "此对话已不可用，请发送 /clear 开始新对话。"
            : "此对话已不可用，请另开话题并 @机器人。",
        );
      if (!run.core_session_id) run = await sessions(run.id, {});
      if (
        row.text.startsWith("/") &&
        row.text !== "/stop" &&
        !row.text.startsWith("/answer ")
      )
        return reject(
          row,
          "支持的命令：/clear、/stop、/answer、/help。其他消息会交给 Agent。",
        );
      let steered = false;
      try {
        if (
          media.files.length &&
          !row.files_uploaded &&
          row.text !== "/stop"
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
        if (!row.event && row.text !== "/stop") {
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
          (row.text === "/stop"
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
        // Freeze the display boundary before submitting any follow-up: Core may
        // acknowledge it before its resumed output becomes visible.
        if (!row.event && event.type !== "agent.session.input.cancel") {
          if (event.type === "agent.session.input.message" && thread.awaiting_user_input)
            throw new AppError(409, "等待上一条输入出现在执行记录中。");
          const prior = await history(run.core_session_id, "items", thread.item_cursor);
          if (thread.observed_through && !prior.some((item) => item.id === thread.observed_through))
            throw new AppError(409, "等待执行记录更新后继续。");
          row = { ...row, display_after: prior.at(-1)?.id };
        }
        row = { ...row, state: "submitting", event };
        store.put("feishu_inbound", row);
        // Messages go through the Turn ledger; answers and cancellations go to Core directly.
        if (event.type === "agent.session.input.message")
          steered = (await ledger.deliver(run, event.input, { id: row.id, key: row.id, source: "feishu" })).id !== row.id;
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
          row.text !== "/stop" &&
          (error?.code === "environment_unavailable" ||
            /environment is no longer available/i.test(error?.message || ""))
        ) {
          rotateThread();
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
      if (row.text === "/stop")
        queueReply(
          stableId(`${row.id}:cancel`),
          thread,
          "已请求取消，等待 Agent 确认停止。",
        );
      // A message that steered the running Turn keeps that Turn's card.
      else if (!steered) progress(thinking);
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
    let questions = (session.required_actions || []).filter((action) => action.type === "function_call" && action.name === "ask_user" && !thread.answered_call_ids?.includes(action.call_id));
    const displayed = (items) => {
      if (thread.observed_through && !items.some((item) => item.id === thread.observed_through)) return undefined;
      let boundary = thread.display_after ? items.findIndex((item) => item.id === thread.display_after) : -1;
      if (thread.display_after && boundary < 0) return undefined; // Wait for a snapshot that includes the submitted input's boundary.
      const lastAnswer = thread.answered_call_ids?.at(-1);
      if (lastAnswer) {
        const answered = items.findIndex((item) => item.type === "function_call" && item.call_id === lastAnswer);
        if (answered < 0) return undefined;
        boundary = Math.max(boundary, answered);
      }
      const answeredCalls = new Set(items.filter((item) => item.type === "function_call_output").map((item) => item.call_id));
      questions = questions.filter((action) => !answeredCalls.has(action.call_id));
      const currentItems = items.slice(boundary + 1);
      if (thread.awaiting_user_input) {
        const input = currentItems.findIndex((item) => item.type === "message" && item.role === "user");
        if (input < 0) return undefined;
        return currentItems.slice(input + 1);
      }
      return currentItems;
    };
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
    let progress = thread.active_outbound_id && store.get("feishu_outbound", thread.active_outbound_id);
    // The progress card of the input a Turn answers; an older Turn never takes
    // the card of a newer message.
    const cardFor = (turn) => {
      const entry = ledger.entryFor(run.id, turn.id);
      const own = entry && store.get("feishu_outbound", stableId(`${entry.kind === "first" ? thread.first_message_id : entry.id}:accepted`));
      return own || (turn.id === turns.at(-1)?.id ? progress : undefined);
    };
    const progressReplyTo = progress?.reply_to || thread.root_id;
    const questionBase = progress?.sent_message_id ? progress.id : thread.id;
    if ((progress || session.status === "requires_action") && (["in_progress", "requires_action"].includes(session.status) || recovering) && !unseen.length) {
      const snapshot = await history(sid, "items", thread.item_cursor);
      const currentItems = displayed(snapshot);
      if (!currentItems) return;
      const observedThrough = snapshot.at(-1)?.id;
      if (thread.observed_through !== observedThrough || thread.awaiting_user_input) {
        thread = { ...thread, observed_through: observedThrough, awaiting_user_input: false };
        store.put("feishu_threads", thread);
      }
      const items = currentItems.filter(
        (item) => item.turn_id === turns.at(-1)?.id && !ledger.superseded(run.id, item.turn_id),
      );
      queueMedia(thread, items);
      const text = formatTurnCard(session.status === "requires_action" && questions.length ? "requires_action" : "in_progress", items, undefined, ledger.summary(run.id), questions);
      if (progress?.sent_message_id) {
        if (thread.progress_text !== text) {
          try {
            await channel.edit(progress.sent_message_id, text, thread);
            thread = { ...thread, progress_text: text };
            store.put("feishu_threads", thread);
            store.put("feishu_outbound", { ...progress, payload: text, state: "delivered", error: undefined });
          } catch (error) {
            // Unknown outcomes retry the same card; only confirmed loss replaces it.
            if (questions.length && error.code === "feishu_card_unavailable") progress = undefined;
          }
        }
      } else if (progress?.state === "queued" && progress.payload !== text) {
        progress = { ...progress, payload: text };
        store.put("feishu_outbound", progress);
        thread = { ...thread, progress_text: text };
        store.put("feishu_threads", thread);
      }
      if ((!progress || progress.state === "failed" && !progress.sent_message_id && !sendExpired(progress)) && questions.length) {
        const questionId = stableId(`${questionBase}:question:${questions.map((x) => x.call_id).join(":")}`);
        queueReply(questionId, thread, text, true, progressReplyTo);
        thread = { ...thread, active_outbound_id: questionId, progress_text: text };
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
    const currentItems = displayed(items);
    if (!currentItems) return;
    for (const turn of unseen) {
      const turnItems = items.filter((item) => item.turn_id === turn.id);
      if (turn.status === "completed" && (thread.answered_call_ids || []).some((callId) =>
        turnItems.some((item) => item.type === "function_call" && item.call_id === callId) &&
        !turnItems.some((item) => item.type === "function_call_output" && item.call_id === callId))) return;
      queueMedia(thread, turnItems, artifacts.filter((file) => file.turn_id === turn.id));
      const latest = turn.id === turns.at(-1)?.id;
      const card = cardFor(turn);
      // The display boundary belongs to the newest input; an older Turn shows all of itself.
      const text = formatTurnCard(
        turn.status,
        latest ? currentItems.filter((item) => item.turn_id === turn.id) : turnItems,
        turn.status === "failed"
          ? ledger.summary(run.id)?.failure || explainFailure(turn.error)
          : turn.error?.message,
      );
      const receiptId = stableId(`${thread.id}:${turn.id}`);
      const receipt = (sentMessageId) => store.put("feishu_outbound", {
        id: receiptId,
        app_id: thread.app_id,
        chat_id: thread.chat_id,
        chat_type: thread.chat_type,
        thread_id: thread.id,
        reply_to: card.reply_to || thread.root_id,
        payload: text,
        notify: false,
        state: "delivered",
        sent_message_id: sentMessageId,
        attempts: 1,
        created_at: stamp(),
        next_at: 0,
      });
      // The newest message's card is free again once its Turn has its result.
      const release = () => {
        if (card.id !== progress?.id) return;
        thread = { ...thread, progress_text: text, active_outbound_id: undefined };
        progress = undefined;
      };
      if (card?.sent_message_id) {
        try {
          await channel.edit(card.sent_message_id, text, thread);
          store.put("feishu_outbound", { ...card, payload: text, state: "delivered", error: undefined });
          receipt(card.sent_message_id);
          release();
          continue;
        } catch (error) {
          if (error.code !== "feishu_card_unavailable") throw error;
          // A confirmed uneditable card falls back to sending a result card.
        }
      } else if (card?.state === "queued") {
        store.put("feishu_outbound", { ...card, payload: text });
        receipt(card.id);
        release();
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
        answered_call_ids: (thread.answered_call_ids || []).filter((callId) => !items.some((item) => item.type === "function_call" && item.call_id === callId && ids.has(item.turn_id))),
        display_after: ids.has(items.find((item) => item.id === thread.display_after)?.turn_id) ? undefined : thread.display_after,
        awaiting_user_input: false,
        observed_through: ids.has(items.find((item) => item.id === thread.observed_through)?.turn_id) ? undefined : thread.observed_through,
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
      if (sendExpired(row)) {
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
        let uploaded = row.uploaded_media;
        if (row.media && !uploaded) {
          const bytes = qrImagePattern.test(row.media.path)
            ? await product("GET", row.media.path, {})
            : await core(row.media.path, "GET");
          if (!Buffer.isBuffer(bytes) || !bytes.length || bytes.length > maxMediaBytes)
            throw new AppError(413, "附件为空或超过 5 MiB，请在网页会话中下载。");
          uploaded = await channel.upload(bytes, row.media.name);
          store.put("feishu_outbound", { ...store.get("feishu_outbound", row.id), uploaded_media: uploaded });
        }
        const result = await channel.send({
          ...row,
          uploaded_media: uploaded,
          reply_to: root,
          context_data: thread?.context_data,
          sender_id: thread?.sender_id,
        });
        // A deduplicated retry can return the card sent before its ACK was lost.
        // Apply the current payload before recording delivery as confirmed.
        if (row.first_attempt_at && !row.media) {
          store.put("feishu_outbound", { ...store.get("feishu_outbound", row.id), sent_message_id: result.id });
          if (thread?.active_outbound_id === row.id)
            store.put("feishu_threads", { ...thread, progress_text: undefined });
          await channel.edit(result.id, row.payload, thread);
        }
        if (thread && !thread.root_id)
          store.put("feishu_threads", {
            ...store.get("feishu_threads", thread.id),
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
        // The bot keeps the name it has in Feishu; renaming the app there renames it here.
        const saved = store.get("feishu_bots", configId);
        if (channel.name && saved && saved.name !== channel.name)
          store.put("feishu_bots", { ...saved, name: channel.name });
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
        row.text === "/stop" &&
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
        row.text === "/stop" ||
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
        console.error(`Feishu thread ${thread.id}: ${error.message}`);
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
