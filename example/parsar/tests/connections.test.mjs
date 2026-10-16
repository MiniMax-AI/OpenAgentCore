import { feishuConnection } from "../server/feishu-connection.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { openStore } from "../server/store.mjs";
import {
  userConnections,
  guardConnectionInput,
} from "../server/connections.mjs";
import { productAPI } from "../server/product.mjs";

function fixture(t) {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const bot = {
    id: randomUUID(),
    name: "Office",
    app_id: "cli_test",
    app_secret: "app-secret",
    brand: "feishu",
  };
  store.put("feishu_bots", bot);
  let approved = false,
    subject = "ou_alice",
    failInfo = false,
    refreshError = false,
    exchangeCount = 0,
    refreshCount = 0;
  const fetch = async (url, options) => {
    if (url.endsWith("/device_authorization")) {
      assert.match(options.headers.Authorization, /^Basic /);
      assert.match(String(options.body), /offline_access/);
      return Response.json({
        device_code: "private-device",
        user_code: "ABCD",
        verification_uri: "https://accounts.feishu.cn/device",
        verification_uri_complete:
          "https://accounts.feishu.cn/device?code=ABCD",
        expires_in: 240,
        interval: 5,
      });
    }
    if (url.endsWith("/token")) {
      const fields =
        options.headers["Content-Type"] === "application/json"
          ? new URLSearchParams(JSON.parse(options.body))
          : new URLSearchParams(options.body);
      assert.equal(fields.get("client_secret"), "app-secret");
      if (fields.get("grant_type") === "refresh_token") {
        refreshCount++;
        if (refreshError)
          return Response.json({ error: "invalid_grant" }, { status: 400 });
      } else {
        exchangeCount++;
        if (!approved)
          return Response.json(
            { error: "authorization_pending" },
            { status: 400 },
          );
      }
      return Response.json({
        access_token: "private-access",
        refresh_token: "private-refresh",
        expires_in: 7200,
        refresh_token_expires_in: 604800,
        token_type: "Bearer",
        scope:
          "contact:user.base:readonly calendar:calendar:readonly offline_access",
      });
    }
    if (url.endsWith("/revoke")) return new Response(null, { status: 200 });
    assert.match(url, /\/user_info$/);
    assert.equal(options.headers.Authorization, "Bearer private-access");
    if (failInfo) throw new Error("temporary");
    return Response.json({
      code: 0,
      data: { open_id: subject, name: "Alice" },
    });
  };
  const requirement = {
    service: "feishu",
    bot_id: bot.id,
    app_id: bot.app_id,
    scopes: "calendar:calendar:readonly",
  };
  const ready = () => {
    for (const row of store.list("connection_logins"))
      store.put("connection_logins", { ...row, next_at: 0 });
  };
  const row = (owner = { kind: "local" }) => ({
    id: randomUUID(),
    connection_requirement: requirement,
    connection_env: {
      LARKSUITE_CLI_USER_ACCESS_TOKEN: "access_token",
      LARKSUITE_CLI_APP_ID: "client_id",
      LARKSUITE_CLI_BRAND: "region",
    },
    connection_owner: owner,
    request: {
      environment: { type: "openai_hosted", env: { HELLO: "world" } },
    },
  });
  return {
    store,
    bot,
    fetch,
    requirement,
    ready,
    row,
    approve: () => (approved = true),
    setSubject: (v) => (subject = v),
    failInfo: (v) => (failInfo = v),
    refreshError: (v) => (refreshError = v),
    counts: () => ({ exchangeCount, refreshCount }),
  };
}

test("connection device flow survives restart, verifies identity, resumes and reuses without exposing secrets", async (t) => {
  const f = fixture(t);
  let c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  const waiting = await c.prepare(f.row());
  assert.equal(waiting.connection_wait.status, "pending");
  assert.equal(
    waiting.request.environment.env.LARKSUITE_CLI_USER_ACCESS_TOKEN,
    undefined,
  );
  f.ready();
  await c.tick();
  assert.equal(f.store.list("user_connections").length, 0);
  f.approve();
  f.failInfo(true);
  f.ready();
  await c.tick();
  const exchanges = f.counts().exchangeCount;
  const exchanged = f.store.list("connection_logins")[0];
  const originalExpiry = exchanged.pending_tokens.expires_at - 30000;
  f.store.put("connection_logins", {
    ...exchanged,
    pending_tokens: { ...exchanged.pending_tokens, expires_at: originalExpiry },
  });
  c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  f.failInfo(false);
  f.ready();
  await c.tick();
  assert.equal(
    f.counts().exchangeCount,
    exchanges,
    "completed token exchange must not repeat after restart",
  );
  const prepared = await c.prepare(waiting);
  assert.equal(prepared.connection.expires_at, originalExpiry);
  assert.equal(prepared.connection_wait, undefined);
  assert.equal(prepared.connection.subject_id, "ou_alice");
  assert.equal(
    prepared.request.environment.env.LARKSUITE_CLI_USER_ACCESS_TOKEN,
    "private-access",
  );
  assert.equal(
    prepared.request.environment.env.LARKSUITE_CLI_APP_SECRET,
    undefined,
  );
  const publicRows = await c.api("GET", "/app/connections");
  assert.equal(publicRows[0].connection.name, "Alice");
  assert.doesNotMatch(JSON.stringify(publicRows), /private-|app-secret/);
  const reused = await c.prepare(f.row());
  assert.equal(reused.connection.id, prepared.connection.id);
  assert.equal(f.counts().exchangeCount, exchanges);
});

test("Feishu sender must match OAuth subject and cannot borrow a different local user's grant", async (t) => {
  const f = fixture(t);
  const c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  f.approve();
  const owner = { kind: "feishu", app_id: f.bot.app_id, subject_id: "ou_bob" };
  const waiting = await c.prepare(f.row(owner));
  f.ready();
  await c.tick();
  assert.equal(f.store.list("user_connections").length, 0);
  assert.equal(
    f.store.get("connection_logins", waiting.connection_wait.id).status,
    "denied",
  );
  const local = await c.prepare(f.row());
  f.ready();
  await c.tick();
  assert.ok((await c.prepare(local)).connection);
  assert.ok((await c.prepare(f.row(owner))).connection_wait);
  assert.ok(
    (
      await c.prepare(
        f.row({ kind: "feishu", app_id: f.bot.app_id, subject_id: "ou_alice" }),
      )
    ).connection,
  );
  await assert.rejects(
    c.prepare(
      f.row({ kind: "feishu", app_id: "cli_other", subject_id: "ou_alice" }),
    ),
    /应用必须一致/,
  );
});

test("refresh is serialized, invalid grants reconnect, and old Sessions never silently replace credentials", async (t) => {
  const f = fixture(t);
  const c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  f.approve();
  const waiting = await c.prepare(f.row());
  f.ready();
  await c.tick();
  const prepared = await c.prepare(waiting);
  const grant = f.store.list("user_connections")[0];
  f.store.put("user_connections", { ...grant, expires_at: Date.now() - 1 });
  const rows = await Promise.all([c.prepare(f.row()), c.prepare(f.row())]);
  assert.equal(f.counts().refreshCount, 1);
  assert.equal(rows[0].connection.id, rows[1].connection.id);
  const old = {
    ...prepared,
    core_session_id: randomUUID(),
    connection: { ...prepared.connection, expires_at: Date.now() - 1 },
  };
  f.store.put("sessions", old);
  assert.throws(
    () =>
      guardConnectionInput(
        f.store,
        `/v1/agents/sessions/${old.core_session_id}/events`,
        { events: [{ type: "agent.session.input.message" }] },
      ),
    /开启新会话/,
  );
  assert.doesNotThrow(() =>
    guardConnectionInput(
      f.store,
      `/v1/agents/sessions/${old.core_session_id}/events`,
      { events: [{ type: "agent.session.input.cancel" }] },
    ),
  );
  f.store.put("user_connections", {
    ...f.store.get("user_connections", grant.id),
    expires_at: Date.now() - 1,
  });
  f.refreshError(true);
  assert.ok((await c.prepare(f.row())).connection_wait);
  assert.equal(
    f.store.get("user_connections", grant.id).status,
    "reconnect_required",
  );
});

test("disconnect cancels pending consent and prevents reuse, while denied/expired consent does not loop", async (t) => {
  const f = fixture(t);
  const c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  const waiting = await c.prepare(f.row());
  f.store.put("sessions", waiting);
  await c.api("DELETE", `/app/connections/feishu/${f.bot.id}`, {});
  const cancelled = f.store.get("sessions", waiting.id);
  assert.equal(cancelled.connection_wait.status, "cancelled");
  const persisted = f.store.get(
    "connection_logins",
    waiting.connection_wait.id,
  );
  assert.equal(persisted.status, "cancelled");
  const result = await c.prepare(cancelled);
  assert.equal(result.connection_wait.status, "cancelled");
  assert.equal(f.store.list("connection_logins").length, 1);
});

test("product waits before Core creation and resumes original request exactly once after consent", async (t) => {
  const f = fixture(t);
  let calls = 0;
  const core = async (path, method, body) => {
    assert.equal(path, "/v1/agents/sessions");
    assert.equal(
      body.environment.env.LARKSUITE_CLI_USER_ACCESS_TOKEN,
      "private-access",
    );
    calls++;
    return { id: randomUUID() };
  };
  const api = productAPI(f.store, core, f.fetch);
  const put = (kind, body, id = randomUUID()) =>
    api("PUT", `/app/${kind}/${id}`, body);
  const provider = await put("providers", {
    name: "provider",
    base_url: "https://provider.example",
    api_key: "provider-key",
  });
  const model = await put("models", {
    name: "model",
    model: "m",
    provider_id: provider.id,
  });
  const env = await put("runtimes", {
    name: "env",
    connection_env: { LARKSUITE_CLI_USER_ACCESS_TOKEN: "access_token" },
  });
  const agent = await put("agents", {
    name: "agent",
    model_id: model.id,
    harness: "codex",
    instructions: "work",
    skill_ids: [],
    mcp_ids: [],
    connection_requirement: f.requirement,
  });
  const run = await put("sessions", {
    name: "task",
    input: "do work",
    agent_id: agent.id,
    runtime_id: env.id,
  });
  assert.equal(calls, 0);
  assert.ok(run.connection_wait);
  assert.doesNotMatch(JSON.stringify(run), /provider-key|private-/);
  f.approve();
  f.ready();
  await api.tickConnections();
  await api.tickConnections();
  assert.equal(calls, 1);
  const result = await api("GET", `/app/sessions/${run.id}`);
  assert.ok(result.core_session_id);
  assert.equal(result.connection_wait, undefined);
  assert.doesNotMatch(JSON.stringify(result), /private-|provider-key/);
});

test("disconnect revokes a connected grant and blocks snapshots", async (t) => {
  const f = fixture(t);
  const c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  f.approve();
  const waiting = await c.prepare(f.row());
  f.ready();
  await c.tick();
  const prepared = await c.prepare(waiting);
  await c.api("DELETE", `/app/connections/feishu/${f.bot.id}`, {});
  const grant = f.store.get("user_connections", prepared.connection.id);
  assert.equal(grant.status, "disconnected");
  assert.equal(grant.revocation_pending, false);
  assert.equal(grant.access_token, undefined);
  await assert.rejects(c.prepare(prepared), /开启新会话/);
});

test("reconnect cannot restore a task cancelled while device authorization is in flight", async (t) => {
  const f = fixture(t);
  const c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  const waiting = await c.prepare(f.row());
  f.store.put("sessions", waiting);
  let release, started;
  const inFlight = new Promise((resolve) => {
    started = resolve;
  });
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  let calls = 0;
  const api = productAPI(
    f.store,
    async () => {
      calls++;
      return { id: randomUUID() };
    },
    async (url, options) => {
      if (url.endsWith("/device_authorization")) {
        started();
        await gate;
      }
      return f.fetch(url, options);
    },
  );
  const retry = api("POST", `/app/sessions/${waiting.id}/connection`, {});
  const rejected = assert.rejects(retry, /会话状态已改变/);
  await inFlight;
  await api("DELETE", `/app/sessions/${waiting.id}`);
  release();
  await rejected;
  f.approve();
  f.ready();
  await api.tickConnections();
  assert.ok(f.store.get("sessions", waiting.id).deleted_at);
  assert.equal(f.store.get("sessions", waiting.id).connection_wait, undefined);
  assert.equal(calls, 0);
});

test("environment mappings freeze with waiting work and never bind refresh tokens or app secrets", async (t) => {
  const { runtimeProfile } = await import("../server/runtime-profile.mjs");
  const f = fixture(t);
  for (const connection_env of [
    { TOKEN: "refresh_token" },
    { TOKEN: "client_secret" },
    { PATH: "access_token" },
    { OAC_KEY: "access_token" },
    { TOKEN: "__proto__" },
  ])
    assert.throws(() => runtimeProfile({ connection_env }), /连接变量/);
  assert.throws(
    () =>
      runtimeProfile({
        env: { TOKEN: "fixed" },
        connection_env: { TOKEN: "access_token" },
      }),
    /连接变量/,
  );
  const profile = runtimeProfile({
    env: { TOOL_IDENTITY: "user" },
    connection_env: {
      TOOL_TOKEN: "access_token",
      TOOL_APPLICATION: "client_id",
    },
  });
  const c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  const waiting = await c.prepare({
    ...f.row(),
    connection_env: profile.connection_env,
    request: { environment: { env: profile.env } },
  });
  // Editing the template cannot change a previously frozen Session binding.
  f.store.put("runtimes", {
    id: randomUUID(),
    ...profile,
    connection_env: { OTHER_TOKEN: "access_token" },
  });
  f.approve();
  f.ready();
  await c.tick();
  const prepared = await c.prepare(waiting);
  assert.deepEqual(prepared.request.environment.env, {
    TOOL_IDENTITY: "user",
    TOOL_TOKEN: "private-access",
    TOOL_APPLICATION: "cli_test",
  });
  assert.doesNotMatch(
    JSON.stringify(prepared.request),
    /private-refresh|app-secret|LARKSUITE/,
  );
});

test("generic task connection lifecycle accepts an adapter without Feishu or CLI semantics", async (t) => {
  const f = fixture(t);
  const requirement = {
    service: "fixture",
    app_id: "application",
    bot_id: randomUUID(),
    scopes: "read",
  };
  const adapter = {
    service: "fixture",
    label: "Account",
    defaultScopes: "read",
    requirement: (value) => value,
    owner: () => ({ kind: "local" }),
    expectedSubject: () => undefined,
    applications: () => [requirement],
    authorize: async () => ({
      device_code: "device",
      verification_url: "https://account.example/consent",
      expires_in: 240,
    }),
    exchange: async () => ({
      access_token: "fixture-token",
      refresh_token: "private-refresh",
      expires_in: 3600,
      scope: "read",
    }),
    identity: async () => ({ subject_id: "account", name: "Example" }),
    refresh: async () => {
      throw new Error("unexpected refresh");
    },
    revoke: async () => {},
    values: (_, connection) => ({
      access_token: connection.access_token,
      client_id: "application",
      region: "example",
    }),
  };
  const c = userConnections(f.store, adapter);
  const waiting = await c.prepare({
    id: randomUUID(),
    connection_requirement: requirement,
    connection_owner: { kind: "local" },
    connection_env: { EXAMPLE_API_TOKEN: "access_token" },
    request: { environment: { env: {} } },
  });
  assert.equal(waiting.connection_wait.status, "pending");
  f.ready();
  await c.tick();
  const ready = await c.prepare(waiting);
  assert.deepEqual(ready.request.environment.env, {
    EXAMPLE_API_TOKEN: "fixture-token",
  });
  assert.equal(ready.configuration.connection.service, "fixture");
});

test("the Feishu case is ordinary template data with no personal credentials", async () => {
  const { readFile } = await import("node:fs/promises");
  const { runtimeProfile } = await import("../server/runtime-profile.mjs");
  const directory = new URL("../examples/feishu/", import.meta.url);
  const environment = JSON.parse(
    await readFile(new URL("environment.json", directory), "utf8"),
  );
  environment.setup_script = await readFile(
    new URL("setup.sh", directory),
    "utf8",
  );
  const profile = runtimeProfile(environment);
  assert.equal(
    profile.connection_env.LARKSUITE_CLI_USER_ACCESS_TOKEN,
    "access_token",
  );
  assert.equal(profile.env.LARKSUITE_CLI_STRICT_MODE, "user");
  assert.equal(profile.env.LARKSUITE_CLI_USER_ACCESS_TOKEN, undefined);
  assert.match(profile.setup_script, /checksum/i);
  assert.doesNotMatch(
    profile.setup_script,
    /10\.1\.171\.214|\/etc\/hosts|USER_ACCESS_TOKEN/,
  );
});

test("another Agent's consent retains the same account's existing grant scopes", async (t) => {
  const f = fixture(t);
  const c = userConnections(f.store, feishuConnection(f.store, f.fetch));
  f.approve();
  await c.prepare(f.row());
  f.ready();
  await c.tick();
  let requested;
  const adapter = feishuConnection(f.store, async (url, options) => {
    if (url.endsWith("/device_authorization"))
      requested = new URLSearchParams(options.body).get("scope");
    return f.fetch(url, options);
  });
  await userConnections(f.store, adapter).begin(
    { ...f.requirement, scopes: "docs:document:readonly" },
    { kind: "local" },
  );
  assert.match(requested, /calendar:calendar:readonly/);
  assert.match(requested, /docs:document:readonly/);
});
