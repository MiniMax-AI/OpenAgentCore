import { gzipSync } from "node:zlib";
import QRCode from "qrcode";
import { qrImagePattern } from "../shared/media.mjs";
import { AppError, uuid, text } from "./store.mjs";

// App-identity scopes the new app requests on top of the platform's default
// template, so an Agent can work with Feishu data without its users applying
// for each scope later. All of them are ones the organization grants without
// review; scopes that need review are left for when an Agent truly needs them.
// Without im:message.group_msg the bot never receives topic replies that do
// not mention it.
export const botScopes = [
  // Messages and groups
  "im:message", "im:message:readonly", "im:message:send_as_bot", "im:message:send_multi_users", "im:message:update",
  "im:message.group_msg", "im:message.group_msg.include_bot:read", "im:message.group_at_msg:readonly",
  "im:message.group_at_msg.include_bot:readonly", "im:message.p2p_msg:readonly", "im:message.pins:read", "im:message.pins:write_only",
  "im:message.reactions:read", "im:message.reactions:write_only", "im:resource",
  "im:chat", "im:chat:read", "im:chat:readonly", "im:chat:create", "im:chat:update", "im:chat.members:read",
  "im:chat.members:write_only", "im:chat.members:bot_access", "im:chat.announcement:read", "im:chat.announcement:write_only",
  "im:chat.moderation:read", "im:chat.tabs:read", "im:chat.access_event.bot_p2p_chat:read", "search:message",
  // The bot itself, cards
  "application:application:self_manage", "application:bot.basic_info:read", "application:bot.menu:readonly",
  "application:bot.menu:write", "application:app_slash_command:read", "application:app_slash_command:write",
  "cardkit:card:read", "cardkit:card:write", "cardkit:template:read",
  // People
  "contact:contact.base:readonly", "contact:user.base:readonly", "contact:user.id:readonly", "contact:user.employee_id:readonly",
  "contact:user.email:readonly", "contact:department.base:readonly", "tenant:tenant:readonly",
  // Base
  "bitable:app", "bitable:app:readonly", "base:app:read", "base:app:create", "base:app:update", "base:app:copy",
  "base:table:read", "base:table:create", "base:table:update", "base:field:read", "base:field:create", "base:field:update",
  "base:record:read", "base:record:retrieve", "base:record:create", "base:record:update", "base:record:delete",
  "base:view:read", "base:view:write_only", "base:dashboard:read", "base:form:read", "base:workspace:list", "base:workspace:read",
  // Calendar and meetings
  "calendar:calendar", "calendar:calendar:readonly",
  "vc:meeting.search:read", "vc:note:read", "vc:reserve:readonly", "vc:room:readonly",
  "minutes:minutes.artifacts:read", "minutes:minutes.search:read",
  // Documents
  "docx:document", "docx:document:create", "docx:document:readonly", "docx:document:write_only", "docx:document.block:convert",
  "docs:document.content:read", "docs:document.media:download", "docs:document.media:upload", "docs:document:copy",
  "docs:document:export", "docs:document:import", "docs:document.comment:read", "docs:document.comment:create",
  "docs:document.comment:update", "docs:document.comment:delete", "docs:document.comment:write_only",
  "docs:permission.member:create", "docs:permission.member:retrieve", "docs:permission.setting:read",
  "drive:drive.metadata:readonly", "drive:file:download", "drive:file:upload", "drive:drive:version:readonly", "space:folder:create",
  "sheets:spreadsheet", "sheets:spreadsheet:read", "sheets:spreadsheet:readonly", "sheets:spreadsheet:create",
  "sheets:spreadsheet:write_only", "sheets:spreadsheet.meta:read",
  "slides:presentation:create", "slides:presentation:read", "slides:presentation:update", "slides:presentation:write_only",
  "board:whiteboard:node:read", "board:whiteboard:node:create", "board:whiteboard:node:update",
  "mindnote:node:read", "mindnote:node:create",
  "wiki:wiki", "wiki:wiki:readonly", "wiki:space:read", "wiki:space:retrieve", "wiki:node:read", "wiki:node:retrieve",
  "wiki:node:create", "wiki:node:update", "wiki:node:copy",
  // Tasks
  "task:task", "task:task:read", "task:task:readonly", "task:tasklist:read", "task:comment:read", "task:comment:write",
  "task:attachment:read", "task:attachment:write", "task:section:read", "task:custom_field:read",
  // Translation, OCR and speech
  "translation:text", "optical_char_recognition:image", "speech_to_text:speech",
];
// The name the app has in Feishu, read with its own credentials; undefined
// when Feishu cannot say.
export async function feishuAppName(fetchImpl, { app_id, app_secret, brand }) {
  const host = brand === "lark" ? "open.larksuite.com" : "open.feishu.cn";
  const call = async (path, init) =>
    (await fetchImpl(`https://${host}${path}`, { ...init, redirect: "error", signal: AbortSignal.timeout(10000) })).json();
  try {
    const { tenant_access_token: token } = await call("/open-apis/auth/v3/tenant_access_token/internal", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ app_id, app_secret }),
    });
    if (!token) return;
    const info = await call("/open-apis/bot/v3/info", { headers: { Authorization: `Bearer ${token}` } });
    const name = (info.bot || info.data?.bot)?.app_name;
    return typeof name === "string" && name.trim() ? name.trim().slice(0, 80) : undefined;
  } catch {
    return undefined;
  }
}

// The official SDKs' QR `addons` encoding.
const addons = gzipSync(JSON.stringify({ scopes: { tenant: botScopes } })).toString("base64url");

// The SDK registerApp helper runs an in-memory polling loop and cannot resume a
// persisted device code after restart. This adapter implements the same device
// flow with durable polling state; codes and credentials never leave the server.
export function feishuRegistrationAPI(store, fetchImpl = fetch) {
  const active = new Set();
  const publicState = (row) => ({
    id: row.id,
    status: row.status,
    verification_url: row.verification_url,
    qr_url: `/app/feishu/provision/${row.id}/qrcode`,
    expires_at: row.expires_at,
    interval: row.interval,
    error: row.error,
  });
  const call = async (action, fields = {}, brand = "feishu") => {
    let response, value;
    try {
      response = await fetchImpl(
        `https://${brand === "lark" ? "accounts.larksuite.com" : "accounts.feishu.cn"}/oauth/v1/app/registration`,
        {
          method: "POST",
          redirect: "error",
          signal: AbortSignal.timeout(15000),
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({ action, ...fields }),
        },
      );
      const raw = await response.text();
      if (raw.length > 65536) throw new Error();
      value = JSON.parse(raw);
    } catch {
      throw new AppError(502, "无法连接飞书应用注册服务，请重试。");
    }
    if (
      !response.ok &&
      ![
        "authorization_pending",
        "slow_down",
        "access_denied",
        "expired_token",
        "invalid_grant",
      ].includes(value.error)
    )
      throw new AppError(502, "飞书应用注册服务暂不可用，请稍后重试。");
    return value;
  };
  return async (method, path, body) => {
    const qrId = path.match(qrImagePattern)?.[1];
    if (method === "GET" && qrId && uuid.test(qrId)) {
      const row = store.get("feishu_registration", qrId);
      if (!row) throw new AppError(404, "注册会话不存在。");
      if (row.status !== "pending" || row.expires_at <= Date.now())
        throw new AppError(410, "授权二维码已失效，请重新发起注册。");
      return QRCode.toBuffer(row.verification_url, { width: 640, margin: 4 });
    }
    const id = path.match(/^\/app\/feishu\/provision\/([a-f0-9-]{36})$/)?.[1];
    if (!id || !uuid.test(id)) throw new AppError(404, "Not found.");
    if (method !== "POST" || !["begin", "poll"].includes(body.action))
      throw new AppError(400, "注册操作无效。");
    if (active.has(id))
      throw new AppError(409, "应用注册正在处理，请稍后重试。");
    active.add(id);
    try {
      let row = store.get("feishu_registration", id);
      if (body.action === "begin") {
        if (row && (row.status === "success" || row.expires_at > Date.now()))
          return publicState(row);
        const bot = store.get("feishu_bots", id);
        if (bot?.app_id)
          throw new AppError(409, "机器人已有应用，请新建机器人进行扫码注册。");
        const name = body.name ? text(body.name, "机器人名称", 80, true) : "";
        await call("init");
        const value = await call("begin", {
          archetype: "PersonalAgent",
          auth_method: "client_secret",
          request_user_info: "open_id tenant_brand",
        });
        if (!value.device_code || !value.user_code || value.error)
          throw new AppError(502, "飞书未返回有效的注册会话。");
        const verification = new URL(
          value.verification_uri_complete ||
            `https://open.feishu.cn/page/cli?user_code=${encodeURIComponent(value.user_code)}`,
        );
        if (
          verification.protocol !== "https:" ||
          ![
            "open.feishu.cn",
            "accounts.feishu.cn",
            "open.larksuite.com",
            "accounts.larksuite.com",
          ].includes(verification.hostname) ||
          verification.username ||
          verification.password
        )
          throw new AppError(502, "飞书返回的授权地址无效。");
        // The confirmation page after scanning pre-fills the app's scopes, and its name when one was given.
        if (name) verification.searchParams.set("name", name);
        verification.searchParams.set("addons", addons);
        row = {
          id,
          name,
          status: "pending",
          device_code: value.device_code,
          verification_url: verification.href,
          expires_at:
            Date.now() +
            Math.min(Math.max(Number(value.expires_in) || 300, 30), 1800) *
              1000,
          interval: Math.min(Math.max(Number(value.interval) || 5, 1), 60),
          next_at: 0,
        };
        store.put("feishu_registration", row);
        return publicState(row);
      }
      if (!row) throw new AppError(404, "注册会话不存在，请重新扫码。");
      if (row.status !== "pending") return publicState(row);
      if (row.expires_at <= Date.now()) {
        row = {
          ...row,
          status: "error",
          error: "expired_token",
          device_code: undefined,
        };
        store.put("feishu_registration", row);
        return publicState(row);
      }
      if (Date.now() < row.next_at) return publicState(row);
      store.put("feishu_registration", {
        ...row,
        next_at: Date.now() + row.interval * 1000,
      });
      const value = await call(
        "poll",
        { device_code: row.device_code },
        row.brand,
      );
      if (value.user_info?.tenant_brand === "lark" && row.brand !== "lark") {
        // Match the SDK's one-way regional handoff and retain it across restart.
        row = { ...row, brand: "lark", next_at: 0 };
        store.put("feishu_registration", row);
        return publicState(row);
      }
      if (value.client_id && value.client_secret) {
        if (
          !/^cli_[0-9a-fA-F]{16}$/.test(value.client_id) ||
          typeof value.client_secret !== "string" ||
          value.client_secret.length > 300
        )
          throw new AppError(502, "飞书返回的应用凭据无效。");
        const bot = store.get("feishu_bots", id);
        if (
          bot?.app_id ||
          store
            .list("feishu_bots")
            .some((bot) => bot.app_id === value.client_id)
        )
          throw new AppError(409, "应用已绑定，请检查机器人配置。");
        const brand = row.brand === "lark" ? "lark" : "feishu";
        const appName = await feishuAppName(fetchImpl, { app_id: value.client_id, app_secret: value.client_secret, brand });
        store.transaction(() => {
          store.put("feishu_bots", {
            ...bot,
            id,
            name: appName || row.name || value.client_id,
            app_id: value.client_id,
            app_secret: value.client_secret,
            brand: row.brand === "lark" ? "lark" : "feishu",
            enabled: false,
            agent_id: bot?.agent_id || "",
            direct: bot?.direct === true,
            groups: bot?.groups === true,
            revision: (bot?.revision || 0) + 1,
          });
          row = { ...row, status: "success", device_code: undefined };
          store.put("feishu_registration", row);
        });
      } else if (
        ["authorization_pending", "slow_down", undefined, ""].includes(
          value.error,
        )
      ) {
        row = {
          ...row,
          interval:
            value.error === "slow_down"
              ? Math.min(row.interval + 5, 60)
              : row.interval,
        };
        store.put("feishu_registration", {
          ...row,
          next_at: Date.now() + row.interval * 1000,
        });
      } else {
        row = {
          ...row,
          status: "error",
          error: ["access_denied", "expired_token", "invalid_grant"].includes(
            value.error,
          )
            ? value.error
            : "registration_failed",
          device_code: undefined,
        };
        store.put("feishu_registration", row);
      }
      return publicState(row);
    } finally {
      active.delete(id);
    }
  };
}
