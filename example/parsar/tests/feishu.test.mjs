import { test } from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { openStore, AppError } from "../server/store.mjs";
import { productAPI } from "../server/product.mjs";
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
  const api = productAPI(store, core);
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
  const profile = await put("profiles", {
    name: "Codex",
    harness: "codex",
    model_id: model.id,
  });
  const agent = await put("agents", {
    name: "Engineer",
    profile_id: profile.id,
    runtime_id: runtime.id,
    instructions: "Work carefully",
    skill_ids: [],
    mcp_ids: [],
  });
  return {
    store,
    core,
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
    JSON.parse(f.sent[0].data.content).header.title.content,
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
    content: "/cancel",
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
    content: "/cancel",
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
    content: "/cancel",
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
    content: "/cancel",
  });
  await w.tick();
  assert.equal(f.store.list("sessions").length, 1);
  assert.ok(
    f.calls.some(
      (call) => call.body?.events?.[0].type === "agent.session.input.cancel",
    ),
  );
});

test("capability versions freeze into dispatch and retired capabilities cannot start new work", async (t) => {
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
  assert.match(f.calls[0].body.agent.instructions, /Always verify/);
  assert.doesNotMatch(f.calls[0].body.agent.instructions, /Updated rules/);
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
  const model = f.store.get("models", f.store.get("profiles", f.agent.profile_id).model_id);
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
  const model = f.store.get("models", f.store.get("profiles", f.agent.profile_id).model_id),
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

test("Feishu group reset explains how to start a fresh conversation", async (t) => {
  const f = await feishu(t),
    worker = f.create();
  worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  const run = f.store.list("sessions")[0];
  worker.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_reset",
    rootId: "om_first",
    content: "/new",
  });
  await worker.tick();
  assert.ok(
    f.store
      .list("feishu_outbound")
      .some((row) => row.payload.includes("群聊请另开话题")),
  );
  assert.equal(f.store.list("sessions")[0].id, run.id);
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
  });
  const profile = f.store.get("profiles", f.agent.profile_id);
  await f.api("PUT", `/app/profiles/${profile.id}`, {
    ...profile,
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
  assert.equal(firstCard.header.title.content, "处理中");
  assert.equal(firstCard.header.template, "blue");
  const [current, thinkingPanel, stepsPanel] = firstCard.body.elements;
  assert.match(current.content, /正在思考下一步/);
  assert.match(current.content, /已完成 1 步/);
  assert.doesNotMatch(current.content, /先读取飞书技能文档/);
  assert.equal(thinkingPanel.tag, "collapsible_panel");
  assert.equal(thinkingPanel.expanded, false);
  assert.match(thinkingPanel.header.title.content, /思考过程/);
  assert.match(thinkingPanel.elements[0].content, /先读取飞书技能文档/);
  assert.match(stepsPanel.header.title.content, /执行记录 · 1 步/);
  assert.match(
    stepsPanel.elements[0].content,
    /✓<\/font> 运行命令 `cat \/workspace\/\.agents\/skills\/lark-shared\/SKILL\.md`/,
  );

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
  assert.equal(patchedCard.header.title.content, "已完成");
  assert.equal(patchedCard.header.template, "green");
  const [answer, thoughts, record] = patchedCard.body.elements;
  assert.equal(answer.content, "你好！我是飞书工作助手。");
  assert.match(thoughts.elements[0].content, /先读取飞书技能文档/);
  assert.match(record.header.title.content, /执行记录 · 1 步/);
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

test("failed turns turn the card red, keep the error visible and mark failed steps", async () => {
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
  assert.equal(card.header.template, "red");
  assert.equal(card.header.title.content, "执行失败");
  assert.match(card.body.elements[0].content, /<font color='red'>permission denied<\/font>/);
  assert.match(card.body.elements[2].elements[0].content, /<font color='red'>✗<\/font> 运行命令/);
  const quiet = JSON.parse(
    formatTurnCard("completed", [
      { type: "message", role: "assistant", content: [{ type: "output_text", text: "答案" }] },
    ]),
  );
  assert.deepEqual(
    quiet.body.elements.map((element) => element.tag),
    ["markdown", "collapsible_panel", "collapsible_panel"],
  );
  assert.match(quiet.body.elements[2].header.title.content, /执行记录 · 0 步/);
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
  assert.match(
    card.body.elements[2].elements[0].content,
    /✓<\/font> 搜索网页 `OpenAgentCore release`/,
  );
});
