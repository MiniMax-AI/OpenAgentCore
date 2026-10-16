import { AppError } from "./store.mjs";
import { connectionScopes as scopesOf } from "./connections.mjs";

// Feishu owns provider wire details and the app-scoped sender identity boundary.
// It does not know CLI commands, environment variable names, or task execution.
export function feishuConnection(store, fetchImpl = fetch) {
  function requirement(value) {
    if (!value) return undefined;
    const bot = store.get("feishu_bots", value.bot_id);
    const scopes = scopesOf(value.scopes);
    if (
      value.service !== "feishu" ||
      !bot?.app_id ||
      !bot.app_secret ||
      !scopes.length ||
      scopes.length > 50 ||
      scopes.some((s) => !/^[a-zA-Z0-9_:.-]+$/.test(s))
    )
      throw new AppError(400, "请选择已配置的飞书应用和有效的授权范围。");
    return {
      service: "feishu",
      bot_id: bot.id,
      app_id: bot.app_id,
      scopes: scopes.join(" "),
    };
  }
  function owner(sessionId) {
    const thread = store
      .list("feishu_threads")
      .find((t) => t.session_id === sessionId);
    return thread
      ? { kind: "feishu", app_id: thread.app_id, subject_id: thread.sender_id }
      : { kind: "local" };
  }
  const app = (requirement) => {
    const bot = store.get("feishu_bots", requirement.bot_id);
    if (!bot?.app_secret || bot.app_id !== requirement.app_id)
      throw new AppError(409, "飞书应用配置已改变，请重新选择连接应用。");
    return bot;
  };
  const request = async (url, options) => {
    let response, value;
    try {
      response = await fetchImpl(url, {
        ...options,
        redirect: "error",
        signal: AbortSignal.timeout(15000),
      });
      const raw = await response.text();
      if (raw.length > 65536) throw new Error();
      value = raw ? JSON.parse(raw) : {};
    } catch {
      throw new AppError(502, "无法连接飞书授权服务，请稍后重试。");
    }
    if (!response.ok && !value.error)
      throw new AppError(502, "飞书授权服务暂不可用。");
    return value;
  };
  const accounts = (bot) =>
    `https://accounts.${bot.brand === "lark" ? "larksuite.com" : "feishu.cn"}`;
  const post = (bot, path, fields, basic = false) =>
    request(accounts(bot) + path, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        ...(basic
          ? {
              Authorization: `Basic ${Buffer.from(`${bot.app_id}:${bot.app_secret}`).toString("base64")}`,
            }
          : {}),
      },
      body: new URLSearchParams(fields),
    });
  const tokenFields = (bot, fields) => ({
    client_id: bot.app_id,
    client_secret: bot.app_secret,
    ...fields,
  });
  return {
    service: "feishu",
    label: "飞书账号",
    defaultScopes: "contact:user.base:readonly",
    requirement,
    owner,
    applications: () =>
      store
        .list("feishu_bots")
        .filter((b) => b.app_id && b.app_secret)
        .map((b) => ({
          service: "feishu",
          bot_id: b.id,
          app_id: b.app_id,
          name: b.name,
        })),
    expectedSubject(requirement, owner) {
      if (owner.kind === "local") return undefined;
      if (owner.kind !== "feishu" || owner.app_id !== requirement.app_id)
        throw new AppError(400, "飞书入口与 Agent 的连接应用必须一致。");
      return owner.subject_id;
    },
    async authorize(requirement) {
      const bot = app(requirement);
      const value = await post(
        bot,
        "/oauth/v1/device_authorization",
        {
          client_id: bot.app_id,
          scope: [
            ...new Set([...scopesOf(requirement.scopes), "offline_access"]),
          ].join(" "),
        },
        true,
      );
      if (value.error || !value.device_code || !value.verification_uri)
        throw new AppError(
          502,
          "无法发起飞书授权，请检查应用的用户权限是否已开通。",
        );
      const url = new URL(
        value.verification_uri_complete || value.verification_uri,
      );
      if (
        url.protocol !== "https:" ||
        url.username ||
        url.password ||
        ![
          "accounts.feishu.cn",
          "open.feishu.cn",
          "accounts.larksuite.com",
          "open.larksuite.com",
        ].includes(url.hostname)
      )
        throw new AppError(502, "飞书返回了无效授权地址。");
      return { ...value, verification_url: url.href };
    },
    exchange(row) {
      const bot = app(row.requirement);
      return post(
        bot,
        "/oauth/v3/token",
        tokenFields(bot, {
          grant_type: "urn:ietf:params:oauth:grant-type:device_code",
          device_code: row.device_code,
        }),
      );
    },
    refresh(row) {
      const bot = app(row);
      return request(accounts(bot) + "/oauth/v3/token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(
          tokenFields(bot, {
            grant_type: "refresh_token",
            refresh_token: row.refresh_token,
          }),
        ),
      });
    },
    async identity(requirement, tokens) {
      const bot = app(requirement);
      const info = await request(
        `https://open.${bot.brand === "lark" ? "larksuite.com" : "feishu.cn"}/open-apis/authen/v1/user_info`,
        { headers: { Authorization: `Bearer ${tokens.access_token}` } },
      );
      if (info.code !== 0 || !info.data?.open_id)
        throw new AppError(502, "无法确认授权账号身份，请重新连接。");
      return {
        subject_id: info.data.open_id,
        name: info.data.name || info.data.en_name || info.data.open_id,
      };
    },
    async revoke(row) {
      const bot = app(row);
      for (const field of ["refresh_token", "access_token"]) {
        if (!row[field]) continue;
        const value = await post(
          bot,
          "/oauth/v1/revoke",
          tokenFields(bot, { token: row[field], token_type_hint: field }),
        );
        if (value.error || (value.code && value.code !== 0))
          throw new AppError(502, "撤销飞书授权暂未确认，将自动重试。");
      }
    },
    values(requirement, connection) {
      const bot = app(requirement);
      return {
        access_token: connection.access_token,
        client_id: bot.app_id,
        region: bot.brand === "lark" ? "lark" : "feishu",
      };
    },
  };
}
