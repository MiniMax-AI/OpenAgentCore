import { createHash, randomUUID } from "node:crypto";
import { AppError, text, uuid } from "./store.mjs";

const key = (...parts) =>
  createHash("sha256").update(parts.join("\n")).digest("hex");
export const connectionScopes = (value) =>
  [
    ...new Set(
      text(value || "", "授权范围", 3000)
        .split(/\s+/)
        .filter(Boolean),
    ),
  ].sort();
const ownerKey = (owner) =>
  owner.kind === "local" ? "local" : key(owner.app_id, owner.subject_id);
const safeLogin = (row) =>
  row && {
    id: row.id,
    status: row.status,
    verification_url: row.verification_url,
    user_code: row.user_code,
    expires_at: row.expires_at,
    error: row.error,
  };
const safeConnection = (row) =>
  row && {
    id: row.id,
    app_id: row.app_id,
    subject_id: row.subject_id,
    name: row.name,
    status: row.status,
    revocation_pending: row.revocation_pending,
    scopes: row.scopes,
    expires_at: row.expires_at,
  };

// Durable OAuth device-grant lifecycle. Provider wire behavior is supplied by the adapter.
// Templates own all runtime bindings; the lifecycle never selects tools or CLI variables.
export function userConnections(store, adapter) {
  const locks = new Map();
  const locked = (id, fn) => {
    if (!locks.has(id))
      locks.set(
        id,
        Promise.resolve()
          .then(fn)
          .finally(() => locks.delete(id)),
      );
    return locks.get(id);
  };
  const tokenData = (value, old = {}) => {
    if (
      typeof value.access_token !== "string" ||
      !value.access_token ||
      (value.token_type && value.token_type.toLowerCase() !== "bearer") ||
      !(Number(value.expires_in) > 0)
    )
      throw new AppError(502, "授权服务未返回受支持的用户授权，请重新连接。");
    return {
      access_token: value.access_token,
      refresh_token: value.refresh_token || old.refresh_token,
      expires_at: Date.now() + Number(value.expires_in) * 1000,
      refresh_expires_at: value.refresh_token_expires_in
        ? Date.now() + Number(value.refresh_token_expires_in) * 1000
        : old.refresh_expires_at,
      scopes: typeof value.scope === "string" ? value.scope : old.scopes || "",
    };
  };
  const selected = (requirement, owner) => {
    const subject = adapter.expectedSubject(requirement, owner);
    if (subject)
      return store.get("user_connections", key(requirement.app_id, subject));
    const binding = store.get(
      "connection_bindings",
      key("local", requirement.app_id),
    );
    return binding && store.get("user_connections", binding.connection_id);
  };
  const hasScopes = (row, requirement) =>
    connectionScopes(requirement.scopes).every((s) =>
      connectionScopes(row.scopes).includes(s),
    );
  const refresh = (row) =>
    locked(`refresh:${row.id}`, async () => {
      row = store.get("user_connections", row.id);
      if (row.status !== "connected" || row.expires_at > Date.now() + 120000)
        return row;
      if (
        !row.refresh_token ||
        (row.refresh_expires_at && row.refresh_expires_at <= Date.now())
      ) {
        row = {
          ...row,
          status: "reconnect_required",
          access_token: undefined,
          refresh_token: undefined,
        };
      } else {
        const value = await adapter.refresh(row);
        if (value.error) {
          if (
            !["invalid_grant", "invalid_token", "access_denied"].includes(
              value.error,
            )
          )
            throw new AppError(502, "账号授权续期暂时失败，请稍后重试。");
          row = {
            ...row,
            status: "reconnect_required",
            access_token: undefined,
            refresh_token: undefined,
          };
        } else row = { ...row, ...tokenData(value, row) };
      }
      // Disconnect may run while the provider request is in flight.
      if (store.get("user_connections", row.id)?.generation !== row.generation)
        return store.get("user_connections", row.id);
      store.put("user_connections", row);
      return row;
    });
  const revoke = (row) =>
    locked(`revoke:${row.id}`, async () => {
      row = store.get("user_connections", row.id);
      if (!row?.revocation_pending) return;
      await adapter.revoke(row);
      if (store.get("user_connections", row.id)?.generation === row.generation)
        store.put("user_connections", {
          ...row,
          access_token: undefined,
          refresh_token: undefined,
          revocation_pending: false,
        });
    });
  const begin = (requirement, owner, force = false) =>
    locked(`login:${ownerKey(owner)}:${requirement.app_id}`, async () => {
      if (selected(requirement, owner)?.revocation_pending)
        throw new AppError(409, "正在撤销旧授权，请稍后重新连接。");
      const existing = store
        .list("connection_logins")
        .find(
          (r) =>
            r.owner_key === ownerKey(owner) &&
            r.requirement.app_id === requirement.app_id &&
            r.status === "pending" &&
            r.expires_at > Date.now() &&
            r.requirement.scopes === requirement.scopes,
        );
      if (existing && !force) return safeLogin(existing);
      const prior = selected(requirement, owner);
      const scopes = [
        ...new Set([
          ...connectionScopes(requirement.scopes),
          ...connectionScopes(
            prior?.status === "connected" ? prior.scopes : "",
          ),
        ]),
      ].join(" ");
      const value = await adapter.authorize({ ...requirement, scopes });
      const row = {
        id: randomUUID(),
        owner,
        owner_key: ownerKey(owner),
        requirement,
        status: "pending",
        device_code: value.device_code,
        verification_url: value.verification_url,
        user_code: value.user_code,
        expires_at:
          Date.now() + Math.min(Number(value.expires_in) || 240, 1800) * 1000,
        interval: Math.max(5, Number(value.interval) || 5),
        next_at: Date.now() + 5000,
      };
      store.transaction(() => {
        for (const old of store
          .list("connection_logins")
          .filter(
            (r) =>
              r.owner_key === row.owner_key &&
              r.requirement.app_id === requirement.app_id &&
              r.status === "pending",
          ))
          store.put("connection_logins", {
            ...old,
            status: "cancelled",
            device_code: undefined,
          });
        store.put("connection_logins", row);
      });
      return safeLogin(row);
    });
  const poll = (id) =>
    locked(`poll:${id}`, async () => {
      let row = store.get("connection_logins", id);
      if (!row || row.status !== "pending") return;
      if (row.expires_at <= Date.now()) {
        store.put("connection_logins", {
          ...row,
          status: "expired",
          device_code: undefined,
        });
        return;
      }
      if (row.next_at > Date.now()) return;
      row = { ...row, next_at: Date.now() + row.interval * 1000 };
      store.put("connection_logins", row);
      const value = row.pending_tokens || (await adapter.exchange(row));
      if (store.get("connection_logins", id)?.status !== "pending") return;
      if (value.error) {
        if (value.error === "authorization_pending") return;
        if (value.error === "slow_down") {
          store.put("connection_logins", {
            ...row,
            interval: Math.min(row.interval + 5, 60),
            next_at: Date.now() + (row.interval + 5) * 1000,
          });
          return;
        }
        store.put("connection_logins", {
          ...row,
          status: value.error === "expired_token" ? "expired" : "denied",
          error: "授权未完成，请重新连接。",
          device_code: undefined,
        });
        return;
      }
      if (!row.pending_tokens) {
        row = {
          ...row,
          pending_tokens: tokenData(value),
          device_code: undefined,
        };
        store.put("connection_logins", row);
      }
      const tokens = row.pending_tokens;
      const user = await adapter.identity(row.requirement, tokens);
      const expected = adapter.expectedSubject(row.requirement, row.owner);
      if (expected && user.subject_id !== expected) {
        store.put("connection_logins", {
          ...row,
          status: "denied",
          error: "授权账号与发起任务的账号不一致，请使用自己的账号重新授权。",
          device_code: undefined,
          pending_tokens: undefined,
        });
        return;
      }
      if (store.get("connection_logins", id)?.status !== "pending") return;
      const connection = {
        id: key(row.requirement.app_id, user.subject_id),
        bot_id: row.requirement.bot_id,
        app_id: row.requirement.app_id,
        subject_id: user.subject_id,
        name: user.name,
        ...tokens,
        status: "connected",
        generation: randomUUID(),
      };
      store.transaction(() => {
        store.put("user_connections", connection);
        if (row.owner.kind === "local")
          store.put("connection_bindings", {
            id: key("local", row.requirement.app_id),
            connection_id: connection.id,
          });
        store.put("connection_logins", {
          ...row,
          status: hasScopes(connection, row.requirement)
            ? "connected"
            : "insufficient_scope",
          error: hasScopes(connection, row.requirement)
            ? undefined
            : "已登录，但未授予任务所需权限，请检查应用权限并重新授权。",
          connection_id: connection.id,
          device_code: undefined,
          pending_tokens: undefined,
        });
      });
    });
  return {
    requirement: adapter.requirement,
    owner: adapter.owner,
    begin,
    async prepare(row) {
      const requirement = row.connection_requirement;
      if (!requirement) return row;
      if (row.connection_dispatch_pending) return row;
      if (row.connection) {
        const current = store.get("user_connections", row.connection.id);
        if (
          row.connection.expires_at <= Date.now() ||
          current?.generation !== row.connection.generation ||
          current.status !== "connected"
        )
          throw new AppError(
            428,
            "此会话的账号连接已失效，请开启新会话。",
            "connection_expired",
          );
        return row;
      }
      if (!Object.keys(row.connection_env || {}).length)
        throw new AppError(
          409,
          "此任务未配置连接变量映射，请配置环境模板后开启新会话。",
        );
      const owner = row.connection_owner;
      let connection = selected(requirement, owner);
      if (
        connection?.status === "connected" &&
        hasScopes(connection, requirement)
      )
        connection = await refresh(connection);
      if (
        !connection ||
        connection.status !== "connected" ||
        !hasScopes(connection, requirement) ||
        connection.expires_at <= Date.now() + 60000
      ) {
        const previous =
          row.connection_wait &&
          store.get("connection_logins", row.connection_wait.id);
        // Terminal authorizations need an explicit retry, never repeated consent loops.
        const login =
          previous && previous.status !== "connected"
            ? safeLogin(previous)
            : await begin(requirement, owner);
        return { ...row, connection_wait: login };
      }
      const values = adapter.values(requirement, connection);
      const env = Object.fromEntries(
        Object.entries(row.connection_env || {}).map(([name, source]) => {
          if (
            !Object.hasOwn(values, source) ||
            typeof values[source] !== "string"
          )
            throw new AppError(400, "连接无法提供环境模板需要的字段。");
          return [name, values[source]];
        }),
      );
      return {
        ...row,
        connection_wait: undefined,
        configuration: {
          ...row.configuration,
          connection: {
            service: adapter.service,
            name: connection.name,
            expires_at: connection.expires_at,
          },
        },
        connection: {
          id: connection.id,
          name: connection.name,
          subject_id: connection.subject_id,
          generation: connection.generation,
          expires_at: connection.expires_at,
        },
        request: {
          ...row.request,
          environment: {
            ...row.request.environment,
            env: {
              ...row.request.environment.env,
              ...env,
            },
          },
        },
      };
    },
    async tick() {
      for (const row of store
        .list("user_connections")
        .filter((r) => r.revocation_pending)) {
        try {
          await revoke(row);
        } catch {
          /* Retry provider revocation without restoring local access. */
        }
      }
      for (const row of store
        .list("connection_logins")
        .filter((r) => r.status === "pending")) {
        try {
          await poll(row.id);
        } catch {
          /* Keep the durable request for a later poll. */
        }
      }
    },
    async api(method, path, body) {
      if (path === "/app/connections" && method === "GET")
        return adapter.applications().map((application) => {
          const requirement = application;
          return {
            ...application,
            label: adapter.label,
            default_scopes: adapter.defaultScopes,
            required_scopes: [
              ...new Set(
                store
                  .list("agents")
                  .filter(
                    (a) =>
                      a.connection_requirement?.bot_id === application.bot_id,
                  )
                  .flatMap((a) =>
                    connectionScopes(a.connection_requirement.scopes),
                  ),
              ),
            ].join(" "),
            connection: safeConnection(
              selected(requirement, { kind: "local" }),
            ),
            login: safeLogin(
              store
                .list("connection_logins")
                .find(
                  (r) =>
                    r.owner.kind === "local" &&
                    r.requirement.app_id === application.app_id,
                ),
            ),
          };
        });
      const match = path.match(
        /^\/app\/connections\/([^/]+)\/([a-f0-9-]{36})$/,
      );
      const id = match?.[2];
      if (!id || !uuid.test(id) || match[1] !== adapter.service)
        throw new AppError(404, "Not found.");
      const requirement = adapter.requirement({
        service: adapter.service,
        bot_id: id,
        scopes: body?.scopes || adapter.defaultScopes,
      });
      if (method === "POST") return begin(requirement, { kind: "local" }, true);
      if (method !== "DELETE") throw new AppError(405, "Method not allowed.");
      const row = selected(requirement, { kind: "local" });
      if (row)
        store.put("user_connections", {
          ...row,
          status: "disconnected",
          generation: randomUUID(),
          revocation_pending: true,
        });
      for (const login of store
        .list("connection_logins")
        .filter(
          (r) =>
            r.owner.kind === "local" &&
            r.requirement.app_id === requirement.app_id &&
            r.status === "pending",
        ))
        store.put("connection_logins", {
          ...login,
          status: "cancelled",
          device_code: undefined,
        });
      for (const session of store
        .list("sessions")
        .filter(
          (r) =>
            r.connection_owner?.kind === "local" &&
            r.connection_requirement?.app_id === requirement.app_id &&
            r.connection_wait &&
            !r.core_session_id,
        ))
        store.put("sessions", {
          ...session,
          connection_wait: { ...session.connection_wait, status: "cancelled" },
        });
      if (row) {
        try {
          await revoke(row);
        } catch {
          /* Background polling retries revocation. */
        }
      }
      return { disconnected: true };
    },
  };
}

export function guardConnectionInput(store, path, body) {
  const id = path.match(
    /^\/v1\/agents\/sessions\/([a-f0-9-]{36})\/events$/,
  )?.[1];
  if (
    !id ||
    !body?.events?.some((e) => e.type !== "agent.session.input.cancel")
  )
    return;
  const row = store.list("sessions").find((r) => r.core_session_id === id);
  if (!row?.connection) return;
  const connection = store.get("user_connections", row.connection.id);
  if (
    row.connection.expires_at <= Date.now() ||
    connection?.generation !== row.connection.generation ||
    connection.status !== "connected"
  )
    throw new AppError(
      428,
      "此会话的账号连接已失效，请开启新会话。",
      "connection_expired",
    );
}
