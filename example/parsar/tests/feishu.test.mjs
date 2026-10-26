import { test } from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { openStore, AppError } from "../server/store.mjs";
import { productAPI } from "../server/product.mjs";
import { withSavedResources } from "./core-double.mjs";
import { feishuOperations } from "../server/feishu.mjs";
import { feishuWorker as workerForBot } from "../server/feishu.mjs";

const botId = "11111111-1111-4111-8111-111111111111";
const feishuWorker = (...args) => workerForBot(...args, botId);

async function setup(t) {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const calls = [],
    receipts = new Map(),
    resources = new Map();
  let lose = false,
    reject = false;
  const core = async (path, method, body, key) => {
    calls.push({ path, method, body, key });
    if (method === "POST") {
      if (reject) throw new AppError(422, "invalid");
      if (!receipts.has(key)) {
        if (path.endsWith("/events")) receipts.set(key, undefined);
        else {
          const id = randomUUID();
          receipts.set(key, { id });
          resources.set(id, {
            status: "idle",
            turns: [{ id: randomUUID(), status: "completed" }],
            items: [],
          });
        }
      }
      if (lose) {
        lose = false;
        throw new Error("lost response");
      }
      return receipts.get(key);
    }
    const id = path.split("/")[4],
      resource = resources.get(id);
    if (!resource) throw new AppError(404, "missing");
    if (path.includes("/turns"))
      return { data: resource.turns, has_more: false };
    if (path.includes("/items"))
      return { data: resource.items, has_more: false };
    if (path.includes("/artifacts")) return { data: [], has_more: false };
    return { id, status: resource.status };
  };
  const savedCore = withSavedResources(core);
  const api = productAPI(store, savedCore);
  const put = (kind, body, id = randomUUID()) =>
    api("PUT", `/app/${kind}/${id}`, body);
  const provider = await put("providers", {
    name: "Provider",
    base_url: "https://models.example/v1",
    api_key: "test-provider-key",
  });
  const model = await put("models", {
    name: "Model",
    provider_id: provider.id,
    model: "test-model",
  });
  const runtime = await put("runtimes", {
    name: "Text",
    environment: "openai_hosted",
  });
  const agent = await put("agents", {
    name: "Engineer",
    harness: "codex",
    model_id: model.id,
    runtime_id: runtime.id,
    instructions: "Work carefully",
    skill_ids: [],
    mcp_ids: [],
  });
  return {
    store,
    core: savedCore,
    api,
    put,
    agent,
    runtime,
    calls,
    receipts,
    resources,
    setLose: () => (lose = true),
    setReject: (value) => (reject = value),
  };
}
async function feishu(t) {
  const f = await setup(t),
    sent = [];
  await f.api("PUT", `/app/feishu/bots/${botId}`, {
    revision: 0,
    app_id: "cli_0123456789abcdef",
    app_secret: "never-return-me",
    enabled: true,
    agent_id: f.agent.id,
    direct: false,
    groups: true,
  });
  const channel = {
    readContext: async (row) => ({
      text: row.text,
      resources: row.resources || [],
      notice: "",
    }),
    connect: async () => {},
    disconnect: async () => {},
    getConnectionStatus: () => ({ state: "connected" }),
    rawClient: {
      im: {
        message: {
          patch: async () => ({ code: 0 }),
          reply: async (payload) => {
            sent.push(payload);
            return {
              code: 0,
              data: { message_id: `om_reply_${payload.data.uuid}` },
            };
          },
        },
      },
    },
  };
  Object.assign(channel, feishuOperations(channel.rawClient));
  const create = () =>
    feishuWorker(f.store, f.core, f.api, async () => channel);
  const message = {
    messageId: "om_first",
    chatId: "oc_test",
    chatType: "group",
    senderId: "ou_user",
    mentionedBot: true,
    content: "Investigate the checkout flow",
  };
  return { ...f, sent, create, message, channel };
}
test("Feishu settings redact secrets, default-deny chat dispatch and persist ingress before processing", async (t) => {
  const f = await feishu(t),
    worker = f.create();
  assert.equal(
    JSON.stringify(await f.api("GET", `/app/feishu/bots/${botId}`)).includes(
      "never-return-me",
    ),
    false,
  );
  for (const patch of [
    { mentionedBot: false },
    { chatType: "p2p" },
    { senderId: "" },
  ])
    worker.receive("cli_0123456789abcdef", { ...f.message, ...patch });
  assert.equal(f.store.list("feishu_inbound").length, 0);
  worker.receive("cli_0123456789abcdef", f.message);
  worker.receive("cli_0123456789abcdef", f.message);
  assert.equal(f.store.list("feishu_inbound").length, 1);
  assert.equal(f.calls.length, 0);
  const restarted = f.create();
  await restarted.tick();
  assert.equal(f.receipts.size, 1);
  assert.equal(f.store.list("feishu_inbound")[0].state, "delivered");
  assert.equal(f.sent.length, 1);
  assert.ok(f.sent.every((x) => x.data.reply_in_thread));
  assert.equal(f.sent[0].data.msg_type, "interactive");
  assert.match(
    JSON.parse(f.sent[0].data.content).config.summary.content,
    /已完成/,
  );
  await restarted.tick();
  assert.equal(f.sent.length, 1);
  await restarted.tick();
  assert.equal(f.sent.length, 1);
  const thread = f.store.list("feishu_threads")[0];
  worker.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_next",
    rootId: "om_first",
    content: "Also check mobile",
  });
  await restarted.tick();
  assert.equal(f.store.list("feishu_threads").length, 1);
  assert.equal(f.store.list("sessions").length, 1);
  assert.equal(f.store.get("sessions", thread.session_id).source, "feishu");
  assert.equal(f.calls.filter((x) => x.path.endsWith("/events")).length, 1);
  assert.equal(
    f.calls.find((x) => x.path.endsWith("/events")).body.events[0].input[0]
      .content[0].text,
    "Also check mobile",
  );
});

test("Feishu retries lost Core response with same identity and expires uncertain outbound safely", async (t) => {
  const f = await feishu(t),
    worker = f.create();
  worker.receive("cli_0123456789abcdef", f.message);
  f.setLose();
  await worker.tick();
  const inbound = f.store.list("feishu_inbound")[0];
  f.store.put("feishu_inbound", { ...inbound, next_at: 0 });
  const restarted = f.create();
  await restarted.tick();
  assert.equal(f.receipts.size, 1);
  const posts = f.calls.filter((x) => x.method === "POST");
  assert.equal(posts[0].key, posts[1].key);
  const out = f.store.list("feishu_outbound")[0];
  f.store.put("feishu_outbound", {
    ...out,
    state: "queued",
    first_attempt_at: Date.now() - 3600000,
    next_at: 0,
  });
  const count = f.sent.length;
  await restarted.tick();
  assert.equal(f.sent.length, count);
  assert.equal(f.store.get("feishu_outbound", out.id).state, "failed");
});

test("Feishu disabling pauses processing and changing app never reroutes queued receipts", async (t) => {
  const f = await feishu(t),
    worker = f.create();
  worker.receive("cli_0123456789abcdef", f.message);
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...config,
    enabled: false,
  });
  await worker.tick();
  assert.equal(f.calls.length, 0);
  await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...config,
    revision: config.revision + 1,
    app_id: "cli_fedcba9876543210",
    app_secret: "other",
    enabled: true,
  });
  await worker.tick();
  assert.equal(f.calls.length, 0);
});

test("Feishu commands do not poison new topics and rejected controls do not block later messages", async (t) => {
  const f = await feishu(t),
    w = f.create();
  w.receive("cli_0123456789abcdef", { ...f.message, content: "/done" });
  await w.tick();
  assert.equal(f.store.list("feishu_inbound")[0].state, "rejected");
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_task",
    rootId: "om_first",
  });
  await w.tick();
  assert.equal(f.store.list("sessions").length, 1);
  const run = f.store.list("sessions")[0];
  f.resources.get(run.core_session_id).status = "in_progress";
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_done",
    rootId: "om_first",
    content: "/done",
  });
  await w.tick();
  assert.equal(f.store.list("feishu_inbound")[0].state, "rejected");
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_cancel",
    rootId: "om_first",
    content: "/stop",
  });
  await w.tick();
  assert.ok(
    f.calls.some(
      (x) => x.body?.events?.[0].type === "agent.session.input.cancel",
    ),
  );
});

test("uncertain follow-up retains its event across retries, and history resumes at its cursor", async (t) => {
  const f = await feishu(t),
    w = f.create();
  w.receive("cli_0123456789abcdef", f.message);
  await w.tick();
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_follow",
    rootId: "om_first",
    content: "Follow up",
  });
  f.setLose();
  await w.tick();
  const row = f.store.list("feishu_inbound")[0];
  f.store.put("feishu_inbound", { ...row, next_at: 0 });
  await w.tick();
  const events = f.calls.filter((x) => x.path.endsWith("/events"));
  assert.equal(events.length, 2);
  assert.deepEqual(events[0], events[1]);
  assert.equal(f.store.get("feishu_inbound", row.id).state, "delivered");
  assert.ok(
    f.calls.some(
      (x) => x.path.includes("/turns?") && x.path.includes("after="),
    ),
  );
});

test("Feishu closes failed connections and never sends through a config changed during connect", async (t) => {
  const f = await feishu(t);
  let disconnects = 0,
    connections = 0,
    state = "failed";
  f.channel.disconnect = async () => {
    disconnects++;
  };
  f.channel.connect = async () => {
    connections++;
  };
  f.channel.getConnectionStatus = () => ({ state });
  const w = f.create();
  await w.tick();
  state = "connected";
  await w.tick();
  state = "failed";
  await w.tick();
  assert.equal(connections, 2);
  assert.equal(disconnects, 1);
  await w.stop();
  const changed = feishuWorker(f.store, f.core, f.api, async () => ({
    ...f.channel,
    connect: async () => {
      const config = await f.api("GET", `/app/feishu/bots/${botId}`);
      await f.api("PUT", `/app/feishu/bots/${botId}`, {
        ...config,
        enabled: false,
      });
    },
  }));
  changed.receive("cli_0123456789abcdef", f.message);
  await changed.tick();
  assert.equal(f.receipts.size, 0);
  await changed.stop();
});

test("official Feishu dispatcher awaits persistence and propagates a failed commit", async (t) => {
  const { Client, WSClient } = await import("@larksuiteoapi/node-sdk");
  const { createFeishuTransport } = await import("../server/feishu.mjs");
  let dispatcher,
    received = 0,
    fail = false;
  t.mock.method(Client.prototype, "request", async () => ({
    code: 0,
    bot: { open_id: "ou_bot", app_name: "Parsar" },
  }));
  t.mock.method(WSClient.prototype, "start", async (options) => {
    dispatcher = options.eventDispatcher;
  });
  t.mock.method(WSClient.prototype, "close", () => {});
  const transport = await createFeishuTransport(
    { app_id: "cli_0123456789abcdef", app_secret: "test" },
    (message) => {
      assert.equal(message.content, "A task");
      assert.equal(message.mentionedBot, true);
      if (fail) throw new Error("commit failed");
      received++;
    },
  );
  await transport.connect();
  const event = {
    schema: "2.0",
    header: { event_type: "im.message.receive_v1" },
    event: {
      sender: { sender_type: "user", sender_id: { open_id: "ou_user" } },
      message: {
        message_id: "om_test",
        chat_id: "oc_test",
        chat_type: "group",
        message_type: "text",
        content: JSON.stringify({ text: "@_user_1 A task" }),
        mentions: [
          { key: "@_user_1", id: { open_id: "ou_bot" }, name: "Parsar" },
        ],
      },
    },
  };
  await dispatcher.invoke(event, { needCheck: false });
  assert.equal(received, 1);
  const external = structuredClone(event);
  external.header.tenant_key = "tenant_bot";
  external.event.sender.tenant_key = "tenant_guest";
  external.event.message.message_id = "om_external";
  await dispatcher.invoke(external, { needCheck: false });
  assert.equal(received, 1, "messages from another tenant are ignored");
  fail = true;
  await assert.rejects(
    dispatcher.invoke(event, { needCheck: false }),
    /commit failed/,
  );
  assert.equal(received, 1);
  await transport.disconnect();
});

test("unresolved Feishu receipts remain discoverable after more than 100 newer deliveries", async (t) => {
  const f = await feishu(t),
    w = f.create();
  w.receive("cli_0123456789abcdef", f.message);
  f.setReject(true);
  await w.tick();
  const failed = f.store.list("feishu_inbound")[0];
  assert.equal(failed.state, "failed");
  for (let i = 0; i < 110; i++)
    f.store.put("feishu_outbound", {
      id: randomUUID(),
      state: "delivered",
      thread_id: failed.thread_id,
      created_at: failed.created_at + i + 1,
    });
  const visible = await f.api("GET", "/app/feishu/deliveries");
  assert.ok(visible.some((row) => row.id === failed.id));
  f.setReject(false);
  await f.api("PUT", `/app/feishu/deliveries/inbound/${failed.id}`, {});
  await w.tick();
  assert.equal(f.store.get("feishu_inbound", failed.id).state, "delivered");
});

test("busy Core input retries past eight attempts and queued replies precede Core reads", async (t) => {
  const f = await feishu(t),
    initial = f.create();
  initial.receive("cli_0123456789abcdef", f.message);
  await initial.tick();
  let busy = true;
  const order = [];
  const core = async (...args) => {
    order.push("core");
    if (busy && args[0].endsWith("/events"))
      throw new AppError(409, "Earlier input is pending");
    return f.core(...args);
  };
  const reply = f.channel.rawClient.im.message.reply;
  f.channel.rawClient.im.message.reply = async (payload) => {
    order.push("reply");
    return reply(payload);
  };
  const w = feishuWorker(f.store, core, f.api, async () => f.channel);
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_busy",
    rootId: "om_first",
    content: "More work",
  });
  const incoming = f.store.list("feishu_inbound")[0];
  f.store.put("feishu_inbound", { ...incoming, attempts: 9 });
  const out = f.store.list("feishu_outbound")[0];
  f.store.put("feishu_outbound", { ...out, state: "queued", next_at: 0 });
  await w.tick();
  assert.equal(order[0], "reply");
  const pending = f.store.get("feishu_inbound", incoming.id);
  assert.equal(pending.state, "submitting");
  assert.equal(pending.attempts, 10);
  busy = false;
  f.store.put("feishu_inbound", { ...pending, next_at: 0 });
  await w.tick();
  assert.equal(f.store.get("feishu_inbound", incoming.id).state, "delivered");
});

test("terminal public API conflicts preserve their code and stop automatic Feishu retry", async (t) => {
  const { coreClient } = await import("../server.mjs");
  const failed = coreClient(
    { target: "https://core.invalid", key: "test" },
    async () =>
      Response.json(
        {
          error: {
            code: "environment_unavailable",
            message: "Environment unavailable",
          },
        },
        { status: 409 },
      ),
  );
  await assert.rejects(
    failed("/v1/agents/sessions", "POST", {}),
    (error) => error.code === "environment_unavailable",
  );
  const f = await feishu(t),
    initial = f.create();
  initial.receive("cli_0123456789abcdef", f.message);
  await initial.tick();
  const w = feishuWorker(
    f.store,
    async (...args) =>
      args[0].endsWith("/events") ? failed(...args) : f.core(...args),
    f.api,
    async () => f.channel,
  );
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_gone",
    rootId: "om_first",
    content: "Follow up",
  });
  await w.tick();
  const receipt = f.store.list("feishu_inbound")[0];
  assert.equal(receipt.state, "failed");
  assert.equal(receipt.error, "Environment unavailable");
});

test("Feishu cancellation bypasses busy follow-ups and withdraws them permanently", async (t) => {
  const f = await feishu(t),
    initial = f.create();
  initial.receive("cli_0123456789abcdef", f.message);
  await initial.tick();
  const events = [];
  const core = async (...args) => {
    if (args[0].endsWith("/events")) {
      events.push(args[2].events[0].type);
      if (args[2].events[0].type === "agent.session.input.message")
        throw new AppError(409, "Earlier input pending");
    }
    return f.core(...args);
  };
  const w = feishuWorker(f.store, core, f.api, async () => f.channel);
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_wait",
    rootId: "om_first",
    content: "Follow up",
  });
  await w.tick();
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_stop",
    rootId: "om_first",
    content: "/stop",
  });
  await w.tick();
  assert.deepEqual(events, [
    "agent.session.input.message",
    "agent.session.input.cancel",
  ]);
  assert.equal(
    f.store.list("feishu_inbound").find((row) => row.message_id === "om_wait")
      .state,
    "rejected",
  );
  await w.tick();
  assert.equal(events.length, 2);
});

test("Feishu can withdraw an unstarted conversation and recover a lost creation before cancelling", async (t) => {
  const f = await feishu(t),
    w = f.create();
  w.receive("cli_0123456789abcdef", f.message);
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_stop",
    rootId: "om_first",
    content: "/stop",
  });
  await w.tick();
  assert.equal(f.receipts.size, 0);
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_new",
    content: "Start new task",
  });
  f.setLose();
  await w.tick();
  assert.equal(f.receipts.size, 1);
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_cancel_pending",
    rootId: "om_new",
    content: "/stop",
  });
  await w.tick();
  assert.equal(f.store.list("sessions").length, 1);
  assert.ok(
    f.calls.some(
      (call) => call.body?.events?.[0].type === "agent.session.input.cancel",
    ),
  );
});

test("pinned capability versions reach the saved Agent and retired capabilities cannot start new work", async (t) => {
  const f = await setup(t),
    id = randomUUID();
  const cap = await f.api("PUT", `/app/capabilities/${id}`, {
    name: "Team rules",
    kind: "prompt",
    content: "Always verify",
    description: "",
  });
  const agent = await f.put(
    "agents",
    {
      ...f.agent,
      capability_ids: [id],
      capability_versions: { [id]: "1" },
    },
    f.agent.id,
  );
  await f.api("PUT", `/app/capabilities/${id}`, {
    ...cap,
    content: "Updated rules",
    default_version: "",
  });
  const run = await f.api("PUT", `/app/sessions/${randomUUID()}`, {
    name: "Rules",
    agent_id: agent.id,
    input: "Work",
  });
  // A pinned version reaches the saved Core Agent the Session references.
  const saved = f.core.agentOf(f.calls[0].body);
  assert.match(saved.instructions, /Always verify/);
  assert.doesNotMatch(saved.instructions, /Updated rules/);
  const current = await f.api("GET", `/app/capabilities/${id}`);
  await f.api("PUT", `/app/capabilities/${id}`, {
    ...current,
    deprecated: true,
  });
  await assert.rejects(
    f.api("PUT", `/app/sessions/${randomUUID()}`, {
      name: "New",
      agent_id: agent.id,
      input: "hi",
    }),
    { status: 400 },
  );
  assert.ok(run.core_session_id);
  assert.ok((await f.api("GET", "/app/audit")).length > 0);
});

test("Agent Skill version selectors are validated and frozen in public requests", async (t) => {
  const f = await setup(t),
    skillId = "skill_test";
  const runtime = await f.put("runtimes", {
    name: "Hosted",
    environment: "openai_hosted",
  });
  const model = f.store.get("models", f.agent.model_id);
  const provider = f.store.get("providers", model.provider_id);
  await f.put(
    "providers",
    { ...provider, base_url: "https://model.example/v1", api_key: "test-key" },
    provider.id,
  );
  const agent = await f.put(
    "agents",
    { ...f.agent, skill_ids: [skillId], skill_versions: { [skillId]: "2" } },
    f.agent.id,
  );
  await f.api("PUT", `/app/sessions/${randomUUID()}`, {
    name: "Pinned",
    agent_id: agent.id,
    input: "Run",
  });
  assert.deepEqual(f.calls[0].body.environment.skills, [
    { type: "skill_reference", skill_id: skillId, version: "2" },
  ]);
  await assert.rejects(
    f.put(
      "agents",
      { ...agent, skill_versions: { [skillId]: "latest" } },
      agent.id,
    ),
    { status: 400 },
  );
});

test("Feishu QR registration throttles polling, persists credentials without returning them, and resumes safely", async (t) => {
  const { feishuRegistrationAPI } = await import(
    "../server/feishu-registration.mjs"
  );
  const store = openStore(":memory:");
  t.after(() => store.close());
  const calls = [],
    id = randomUUID();
  let outcome = "slow_down";
  const registrationFetch = async (url, init) => {
    assert.ok(
      [
        "https://accounts.feishu.cn/oauth/v1/app/registration",
        "https://accounts.larksuite.com/oauth/v1/app/registration",
      ].includes(url),
    );
    assert.equal(init.redirect, "error");
    const action = init.body.get("action");
    calls.push(action);
    if (action === "begin")
      return Response.json({
        device_code: "private-device",
        user_code: "code",
        expires_in: 300,
        interval: 5,
      });
    if (
      action === "poll" &&
      outcome === "success" &&
      url.includes("accounts.feishu.cn")
    )
      return Response.json(
        { error: "authorization_pending", user_info: { tenant_brand: "lark" } },
        { status: 400 },
      );
    if (action === "poll")
      return outcome === "success"
        ? Response.json({
            client_id: "cli_0123456789abcdef",
            client_secret: "private-app-secret",
          })
        : Response.json({ error: outcome }, { status: 400 });
    return Response.json({});
  };
  const api = feishuRegistrationAPI(store, registrationFetch);
  const path = `/app/feishu/provision/${id}`;
  const begin = await api("POST", path, { action: "begin", name: "Assistant" });
  assert.equal(begin.status, "pending");
  assert.equal(JSON.stringify(begin).includes("private-device"), false);
  await api("POST", path, { action: "begin" });
  assert.deepEqual(calls, ["init", "begin"]);
  const pending = await api("POST", path, { action: "poll" });
  assert.equal(pending.interval, 10);
  await api("POST", path, { action: "poll" });
  assert.equal(calls.filter((action) => action === "poll").length, 1);
  store.put("feishu_registration", {
    ...store.get("feishu_registration", id),
    next_at: 0,
  });
  outcome = "success";
  const handoff = await api("POST", path, { action: "poll" });
  assert.equal(handoff.status, "pending");
  assert.equal(store.get("feishu_registration", id).brand, "lark");
  const done = await feishuRegistrationAPI(store, registrationFetch)(
    "POST",
    path,
    { action: "poll" },
  );
  assert.equal(done.status, "success");
  assert.equal(JSON.stringify(done).includes("private-app-secret"), false);
  assert.equal(store.get("feishu_bots", id).app_secret, "private-app-secret");
  assert.equal(store.get("feishu_bots", id).brand, "lark");
  assert.equal(store.get("feishu_bots", id).enabled, false);
  assert.equal(store.get("feishu_registration", id).device_code, undefined);
  await feishuRegistrationAPI(store, () => {
    throw new Error("must not poll completed registration");
  })("POST", path, { action: "poll" });
});

test("settled Feishu threads wake for unmentioned image replies and release processed media", async (t) => {
  const f = await feishu(t),
    downloads = [],
    reactions = [],
    worker = f.create();
  const model = f.store.get("models", f.agent.model_id),
    provider = f.store.get("providers", model.provider_id);
  await f.put(
    "providers",
    { ...provider, base_url: "https://model.example/v1", api_key: "test-key" },
    provider.id,
  );
  await f.put(
    "runtimes",
    { ...f.runtime, environment: "openai_hosted" },
    f.runtime.id,
  );
  f.channel.rawClient.im.messageResource = {
    get: async (request) => {
      downloads.push(request);
      return {
        getReadableStream: async function* () {
          yield Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
        },
      };
    },
  };
  f.channel.rawClient.im.messageReaction = {
    create: async (payload) => {
      reactions.push(["create", payload]);
      return { code: 0, data: { reaction_id: "reaction" } };
    },
    delete: async (payload) => {
      reactions.push(["delete", payload]);
      return { code: 0 };
    },
  };
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  await worker.tick();
  let thread = f.store.list("feishu_threads")[0];
  assert.ok(thread.settled_at);
  f.store.put("feishu_threads", {
    ...thread,
    settled_at: Date.now() - 6 * 60000,
  });
  const count = f.calls.length;
  await worker.tick();
  assert.equal(
    f.calls.length,
    count,
    "finished threads stop polling after their grace period",
  );
  worker.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_image",
    rootId: "om_first",
    mentionedBot: false,
    content: "![image](img_test)",
    resources: [{ type: "image", fileKey: "img_test" }],
  });
  thread = f.store.list("feishu_threads")[0];
  assert.equal(thread.settled_at, undefined);
  await worker.tick();
  assert.equal(downloads.length, 1);
  const event = f.calls.findLast((call) => call.path.endsWith("/events"));
  assert.equal(event.body.events[0].input[0].content[1].type, "input_image");
  assert.doesNotMatch(
    event.body.events[0].input[0].content[0].text,
    /img_test/,
  );
  assert.equal(
    f.store.list("feishu_inbound").find((row) => row.message_id === "om_image")
      .media,
    undefined,
  );
  assert.ok(reactions.some(([operation]) => operation === "delete"));
});

test("Feishu context expands quoted media and merged forwards after durable ingress", async () => {
  const { feishuContextReader } = await import("../server/feishu-context.mjs");
  const row = {
    message_id: "om_current",
    chat_id: "oc_test",
    chat_type: "group",
    sender_id: "ou_user",
    text: "Summarize",
    reply_to_message_id: "om_quote",
    resources: [],
    created_at: Date.now(),
  };
  const calls = [];
  const reader = feishuContextReader(
    {
      im: {
        message: {
          get: async ({ path }) => {
            calls.push(path.message_id);
            return {
              code: 0,
              data: {
                items:
                  path.message_id === "om_quote"
                    ? [
                        {
                          message_id: "om_quote",
                          chat_id: "oc_test",
                          msg_type: "image",
                          body: {
                            content: JSON.stringify({ image_key: "img_quote" }),
                          },
                          sender: { id: "ou_other" },
                          parent_id: "om_current",
                        },
                      ]
                    : [
                        {
                          message_id: "om_current",
                          msg_type: "merge_forward",
                          body: { content: "{}" },
                        },
                        {
                          message_id: "om_child",
                          upper_message_id: "om_current",
                          msg_type: "text",
                          sender: { id: "ou_other" },
                          body: {
                            content: JSON.stringify({
                              text: "Forwarded evidence",
                            }),
                          },
                        },
                      ],
              },
            };
          },
        },
      },
    },
    { openId: "ou_bot", name: "Parsar" },
  );
  const quoted = await reader(row, true);
  assert.match(quoted.text, /Quoted message/);
  assert.equal(quoted.resources[0].message_id, "om_quote");
  assert.equal(quoted.resources[0].fileKey, "img_quote");
  assert.deepEqual(calls, ["om_quote"]);
  const forward = await reader(
    {
      ...row,
      reply_to_message_id: undefined,
      raw_content_type: "merge_forward",
    },
    true,
  );
  assert.match(forward.text, /Forwarded evidence/);
});

test("Feishu context never reads earlier group messages", async () => {
  const { feishuContextReader } = await import("../server/feishu-context.mjs");
  const reader = feishuContextReader(
    {
      im: {
        message: {
          list: async () => assert.fail("group history must not be read"),
        },
      },
    },
    { openId: "ou_bot" },
  );
  const context = await reader({
    message_id: "om_current",
    chat_id: "oc_test",
    chat_type: "group",
    text: "Current task",
    created_at: Date.now(),
  });
  assert.equal(context.text, "Current task");
  assert.equal(context.notice, "");
});

test("/clear in a topic stops its work and the next message there starts a new conversation", async (t) => {
  const f = await feishu(t),
    worker = f.create();
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  const [run] = f.store.list("sessions");
  f.resources.get(run.core_session_id).status = "in_progress";
  worker.receive("cli_0123456789abcdef", { ...f.message, messageId: "om_clear", rootId: "om_first", content: "/clear" });
  await worker.tick();
  const stopped = f.calls.find((x) => x.body?.events?.[0].type === "agent.session.input.cancel");
  assert.ok(stopped, "running work in the cleared conversation is stopped");
  assert.ok(stopped.path.includes(run.core_session_id));
  assert.ok(f.store.list("feishu_outbound").some((row) => row.payload.includes("已清空这个话题的上下文")));
  worker.receive("cli_0123456789abcdef", { ...f.message, messageId: "om_after", rootId: "om_first", content: "Start over" });
  await worker.tick();
  const sessions = f.store.list("sessions");
  assert.equal(sessions.length, 2, "the topic now has a new conversation");
  const fresh = sessions.find((row) => row.id !== run.id);
  assert.equal(fresh.name, "Start over");
  assert.equal(f.store.list("feishu_threads").length, 1, "the topic keeps one thread");
  assert.equal(f.store.list("feishu_threads")[0].session_id, fresh.id);
});

test("Feishu terminal results survive histories beyond one hundred pages", async (t) => {
  const f = await feishu(t);
  let pages = 0;
  const core = async (path, ...args) => {
    if (path.includes("/items?")) {
      const after = new URL(path, "http://fixture").searchParams.get("after");
      const page = after ? Number(after.slice(5)) + 1 : 0;
      pages++;
      return { data: [], has_more: page < 101, last_id: `page_${page}` };
    }
    return f.core(path, ...args);
  };
  const worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  assert.equal(pages, 102);
  assert.ok(
    f.store
      .list("feishu_outbound")
      .some(
        (row) =>
          row.state === "delivered" &&
          String(row.payload).includes('"content":"已完成"'),
      ),
  );
});

test("Session configuration summary is frozen with the creation request and excludes credentials", async (t) => {
  const f = await setup(t);
  const id = randomUUID();
  f.setLose();
  await assert.rejects(
    f.api("PUT", `/app/sessions/${id}`, {
      name: "Snapshot",
      agent_id: f.agent.id,
      input: "Work",
    }),
    /lost/,
  );
  const before = (await f.api("GET", `/app/sessions/${id}`)).configuration;
  await f.api("PUT", `/app/agents/${f.agent.id}`, {
    ...f.agent,
    name: "Changed",
    harness: "claude_sdk",
  });
  const settled = await f.api("PUT", `/app/sessions/${id}`, {});
  assert.deepEqual(settled.configuration, before);
  assert.equal(before.agent_name, f.agent.name);
  assert.equal(before.harness, "codex");
  assert.equal(settled.request, undefined);
  assert.equal(JSON.stringify(before).includes("api_key"), false);
  assert.equal(JSON.stringify(before).includes("instructions"), false);
});

test("Feishu updates a single card in place with thinking and steps, omits Web tails, and rotates expired p2p sessions", async (t) => {
  const f = await feishu(t),
    patches = [];
  f.channel.rawClient.im.message.patch = async (payload) => {
    patches.push(payload);
    return { code: 0 };
  };
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${botId}`, { ...config, direct: true });
  let expireEvents = false,
    finishTurn = false;
  const items = [
    {
      id: "item_1",
      turn_id: "turn_1",
      type: "message",
      role: "assistant",
      content: [{ type: "output_text", text: "先读取飞书技能文档。" }],
    },
    {
      id: "item_2",
      turn_id: "turn_1",
      type: "command_execution",
      command:
        "/bin/bash -lc 'cat /workspace/.agents/skills/lark-shared/SKILL.md'",
      duration_ms: 420,
      status: "completed",
    },
  ];
  const worker = feishuWorker(
    f.store,
    async (path, method, body, key) => {
      if (expireEvents && method === "POST" && path.endsWith("/events")) {
        throw new AppError(
          409,
          "The environment is no longer available for new input.",
          "environment_unavailable",
        );
      }
      if (method === "GET" && path.includes("/turns")) {
        return {
          data: [
            {
              id: "turn_1",
              status: finishTurn ? "completed" : "in_progress",
            },
          ],
          has_more: false,
        };
      }
      if (method === "GET" && path.includes("/items")) {
        return { data: items, has_more: false };
      }
      if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) {
        return {
          id: path.split("/")[4],
          status: finishTurn ? "idle" : "in_progress",
        };
      }
      return f.core(path, method, body, key);
    },
    f.api,
    async () => f.channel,
  );
  const dm = {
    ...f.message,
    chatType: "p2p",
    chatId: "oc_dm_single",
    mentionedBot: false,
    messageId: "om_dm_1",
    content: "你好",
  };
  worker.receive("cli_0123456789abcdef", dm);
  await worker.tick();
  assert.equal(f.sent.length, 1);
  const firstCard = JSON.parse(f.sent[0].data.content);
  assert.equal(firstCard.schema, "2.0");
  assert.equal(firstCard.config.summary.content, "处理中 · 正在整理结果 · 2 步");
  // The header says what is happening now; the body starts with the timeline.
  assert.deepEqual(firstCard.header, { title: { tag: "plain_text", content: "处理中 · 正在整理结果 · 2 步" }, template: "blue" });
  const [said, ran] = firstCard.body.elements;
  assert.equal(said.tag, "markdown");
  assert.equal(said.content, "先读取飞书技能文档。");
  assert.equal(ran.tag, "collapsible_panel");
  assert.equal(ran.expanded, false);
  // One line per row: a long command is shortened, and its full text is one click away.
  assert.match(ran.header.title.content, /运行.* cat \/workspace\/\.agents\/skills\/.*….*0\.4s/);
  assert.match(ran.elements[0].header.title.content, /运行.* cat \/workspace.*…/);
  assert.match(ran.elements[0].elements[0].text.content, /命令\ncat \/workspace\/\.agents\/skills\/lark-shared\/SKILL\.md/);

  finishTurn = true;
  items.push({
    id: "item_3",
    turn_id: "turn_1",
    type: "message",
    role: "assistant",
    content: [{ type: "output_text", text: "你好！我是飞书工作助手。" }],
  });
  await worker.tick();
  assert.equal(f.sent.length, 1, "must not send a second card or text ping");
  assert.equal(patches.length, 1, "must patch the existing card in place");
  const patchedCard = JSON.parse(patches[0].data.content);
  assert.equal(patchedCard.config.summary.content, "已完成");
  assert.deepEqual(patchedCard.header, { title: { tag: "plain_text", content: "已完成" }, template: "green" });
  // The answer first; the whole process folds into one row.
  const [answer, rule, process] = patchedCard.body.elements;
  assert.equal(answer.content, "你好！我是飞书工作助手。");
  assert.equal(rule.tag, "hr");
  assert.equal(process.tag, "collapsible_panel");
  assert.equal(process.expanded, false);
  assert.match(process.header.title.content, /处理过程 · 2 步/);
  assert.equal(process.elements[0].content, "先读取飞书技能文档。");
  assert.match(process.elements[1].elements[0].header.title.content, /cat \/workspace/);
  assert.doesNotMatch(
    JSON.stringify(patchedCard),
    /任务编号|在 Web 会话下载|Parsar/,
  );

  expireEvents = true;
  worker.receive("cli_0123456789abcdef", {
    ...dm,
    messageId: "om_dm_2",
    content: "你还在吗",
  });
  await worker.tick();
  assert.equal(
    f.store.list("sessions").length,
    2,
    "expired p2p environment automatically starts a fresh session",
  );
  assert.equal(
    f.store.list("feishu_inbound").find((r) => r.message_id === "om_dm_2").state,
    "delivered",
  );

  // Deleting the Web conversation starts a fresh private conversation on the next message.
  expireEvents = false;
  const currentThread = f.store.list("feishu_threads")[0];
  await f.api("DELETE", `/app/sessions/${currentThread.session_id}`);
  worker.receive("cli_0123456789abcdef", {
    ...dm,
    messageId: "om_dm_3",
    content: "收到请回复",
  });
  await worker.tick();
  assert.equal(f.store.list("feishu_threads").length, 1);
  assert.equal(
    f.store.list("sessions").filter((row) => !row.deleted_at).length,
    2,
  );
  assert.equal(
    f.calls.findLast((call) => call.path === "/v1/agents/sessions").body.input,
    "收到请回复",
  );
  const listedSessions = await f.api("GET", "/app/sessions");
  assert.equal(
    listedSessions.every(
      (s) => s.source === "feishu" && s.feishu_chat_type === "p2p",
    ),
    true,
  );
});

test("concurrent creations of one Session send one public request", async (t) => {
  const f = await setup(t),
    id = randomUUID();
  const body = { name: "Once", agent_id: f.agent.id, input: "Work" };
  await Promise.all([
    f.api("PUT", `/app/sessions/${id}`, body),
    f.api("PUT", `/app/sessions/${id}`, body),
  ]);
  assert.equal(f.calls.filter((call) => call.method === "POST").length, 1);
  f.setReject(true);
  await assert.rejects(
    f.api("PUT", `/app/sessions/${randomUUID()}`, body),
    /invalid/,
  );
  assert.equal(f.store.list("sessions").length, 1);
});

test("bot settings bind one Agent, switch group and private chat on and validate before enabling", async (t) => {
  const f = await feishu(t);
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  assert.equal(config.groups, true);
  assert.equal(config.agent_id, f.agent.id);
  for (const patch of [
    { agent_id: randomUUID() },
    { agent_id: "", enabled: true },
    { groups: false, direct: false, enabled: true },
  ])
    await assert.rejects(
      f.api("PUT", `/app/feishu/bots/${botId}`, { ...config, ...patch }),
      (error) => error.status === 400,
    );
  const disabled = await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...config,
    agent_id: "",
    enabled: false,
  });
  assert.equal(disabled.agent_id, "");
  const direct = await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...disabled,
    agent_id: f.agent.id,
    groups: false,
    direct: true,
    enabled: true,
  });
  assert.equal(direct.direct, true);
  const w = f.create();
  w.receive("cli_0123456789abcdef", { ...f.message, chatId: "oc_any_group" });
  assert.equal(
    f.store.list("feishu_inbound").length,
    0,
    "groups stay ignored while group chat is off",
  );
  await f.api("PUT", `/app/feishu/bots/${botId}`, { ...direct, groups: true });
  w.receive("cli_0123456789abcdef", { ...f.message, chatId: "oc_any_group" });
  assert.equal(
    f.store.list("feishu_inbound").length,
    1,
    "any group the bot has joined works once group chat is on",
  );
});

test("multiple Feishu bots route to their own Agents and private chats need direct messages enabled", async (t) => {
  const f = await feishu(t),
    secondId = randomUUID();
  const other = await f.put("agents", {
    ...f.agent,
    id: undefined,
    revision: undefined,
    name: "Designer",
  });
  await f.api("PUT", `/app/feishu/bots/${secondId}`, {
    revision: 0,
    name: "Dedicated",
    app_id: "cli_fedcba9876543210",
    app_secret: "second-secret",
    enabled: true,
    agent_id: other.id,
    direct: true,
  });
  const w = workerForBot(
    f.store,
    f.core,
    f.api,
    async () => f.channel,
    secondId,
  );
  const dm = {
    ...f.message,
    chatType: "p2p",
    chatId: "oc_dm",
    mentionedBot: false,
  };
  w.receive("cli_0123456789abcdef", dm);
  assert.equal(f.store.list("feishu_inbound").length, 0);
  w.receive("cli_fedcba9876543210", dm);
  await w.tick();
  assert.equal(f.store.list("sessions").length, 1);
  assert.equal(f.store.list("sessions")[0].agent_id, other.id);
  assert.equal(f.store.list("sessions")[0].feishu_chat_type, "p2p");
  w.receive("cli_fedcba9876543210", {
    ...dm,
    messageId: "om_followup",
    content: "Continue",
  });
  await w.tick();
  assert.equal(f.store.list("sessions").length, 1);
  assert.equal(
    f.calls.filter((call) => call.path.endsWith("/events")).length,
    1,
  );
  assert.equal(f.sent[0].data.reply_in_thread, false);
  assert.equal(f.sent[0].data.msg_type, "interactive");
  await assert.rejects(
    f.api("PUT", `/app/feishu/bots/${randomUUID()}`, {
      revision: 0,
      app_id: "cli_fedcba9876543210",
      app_secret: "secret",
      enabled: false,
    }),
    { status: 409 },
  );
});

test("group topics are shared by their participants and private chats stay per sender", async (t) => {
  const f = await feishu(t),
    w = f.create();
  w.receive("cli_0123456789abcdef", f.message);
  await w.tick();
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_colleague",
    rootId: "om_first",
    senderId: "ou_colleague",
    mentionedBot: false,
    content: "Add the payment step",
  });
  await w.tick();
  assert.equal(f.store.list("sessions").length, 1);
  assert.equal(
    f.store.list("feishu_inbound").find((row) => row.message_id === "om_colleague")
      .state,
    "delivered",
  );
  const toColleague = {
    ...f.message,
    rootId: "om_first",
    senderId: "ou_colleague",
    mentionedBot: false,
    mentions: [{ key: "@_user_1", openId: "ou_jelly", name: "Jelly", isBot: false }],
  };
  w.receive("cli_0123456789abcdef", {
    ...toColleague,
    messageId: "om_to_jelly",
    content: "@Jelly 你在这里发条消息",
  });
  assert.equal(
    f.store.list("feishu_inbound").some((row) => row.message_id === "om_to_jelly"),
    false,
    "a topic reply that mentions only someone else is not for the bot",
  );
  w.receive("cli_0123456789abcdef", {
    ...toColleague,
    messageId: "om_to_both",
    mentionedBot: true,
    mentions: [
      ...toColleague.mentions,
      { key: "@_user_2", openId: "ou_bot", name: "Parsar", isBot: true },
    ],
    content: "@Parsar @Jelly 一起看一下",
  });
  assert.ok(
    f.store.list("feishu_inbound").some((row) => row.message_id === "om_to_both"),
  );
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${botId}`, { ...config, direct: true });
  for (const [i, sender] of ["ou_a", "ou_b", "ou_a"].entries()) {
    w.receive("cli_0123456789abcdef", {
      ...f.message,
      messageId: `om_dm_${i}`,
      chatType: "p2p",
      chatId: `oc_dm_${sender}`,
      senderId: sender,
      mentionedBot: false,
      content: `Message ${i}`,
    });
    await w.tick();
  }
  assert.equal(f.store.list("sessions").length, 3);
});

test("Feishu private informational and rejected commands retain the active conversation", async (t) => {
  const f = await feishu(t);
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${botId}`, { ...config, direct: true });
  const worker = f.create();
  for (const [i, content] of [
    "Task one",
    "/help",
    "/done",
    "Continue task one",
  ].entries()) {
    worker.receive("cli_0123456789abcdef", {
      ...f.message,
      messageId: `om_private_${i}`,
      chatType: "p2p",
      mentionedBot: false,
      content,
    });
    await worker.tick();
  }
  assert.equal(f.store.list("sessions").length, 1);
  assert.equal(f.store.list("feishu_threads").length, 1);
  assert.equal(
    f.calls.filter((call) => call.path.endsWith("/events")).length,
    1,
  );
});

test("a failure shows its cause with the failed step right below, and a plain answer adds no rows", async () => {
  const { formatTurnCard, feishuCard } = await import("../server/feishu.mjs");
  const card = JSON.parse(
    formatTurnCard(
      "failed",
      [
        {
          type: "command_execution",
          command: "lark-cli docs +fetch --doc abc",
          status: "failed",
          duration_ms: 1200,
        },
      ],
      "permission denied",
    ),
  );
  assert.deepEqual(card.header, { title: { tag: "plain_text", content: "执行失败" }, template: "red" });
  assert.equal(card.config.summary.content, "执行失败");
  const [cause, , failed, process] = card.body.elements;
  assert.match(cause.content, /<font color='red'>permission denied<\/font>/);
  assert.match(failed.content, /运行.* lark-cli docs \+fetch --doc abc .*<font color='red'>失败<\/font>/);
  assert.match(process.header.title.content, /处理过程 · 1 步/);
  const quiet = JSON.parse(
    formatTurnCard("completed", [
      { type: "message", role: "assistant", content: [{ type: "output_text", text: "答案" }] },
    ]),
  );
  assert.deepEqual(
    quiet.body.elements.map((element) => element.tag),
    ["markdown"],
  );
  assert.equal(quiet.body.elements[0].content, "答案");
  const plain = feishuCard("已请求取消，等待 Agent 确认停止。");
  assert.equal(plain.header, undefined);
  assert.equal(plain.body.elements[0].content, "已请求取消，等待 Agent 确认停止。");
  assert.equal(feishuCard("执行失败\n\n环境准备失败").header.template, "red");
});

test("web searches appear as readable steps in the Feishu record", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const card = JSON.parse(
    formatTurnCard("completed", [
      {
        type: "web_search_call",
        status: "completed",
        action: { type: "search", query: "OpenAgentCore release" },
      },
      { type: "message", role: "assistant", content: [{ type: "output_text", text: "Done" }] },
    ]),
  );
  const search = card.body.elements[2].elements[0];
  assert.match(search.header.title.content, /搜索网页.* OpenAgentCore release/);
});

test("Feishu delivers QR during authorization and artifact media once, retaining uploaded keys after a lost send", async (t) => {
  const f = await feishu(t);
  const uploads = [];
  f.channel.rawClient.request = async ({ url, data, headers }) => {
    assert.equal(headers["Content-Type"], "multipart/form-data");
    const type = url.endsWith("/images") ? "image" : "file";
    uploads.push({ type, data });
    return { code: 0, data: { [type === "image" ? "image_key" : "file_key"]: `${type}-${uploads.length}` } };
  };
  const registrationId = randomUUID();
  f.store.put("feishu_registration", {
    id: registrationId, status: "pending", expires_at: Date.now() + 300000,
    verification_url: "https://open.feishu.cn/page/cli?user_code=test-code",
    device_code: "private-device",
  });
  let waiting = true;
  const turnId = randomUUID(), itemId = randomUUID(), imageId = randomUUID(), fileId = randomUUID();
  const png = await (await import("qrcode")).default.toBuffer("media-test");
  const core = async (path, ...args) => {
    if (path.endsWith(`/${imageId}/content`)) return png;
    if (path.endsWith(`/${fileId}/content`)) return Buffer.from("report content");
    const value = await f.core(path, ...args);
    if (path.includes("/turns")) return { data: [{ id: turnId, status: waiting ? "in_progress" : "completed" }], has_more: false };
    if (path.includes("/items")) return { data: [{ id: itemId, turn_id: turnId, type: "message", role: "assistant", content: [{ type: "output_text", text: `Scan [authorize](https://open.feishu.cn/page/cli?user_code=test-code)\n![飞书授权二维码](/app/feishu/provision/${registrationId}/qrcode)` }] }], has_more: false };
    if (path.includes("/artifacts")) return { data: waiting ? [] : [
      { id: imageId, session_id: path.split("/")[4], turn_id: turnId, path: "/workspace/outputs/chart.png", size_bytes: png.length },
      { id: fileId, session_id: path.split("/")[4], turn_id: turnId, path: "/workspace/outputs/report.txt", size_bytes: 14 },
    ], has_more: false };
    if (args[0] === "GET") return { ...value, status: waiting ? "requires_action" : "idle" };
    return value;
  };
  let worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  assert.equal(uploads.length, 1, "QR is visible while waiting for the user");
  assert.ok(f.sent.some((row) => row.data.msg_type === "interactive" && row.data.content.includes("https://open.feishu.cn/page/cli?user_code=test-code")), "authorization link stays visible on the waiting card");
  assert.equal(f.sent.filter((row) => row.data.msg_type === "image").length, 1);
  const originalReply = f.channel.rawClient.im.message.reply;
  let lost = false;
  f.channel.rawClient.im.message.reply = async (payload) => {
    const result = await originalReply(payload);
    if (payload.data.msg_type === "file" && !lost) { lost = true; throw new Error("lost response"); }
    return result;
  };
  waiting = false;
  const put = f.store.put;
  let interrupted = false;
  f.store.put = (kind, row) => {
    if (!interrupted && kind === "feishu_outbound" && row.media?.name === "chart.png") {
      interrupted = true;
      throw new Error("injected persistence failure");
    }
    return put(kind, row);
  };
  await worker.tick();
  assert.equal(interrupted, true);
  assert.equal(f.store.list("feishu_outbound").filter((row) => row.payload === "" && !row.media).length, 0, "no partial empty reply survives the failed transaction");
  f.store.put = put;
  worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  await worker.tick();
  assert.equal(uploads.length, 3);
  assert.deepEqual(uploads.map((row) => row.type), ["image", "image", "file"]);
  const pending = f.store.list("feishu_outbound").find((row) => row.media?.name === "report.txt");
  assert.equal(pending.state, "queued");
  assert.equal(pending.uploaded_media.msg_type, "file");
  f.store.put("feishu_outbound", { ...pending, next_at: 0 });
  worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  await worker.tick();
  await worker.tick();
  assert.equal(uploads.length, 3, "restart reuses the persisted upload key");
  const sends = f.sent.filter((row) => row.data.msg_type === "file");
  assert.equal(sends.length, 2);
  assert.equal(sends[0].data.uuid, sends[1].data.uuid);
  assert.equal(sends[0].data.content, sends[1].data.content);
  assert.equal(f.store.get("feishu_outbound", pending.id).state, "delivered");
  assert.equal(f.store.list("feishu_outbound").filter((row) => row.media).length, 3);
  const publicRows = await f.api("GET", "/app/feishu/deliveries");
  assert.ok(publicRows.every((row) => !row.media && !row.uploaded_media));
});

test("media limits apply to streamed bytes and active formats are never classified as images", async () => {
  const { imageMime, readMediaBytes, maxMediaBytes, qrImages } = await import("../shared/media.mjs");
  assert.equal(imageMime(Buffer.from('<svg onload="alert(1)"/>')), "");
  assert.equal(imageMime(Buffer.from("RIFFxxxxFAKE")), "");
  await assert.rejects(readMediaBytes(new Response(new Uint8Array(maxMediaBytes + 1))), RangeError);
  assert.deepEqual(qrImages("![external](https://evil.example/image.png)"), []);
  const transport = feishuOperations({ request: async ({ data }) => {
    assert.equal(data.file_name, "picture.svg");
    return { code: 0, data: { file_key: "file-svg" } };
  } });
  assert.equal((await transport.upload(Buffer.from("<svg/>"), "picture.svg")).msg_type, "file");
});


test("Feishu SDK uploads multipart bytes and preserves definite rejection and retryable errors", async (t) => {
  const { createServer } = await import("node:http");
  const { Client } = await import("@larksuiteoapi/node-sdk");
  let code = 0, status = 200;
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    res.setHeader("Content-Type", "application/json");
    if (req.url.includes("/auth/")) return res.end(JSON.stringify({ code: 0, tenant_access_token: "fixture-token", app_access_token: "fixture-token", expire: 7200 }));
    assert.equal(req.url, "/open-apis/im/v1/files");
    assert.match(req.headers["content-type"], /multipart\/form-data; boundary=/);
    assert.ok(Buffer.concat(chunks).includes(Buffer.from("binary-file-content")));
    res.statusCode = status;
    res.end(JSON.stringify({ code, msg: "credential-must-not-leak", data: code === 0 ? { file_key: "uploaded-file" } : undefined }));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const client = new Client({ appId: "fixture-app", appSecret: "fixture-secret", domain: `http://127.0.0.1:${server.address().port}`, logger: { error() {}, warn() {}, info() {}, debug() {}, trace() {} } });
  const upload = () => feishuOperations(client).upload(Buffer.from("binary-file-content"), "report.txt");
  assert.equal((await upload()).msg_type, "file");
  code = 99991672;
  await assert.rejects(upload(), (error) => error.status === 422 && !error.message.includes("credential-must-not-leak"));
  code = 99991400; status = 400;
  await assert.rejects(upload(), { status: 503 });
  code = 0; status = 503;
  await assert.rejects(upload(), { status: 503 });
  const disconnected = feishuOperations({ request: async () => { throw new Error("private-network-data"); } });
  await assert.rejects(disconnected.upload(Buffer.from("file"), "file.txt"), (error) => error.status === 503 && !error.message.includes("private-network-data"));
});

test("consecutive tool calls fold into one row, each joined with its result and folding its diagnostics", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const card = JSON.parse(formatTurnCard("in_progress", [
    { type: "reasoning", summary: [{ text: "先检查接口，再处理结果。" }] },
    { type: "function_call", call_id: "lookup", name: "lookup", arguments: { query: "invoice" }, status: "completed" },
    { type: "function_call_output", call_id: "lookup", status: "failed", error: "permission denied", output: "<at id=all>not a mention</at>```" },
    { type: "command_execution", command: "pwd", cwd: "/workspace", output: "/workspace", exit_code: 0, duration_ms: 420, status: "completed" },
    { type: "mcp_call", name: "read", server_label: "docs", arguments: { id: 3 }, status: "incomplete" },
  ]));
  assert.match(card.header.title.content, /2 步/);
  const [thought, calls] = card.body.elements;
  assert.match(thought.header.title.content, /思考.*先检查接口/);
  assert.equal(thought.elements[0].content, "先检查接口，再处理结果。");
  assert.equal(calls.tag, "collapsible_panel");
  assert.match(calls.header.title.content, /调用 3 次工具.*<font color='red'>失败 1 次<\/font>/);
  const [lookup, pwd, read] = calls.elements;
  assert.match(lookup.header.title.content, /调用工具.* lookup .*<font color='red'>失败<\/font>/);
  const literal = lookup.elements[0].text;
  assert.equal(literal.tag, "plain_text");
  assert.match(literal.content, /invoice/);
  assert.match(literal.content, /permission denied/);
  assert.match(literal.content, /<at id=all>/);
  assert.match(pwd.header.title.content, /运行.* pwd .*0\.4s/);
  assert.match(pwd.elements[0].text.content, /退出码\n0/);
  assert.match(read.header.title.content, /调用工具.* docs · read .*未完成/);
});

test("resumed questions display only the current exchange, including on completion", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const message = (value) => ({ type: "message", role: "assistant", content: [{ type: "output_text", text: value }] });
  const items = [
    message("OLD INVENTORY"),
    { type: "command_execution", command: "old command", status: "completed" },
    { type: "function_call", call_id: "q1", name: "ask_user" },
    { type: "function_call_output", call_id: "q1", status: "completed", output: "create a bot" },
    message("CURRENT CONTEXT"),
    { type: "function_call", call_id: "q2", name: "ask_user" },
  ];
  const card = JSON.parse(formatTurnCard("requires_action", items, undefined, undefined, [{ arguments: JSON.stringify({ question: "What should the bot do?", options: ["Documents", "Reports"] }) }]));
  assert.match(card.body.elements[0].content, /^What should the bot do/);
  assert.doesNotMatch(card.body.elements[0].content, /CURRENT CONTEXT/);
  assert.match(card.body.elements[2].elements[0].content, /CURRENT CONTEXT/);
  assert.doesNotMatch(JSON.stringify(card), /OLD INVENTORY|old command|function_call_output|工具调用/);
  items.push({ type: "function_call_output", call_id: "q2", output: "Reports" }, message("DONE"));
  assert.doesNotMatch(formatTurnCard("completed", items), /CURRENT CONTEXT|OLD INVENTORY/);
});

test("waiting questions stay on one card and a reply never replays stale history while Core catches up", async (t) => {
  const f = await feishu(t);
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${botId}`, { ...config, direct: true });
  const patches = [];
  f.channel.rawClient.im.message.patch = async (payload) => { patches.push(payload); return { code: 0 }; };
  const message = (id, text) => ({ id, turn_id: "same-turn", type: "message", role: "assistant", content: [{ type: "output_text", text }] });
  let items = [message("old", "OLD INVENTORY"), { id: "ask1", turn_id: "same-turn", type: "function_call", name: "ask_user", call_id: "q1" }];
  let actions = [{ type: "function_call", name: "ask_user", call_id: "q1", turn_id: "same-turn", arguments: { question: "FIRST QUESTION" } }];
  let status = "requires_action";
  const core = async (path, method, body, key) => {
    if (method === "GET" && path.includes("/items")) return { data: items, has_more: false };
    if (method === "GET" && path.includes("/turns")) return { data: [{ id: "same-turn", status: status === "idle" ? "completed" : "in_progress" }], has_more: false };
    if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) return { id: path.split("/")[4], status, required_actions: actions };
    return f.core(path, method, body, key);
  };
  let worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  const dm = { ...f.message, chatType: "p2p", mentionedBot: false };
  worker.receive("cli_0123456789abcdef", dm);
  await worker.tick();
  assert.equal(f.sent.length, 1, "question belongs to the progress card");
  assert.match(f.sent[0].data.content, /FIRST QUESTION/);
  await worker.tick();
  assert.equal(f.sent.length, 1);
  worker.receive("cli_0123456789abcdef", { ...dm, messageId: "answer1", content: "create a bot" });
  await worker.tick();
  assert.equal(f.sent.length, 2);
  assert.doesNotMatch(f.sent[1].data.content, /OLD INVENTORY|FIRST QUESTION/);
  // Restart with the persisted boundary before Core publishes the answer result.
  worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  await worker.tick();
  assert.equal(f.sent.length, 2);
  items.push({ id: "reply1", turn_id: "same-turn", type: "function_call_output", call_id: "q1", output: "create a bot" }, message("new", "CURRENT CONTEXT"), { id: "ask2", turn_id: "same-turn", type: "function_call", name: "ask_user", call_id: "q2" });
  actions = [{ ...actions[0], call_id: "q2", arguments: { question: "SECOND QUESTION" } }];
  await worker.tick();
  assert.equal(f.sent.length, 2, "second question updates the reply card");
  assert.match(patches.at(-1).data.content, /SECOND QUESTION/);
  assert.doesNotMatch(patches.at(-1).data.content, /OLD INVENTORY|FIRST QUESTION/);
  const currentItems = items;
  items = items.slice(0, 2); // The pre-submission snapshot can also lag behind the question being answered.
  worker.receive("cli_0123456789abcdef", { ...dm, messageId: "answer2", content: "reports" });
  await worker.tick();
  assert.equal(f.sent.length, 2, "do not submit against a snapshot older than the last displayed question");
  items = currentItems;
  const pendingAnswer = f.store.list("feishu_inbound").find((row) => row.message_id === "answer2");
  f.store.put("feishu_inbound", { ...pendingAnswer, next_at: 0 });
  await worker.tick();
  assert.equal(f.sent.length, 3);
  assert.doesNotMatch(f.sent[2].data.content, /CURRENT CONTEXT|SECOND QUESTION/);
  const beforeCatchup = patches.length;
  await worker.tick();
  assert.ok(patches.slice(beforeCatchup).every((patch) => !/CURRENT CONTEXT|SECOND QUESTION/.test(patch.data.content)));
  // Both history and required_actions can lag behind a previous question after restart.
  const currentActions = actions;
  items = items.slice(0, 2);
  actions = [{ ...actions[0], call_id: "q1", arguments: { question: "FIRST QUESTION" } }];
  const patchCount = patches.length;
  worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  await worker.tick();
  assert.equal(patches.length, patchCount, "an older snapshot must not overwrite the current card");
  items = currentItems;
  await worker.tick();
  assert.ok(patches.slice(patchCount).every((patch) => !/FIRST QUESTION|OLD INVENTORY/.test(patch.data.content)));
  actions = currentActions;
  status = "idle"; actions = [];
  const beforeFinal = patches.length;
  await worker.tick();
  assert.equal(patches.length, beforeFinal, "completed status must not consume a reply whose tool result is still absent");
  assert.equal(f.store.list("feishu_threads")[0].turn_cursor, undefined);
  worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  items.push({ id: "reply2", turn_id: "same-turn", type: "function_call_output", call_id: "q2", output: "reports" }, message("done", "FINAL ANSWER"));
  await worker.tick();
  assert.equal(f.sent.length, 3);
  assert.match(patches.at(-1).data.content, /FINAL ANSWER/);
  assert.doesNotMatch(patches.at(-1).data.content, /OLD INVENTORY|CURRENT CONTEXT|SECOND QUESTION/);
});

test("large multilingual diagnostics stay below the Feishu card payload limit", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const large = '大\\\\"\n'.repeat(10000);
  const items = [
    { type: "reasoning", summary: [{ text: large }] },
    { type: "message", role: "assistant", content: [{ type: "output_text", text: large }] },
    ...Array.from({ length: 20 }, (_, i) => ({ type: "mcp_call", name: `tool${i}`, status: "completed", arguments: { input: large }, output: large })),
    { type: "message", role: "assistant", content: [{ type: "output_text", text: large }] },
  ];
  for (const status of ["in_progress", "completed", "requires_action"]) {
    const payload = formatTurnCard(status, items, undefined, undefined, [{ arguments: { question: large, options: [large] } }]);
    assert.ok(Buffer.byteLength(payload) < 30000, `${status}: ${Buffer.byteLength(payload)}`);
    assert.match(payload, /更早的 \d+ (步|次调用)见网页对话/, "the oldest rows leave the card first");
    assert.match(payload, /内容较长，已截取/);
  }
});


test("an undelivered failed progress card cannot hide a later question", async (t) => {
  const f = await feishu(t);
  let waiting = false, rejectSend = true;
  const send = f.channel.send;
  f.channel.send = async (row) => {
    if (rejectSend) throw new AppError(422, "definite send failure");
    return send(row);
  };
  const core = async (path, method, ...rest) => {
    if (method === "GET" && path.includes("/turns")) return { data: [{ id: "turn", status: "in_progress" }], has_more: false };
    if (method === "GET" && path.includes("/items")) return { data: [], has_more: false };
    if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) return { id: path.split("/")[4], status: waiting ? "requires_action" : "in_progress", required_actions: waiting ? [{ type: "function_call", name: "ask_user", call_id: "question", arguments: { question: "Choose a purpose" } }] : [] };
    return f.core(path, method, ...rest);
  };
  const worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  const failed = f.store.list("feishu_outbound").find((x) => x.state === "failed");
  assert.ok(failed);
  waiting = true; rejectSend = false;
  await worker.tick();
  await worker.tick();
  assert.equal(f.sent.length, 1);
  assert.match(f.sent[0].data.content, /Choose a purpose/);
  assert.equal(f.store.get("feishu_outbound", failed.id).state, "failed");
});

test("identical question text is shown once and agent messages do not count as tools", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const card = JSON.parse(formatTurnCard("requires_action", [
    { type: "create_subagent_call", status: "completed" },
    { type: "agent_message", content: "subagent progress" },
    { type: "message", role: "assistant", content: [{ type: "output_text", text: "Which bot?" }] },
  ], undefined, undefined, [{ arguments: { question: "Which bot?", options: ["Writer", "Analyst"] } }]));
  assert.equal(card.body.elements[0].content.split("Which bot?").length - 1, 1);
  assert.match(card.body.elements[0].content, /Writer/);
  assert.match(card.body.elements.at(-1).header.title.content, /处理过程 · 1 步/);
  assert.doesNotMatch(JSON.stringify(card), /agent_message|subagent progress/);
});


test("authorization instructions stay visible across message phases and later replies", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const id = "11111111-1111-4111-8111-111111111111";
  for (const phase of ["commentary", "final_answer"]) {
    for (const status of ["in_progress", "requires_action", "completed"]) {
      const card = JSON.parse(formatTurnCard(status, [
        { type: "message", role: "assistant", phase, content: [{ type: "output_text", text: `请应用管理员扫码 [授权](https://open.feishu.cn/page/launcher?user_code=test)\n![QR](/app/feishu/provision/${id}/qrcode)` }] },
        { type: "message", role: "assistant", phase: "commentary", content: [{ type: "output_text", text: "等待授权完成。" }] },
      ], undefined, undefined, [{ arguments: { question: "授权完成后告诉我" } }]));
      assert.match(card.body.elements[0].content, /请应用管理员扫码/);
      assert.match(card.body.elements[0].content, /https:\/\/open.feishu.cn/);
      assert.match(card.body.elements[0].content, /授权二维码见图片消息/);
      assert.equal(JSON.stringify(card).split("user_code=test").length - 1, 1);
    }
  }
});

test("an uncertain progress send is updated after its deduplicated retry", async (t) => {
  const f = await feishu(t);
  let waiting = false, lose = true, rejectEdit = true, displayed = "", patches = 0;
  const delivered = new Map();
  f.channel.send = async (row) => {
    if (!delivered.has(row.id)) {
      delivered.set(row.id, { id: `om_${row.id}` });
      displayed = row.payload;
    }
    if (lose) { lose = false; throw new Error("lost ACK"); }
    return delivered.get(row.id);
  };
  f.channel.edit = async (_id, payload) => {
    patches++;
    if (rejectEdit) throw new AppError(422, "update rejected");
    displayed = payload;
  };
  const core = async (path, method, ...rest) => {
    if (method === "GET" && path.includes("/turns")) return { data: [{ id: "turn", status: "in_progress" }], has_more: false };
    if (method === "GET" && path.includes("/items")) return { data: [], has_more: false };
    if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) return { id: path.split("/")[4], status: waiting ? "requires_action" : "in_progress", required_actions: waiting ? [{ type: "function_call", name: "ask_user", call_id: "question", arguments: { question: "Choose a purpose" } }] : [] };
    return f.core(path, method, ...rest);
  };
  let worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  waiting = true;
  await worker.tick();
  const pending = f.store.list("feishu_outbound").find((row) => row.state === "queued");
  f.store.put("feishu_outbound", { ...pending, next_at: 0 });
  worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  await worker.tick();
  assert.ok(f.store.get("feishu_outbound", pending.id).sent_message_id, "retain the confirmed send receipt even if the update fails");
  await worker.tick();
  assert.equal(delivered.size, 1, "a failed update must not create another question card");
  rejectEdit = false;
  await worker.tick();
  assert.equal(f.store.get("feishu_outbound", pending.id).state, "delivered");
  assert.equal(delivered.size, 1);
  assert.ok(patches > 0);
  assert.match(displayed, /Choose a purpose/);
});


test("ordinary follow-ups exclude old content while Core publishes a delayed input", async (t) => {
  const f = await feishu(t);
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${botId}`, { ...config, direct: true });
  const items = [{ id: "old", turn_id: "same-turn", type: "message", role: "assistant", content: [{ type: "output_text", text: "OLD EXCHANGE" }] }];
  const patches = [];
  f.channel.rawClient.im.message.patch = async (payload) => { patches.push(payload); return { code: 0 }; };
  const core = async (path, method, ...rest) => {
    if (method === "GET" && path.includes("/turns")) return { data: [{ id: "same-turn", status: "in_progress" }], has_more: false };
    if (method === "GET" && path.includes("/items")) return { data: items, has_more: false };
    if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) return { id: path.split("/")[4], status: "in_progress" };
    return f.core(path, method, ...rest);
  };
  let worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  const dm = { ...f.message, chatType: "p2p", mentionedBot: false };
  worker.receive("cli_0123456789abcdef", dm);
  await worker.tick();
  worker.receive("cli_0123456789abcdef", { ...dm, messageId: "followup", content: "Focus on the report" });
  await worker.tick();
  assert.equal(f.sent.length, 2);
  assert.doesNotMatch(f.sent[1].data.content, /OLD EXCHANGE/);
  assert.equal(f.store.list("feishu_threads")[0].display_after, "old");
  worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  await worker.tick();
  items.push({ id: "late-old-output", turn_id: "same-turn", type: "message", role: "assistant", content: [{ type: "output_text", text: "OLD_LATE" }] });
  const patchCount = patches.length;
  await worker.tick();
  assert.equal(patches.length, patchCount, "wait for the actual user input before showing late history");
  items.push(
    { id: "new-user", turn_id: "same-turn", type: "message", role: "user", content: [{ type: "input_text", text: "Focus on the report" }] },
    { id: "new-output", turn_id: "same-turn", type: "message", role: "assistant", content: [{ type: "output_text", text: "CURRENT EXCHANGE" }] },
  );
  await worker.tick();
  assert.match(patches.at(-1).data.content, /CURRENT EXCHANGE/);
  assert.doesNotMatch(patches.at(-1).data.content, /OLD EXCHANGE/);
  const complete = [...items];
  items.splice(1);
  worker.receive("cli_0123456789abcdef", { ...dm, messageId: "third", content: "Now summarize" });
  await worker.tick();
  assert.equal(f.sent.length, 2, "an older snapshot cannot move the next input boundary backwards");
  items.splice(0, items.length, ...complete);
  const pending = f.store.list("feishu_inbound").find((row) => row.message_id === "third");
  f.store.put("feishu_inbound", { ...pending, next_at: 0 });
  await worker.tick();
  assert.equal(f.sent.length, 3);
  assert.doesNotMatch(f.sent[2].data.content, /CURRENT EXCHANGE|OLD/);
  const beforeThird = patches.length;
  worker.receive("cli_0123456789abcdef", { ...dm, messageId: "fourth", content: "Keep it short" });
  await worker.tick();
  assert.equal(f.sent.length, 3, "rapid follow-ups wait for the previous input to become visible");
  assert.equal(patches.length, beforeThird);
  items.push({ id: "third-user", turn_id: "same-turn", type: "message", role: "user", content: [{ type: "input_text", text: "Now summarize" }] });
  await worker.tick();
  const fourth = f.store.list("feishu_inbound").find((row) => row.message_id === "fourth");
  f.store.put("feishu_inbound", { ...fourth, next_at: 0 });
  await worker.tick();
  assert.equal(f.sent.length, 4);
  assert.doesNotMatch(f.sent[3].data.content, /CURRENT EXCHANGE|OLD/);
});


test("question updates retry lost acknowledgements and replace only unavailable cards", async (t) => {
  const f = await feishu(t);
  let waiting = false, failure, question = "Choose a purpose", patches = 0;
  f.channel.rawClient.im.message.patch = async () => {
    patches++;
    if (failure === "lost") throw new Error("lost update ACK");
    if (failure === "deleted") return { code: 230110 };
    return { code: 0 };
  };
  const core = async (path, method, ...rest) => {
    if (method === "GET" && path.includes("/turns")) return { data: [{ id: "turn", status: "in_progress" }], has_more: false };
    if (method === "GET" && path.includes("/items")) return { data: [], has_more: false };
    if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) return { id: path.split("/")[4], status: waiting ? "requires_action" : "in_progress", required_actions: waiting ? [{ type: "function_call", name: "ask_user", call_id: "question", arguments: { question } }] : [] };
    return f.core(path, method, ...rest);
  };
  const worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  waiting = true; failure = "lost";
  await worker.tick();
  assert.equal(f.sent.length, 1, "a lost PATCH response must not duplicate the question");
  const attempted = patches;
  failure = undefined;
  await worker.tick();
  assert.equal(patches, attempted + 1);
  assert.equal(f.sent.length, 1);
  for (const value of ["Updated purpose", "Final purpose"]) {
    question = value; failure = "deleted";
    await worker.tick();
    assert.match(f.sent.at(-1).data.content, new RegExp(value));
  }
  assert.equal(f.sent.length, 3, "each confirmed deleted card gets a distinct replacement");
});


test("an expired uncertain question send is never replaced with a new identity", async (t) => {
  const f = await feishu(t);
  let sends = 0;
  f.channel.send = async () => { sends++; throw new Error("accepted but ACK lost"); };
  const core = async (path, method, ...rest) => {
    if (method === "GET" && path.includes("/turns")) return { data: [{ id: "turn", status: "in_progress" }], has_more: false };
    if (method === "GET" && path.includes("/items")) return { data: [], has_more: false };
    if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) return { id: path.split("/")[4], status: "requires_action", required_actions: [{ type: "function_call", name: "ask_user", call_id: "question", arguments: { question: "Choose a purpose" } }] };
    return f.core(path, method, ...rest);
  };
  const worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  const pending = f.store.list("feishu_outbound").find((row) => row.state === "queued");
  f.store.put("feishu_outbound", { ...pending, first_attempt_at: Date.now() - 56 * 60000, next_at: 0 });
  await worker.tick();
  await worker.tick();
  await worker.tick();
  assert.equal(sends, 1);
  assert.equal(f.store.list("feishu_outbound").length, 1);
  assert.equal(f.store.get("feishu_outbound", pending.id).state, "failed");
});


test("answering one of multiple questions leaves the others visible", async (t) => {
  const f = await feishu(t);
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${botId}`, { ...config, direct: true });
  const items = ["q1", "q2"].map((call_id) => ({ id: call_id, turn_id: "turn", type: "function_call", name: "ask_user", call_id }));
  const actions = ["q1", "q2"].map((call_id) => ({ type: "function_call", name: "ask_user", call_id, turn_id: "turn", arguments: { question: `Question ${call_id}` } }));
  const core = async (path, method, ...rest) => {
    if (method === "GET" && path.includes("/turns")) return { data: [{ id: "turn", status: "in_progress" }], has_more: false };
    if (method === "GET" && path.includes("/items")) return { data: items, has_more: false };
    if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) return { id: path.split("/")[4], status: "requires_action", required_actions: actions };
    return f.core(path, method, ...rest);
  };
  const worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  const dm = { ...f.message, chatType: "p2p", mentionedBot: false };
  worker.receive("cli_0123456789abcdef", dm);
  await worker.tick();
  worker.receive("cli_0123456789abcdef", { ...dm, messageId: "answer-first", content: "/answer q1 yes" });
  await worker.tick();
  assert.equal(f.sent.length, 2);
  assert.match(f.sent[1].data.content, /Question q2/);
  assert.doesNotMatch(f.sent[1].data.content, /Question q1/);
  worker.receive("cli_0123456789abcdef", { ...dm, messageId: "answer-second", content: "/answer q2 yes" });
  await worker.tick();
  assert.equal(f.sent.length, 3);
  assert.doesNotMatch(f.sent[2].data.content, /Question q1|Question q2/);
  const restarted = feishuWorker(f.store, core, f.api, async () => f.channel);
  await restarted.tick();
  assert.equal(f.sent.length, 3);
});


test("completed-turn cursors release answered questions before the next turn", async (t) => {
  const f = await feishu(t);
  const items = [{ id: "q1", turn_id: "t1", type: "function_call", name: "ask_user", call_id: "q1" }];
  const turns = [{ id: "t1", status: "in_progress" }];
  let actions = [{ type: "function_call", name: "ask_user", call_id: "q1", turn_id: "t1", arguments: { question: "Continue?" } }];
  const patches = [];
  f.channel.edit = async (_id, payload) => patches.push(payload);
  const core = async (path, method, ...rest) => {
    if (method === "GET" && (path.includes("/items") || path.includes("/turns"))) {
      const rows = path.includes("/items") ? items : turns;
      const after = new URL(path, "https://example.test").searchParams.get("after");
      return { data: after ? rows.slice(rows.findIndex((row) => row.id === after) + 1) : rows, has_more: false };
    }
    if (method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(path)) return { id: path.split("/")[4], status: actions.length ? "requires_action" : "in_progress", required_actions: actions };
    return f.core(path, method, ...rest);
  };
  const worker = feishuWorker(f.store, core, f.api, async () => f.channel);
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  worker.receive("cli_0123456789abcdef", { ...f.message, messageId: "answer", rootId: f.message.messageId, content: "yes" });
  await worker.tick();
  items.push(
    { id: "o1", turn_id: "t1", type: "function_call_output", call_id: "q1", output: "yes" },
    { id: "a1", turn_id: "t1", type: "message", role: "assistant", content: [{ type: "output_text", text: "FIRST RESULT" }] },
    { id: "u2", turn_id: "t2", type: "message", role: "user", content: [{ type: "input_text", text: "Next" }] },
  );
  turns[0].status = "completed";
  turns.push({ id: "t2", status: "in_progress" });
  actions = [];
  await worker.tick();
  items.push({ id: "a2", turn_id: "t2", type: "message", role: "assistant", content: [{ type: "output_text", text: "SECOND RESULT" }] });
  turns[1].status = "completed";
  await worker.tick();
  assert.ok(patches.some((payload) => payload.includes("SECOND RESULT")) || f.sent.some((row) => row.data.content.includes("SECOND RESULT")));
});

test("a running card shows the latest four steps and folds the earlier ones", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const items = [1, 2, 3, 4].flatMap((i) => [
    { type: "message", role: "assistant", content: [{ type: "output_text", text: `说明 ${i}` }] },
    { type: "command_execution", command: `step ${i}`, duration_ms: 1000, status: i === 4 ? "in_progress" : "completed" },
  ]);
  const card = JSON.parse(formatTurnCard("in_progress", items));
  const [earlier, rule, ...recent] = card.body.elements;
  const label = (row) => row.tag === "markdown" ? row.content : row.header.title.content.match(/(step \d)/)[1];
  assert.equal(card.header.title.content, "处理中 · 正在运行 · 8 步");
  assert.equal(earlier.tag, "collapsible_panel");
  assert.equal(earlier.expanded, false);
  assert.match(earlier.header.title.content, /更早的 4 步/);
  assert.equal(earlier.background_color, "grey-50", "earlier steps sit in a tinted block");
  assert.equal(rule.tag, "hr", "a rule separates them from the latest steps");
  assert.deepEqual(earlier.elements.map(label), ["说明 1", "step 1", "说明 2", "step 2"]);
  assert.deepEqual(recent.map(label), ["说明 3", "step 3", "说明 4", "step 4"]);
  assert.match(recent.at(-1).header.title.content, /进行中/);
});

test("thinking and tool rows stay on one line while commentary keeps its lines", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const long = "先确认已有的模型和 Skill，再创建环境，然后检查飞书机器人的绑定和连接状态是否正常，最后试运行";
  const card = JSON.parse(formatTurnCard("in_progress", [
    { type: "reasoning", summary: [{ text: long }] },
    { type: "command_execution", command: `for i in 1 2 3 4; do sleep 30; node parsar.mjs GET /app/runtimes/${"x".repeat(80)}/trial; done`, output: "ok", duration_ms: 128700, status: "completed" },
    { type: "message", role: "assistant", content: [{ type: "output_text", text: "第一行说明。\n第二行说明。" }] },
  ]));
  const columns = (text) => [...text.replace(/<[^>]*>/g, "")].reduce((sum, char) => {
    const code = char.codePointAt(0);
    return sum + (code >= 0x1100 && !(code >= 0x2000 && code <= 0x206f) ? 2 : 1);
  }, 0);
  const [thought, run, said] = card.body.elements;
  for (const row of [thought, run]) assert.ok(columns(row.header.title.content) <= 52, row.header.title.content);
  assert.match(run.header.title.content, /128\.7s<\/font>$/, "the state stays on the row");
  assert.equal(said.content, "第一行说明。\n第二行说明。");
});

test("no Markdown image reaches a card, since Feishu rejects the whole card for an invalid image key", async () => {
  const { formatTurnCard } = await import("../server/feishu.mjs");
  const qr = "![飞书授权二维码](/app/feishu/provision/1a77dd3d-124b-44b2-8d5d-3d75f1c72e23/qrcode)";
  const items = [
    { type: "message", role: "user", content: [{ type: "input_text", text: "给我链接吧" }] },
    { type: "reasoning", summary: [{ text: `Present the link, then ${qr}` }] },
    { type: "message", role: "assistant", phase: "commentary", content: [{ type: "output_text", text: "图表如下 ![chart](https://example.com/chart.png)" }] },
    { type: "command_execution", command: "node parsar.mjs POST /app/feishu/provision/x", output: qr, status: "completed" },
    { type: "message", role: "assistant", content: [{ type: "output_text", text: `链接：https://open.feishu.cn/page/launcher?user_code=AB-12\n\n${qr}` }] },
  ];
  for (const status of ["in_progress", "completed"]) {
    const card = formatTurnCard(status, items);
    const markdown = JSON.stringify(JSON.parse(card).body, (key, value) => key === "text" && value?.tag === "plain_text" ? undefined : value);
    assert.doesNotMatch(markdown, /!\[/, `${status}: Markdown images must not reach the card`);
  }
  const done = JSON.parse(formatTurnCard("completed", items)).body.elements[0].content;
  assert.match(done, /user_code=AB-12/, "the link the Agent gave stays in the answer");
  assert.match(done, /授权二维码见图片消息/);
});
