import { AppError, uuid, text } from "./store.mjs";

// The SDK registerApp helper runs an in-memory polling loop and cannot resume a
// persisted device code after restart. This adapter implements the same device
// flow with durable polling state; codes and credentials never leave the server.
export function feishuRegistrationAPI(store, fetchImpl = fetch) {
  const active = new Set();
  const publicState = (row) => ({
    id: row.id,
    status: row.status,
    verification_url: row.verification_url,
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
        const name = text(body.name || "Feishu", "机器人名称", 80, true);
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
        store.transaction(() => {
          store.put("feishu_bots", {
            ...bot,
            id,
            name: bot?.name || row.name,
            app_id: value.client_id,
            app_secret: value.client_secret,
            brand: row.brand === "lark" ? "lark" : "feishu",
            enabled: false,
            routes: bot?.routes || [],
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
