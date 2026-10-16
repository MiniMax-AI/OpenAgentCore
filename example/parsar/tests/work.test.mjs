import { test } from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { openStore, AppError } from "../server/store.mjs";
import { productAPI } from "../server/product.mjs";
import { feishuOperations } from "../server/feishu.mjs";
import { feishuWorker as workerForBot } from "../server/feishu.mjs";
import { scheduleWorker } from "../server/schedules.mjs";

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
  const agent = await put("agents", {
    name: "Engineer",
    model_id: model.id,
    harness: "codex",
    instructions: "Work carefully",
    skill_ids: [],
    mcp_ids: [],
  });
  const runtime = await put("runtimes", {
    name: "Text",
    environment: "openai_hosted",
  });
  const project = await put("projects", {
    name: "Website",
    description: "Accessible UI",
    repository: "https://github.com/example/site",
  });
  const task = await put("tasks", {
    name: "Review login",
    description: "Check keyboard navigation",
    project_id: project.id,
    agent_id: agent.id,
    runtime_id: runtime.id,
    status: "backlog",
    priority: "high",
  });
  return {
    store,
    core,
    api,
    put,
    agent,
    runtime,
    project,
    task,
    calls,
    receipts,
    resources,
    setLose: () => (lose = true),
    setReject: (value) => (reject = value),
  };
}
test("task dispatch freezes public request, recovers lost response, and guards pending/running/provisioning", async (t) => {
  const f = await setup(t),
    runId = randomUUID();
  f.setLose();
  await assert.rejects(
    f.api("PUT", `/app/tasks/${f.task.id}/runs/${runId}`, {
      revision: f.task.revision,
    }),
    /lost/,
  );
  const pending = await f.api("GET", `/app/tasks/${f.task.id}/runs`);
  assert.equal(pending.length, 1);
  assert.equal(pending[0].request, undefined);
  assert.match(f.calls[0].body.input, /Accessible UI/);
  assert.equal(f.calls[0].body.agent.multi_agent.enabled, false);
  const task = f.store.get("tasks", f.task.id);
  await assert.rejects(
    f.api("PUT", `/app/tasks/${task.id}/runs/${randomUUID()}`, {
      revision: task.revision,
    }),
    /尚未确认/,
  );
  const restarted = productAPI(f.store, f.core);
  const run = await restarted("PUT", `/app/tasks/${task.id}/runs/${runId}`, {});
  assert.equal(f.receipts.size, 1);
  assert.equal(run.request, undefined);
  assert.deepEqual(f.calls[0].body, f.calls[1].body);
  const state = f.resources.get(run.core_session_id);
  state.turns = [];
  await assert.rejects(
    f.put("tasks", { ...task, status: "done" }, task.id),
    /准备/,
  );
  state.status = "in_progress";
  state.turns = [{ id: randomUUID(), status: "in_progress" }];
  await assert.rejects(
    f.api("PUT", `/app/tasks/${task.id}/runs/${randomUUID()}`, {
      revision: task.revision,
    }),
    /执行/,
  );
  state.status = "idle";
  state.turns[0].status = "completed";
  const done = await f.put("tasks", { ...task, status: "done" }, task.id);
  await assert.rejects(
    f.api("PUT", `/app/tasks/${task.id}/runs/${randomUUID()}`, {
      revision: done.revision,
    }),
    /重新打开/,
  );
  await assert.rejects(
    f.api("DELETE", `/app/tasks/${task.id}`, done),
    /已有执行记录/,
  );
});
test("projects, comments and schedules maintain references and optimistic revisions", async (t) => {
  const f = await setup(t),
    comment = randomUUID();
  const path = `/app/tasks/${f.task.id}/activity/${comment}`;
  await f.api("PUT", path, { message: "Please verify focus order" });
  await f.api("PUT", path, { message: "Please verify focus order" });
  assert.equal(
    (await f.api("GET", `/app/tasks/${f.task.id}/activity`)).filter(
      (x) => x.type === "comment",
    ).length,
    1,
  );
  await assert.rejects(f.api("PUT", path, { message: "different" }), /标识/);
  await f.put("tasks", { ...f.task, name: "Changed" }, f.task.id);
  await assert.rejects(
    f.put("tasks", { ...f.task, name: "Stale" }, f.task.id),
    /已更新/,
  );
  await assert.rejects(
    f.api("DELETE", `/app/projects/${f.project.id}`, f.project),
    /仍有任务/,
  );
  await assert.rejects(
    f.api("DELETE", `/app/agents/${f.agent.id}`),
    /仍被引用/,
  );
  await assert.rejects(
    f.put(
      "projects",
      { ...f.project, repository: "https://secret@github.com/x" },
      f.project.id,
    ),
    /无凭据/,
  );
});
test("concurrent dispatches cannot create two Sessions", async (t) => {
  const f = await setup(t);
  const results = await Promise.allSettled(
    [randomUUID(), randomUUID()].map((id) =>
      f.api("PUT", `/app/tasks/${f.task.id}/runs/${id}`, {
        revision: f.task.revision,
      }),
    ),
  );
  assert.equal(results.filter((x) => x.status === "fulfilled").length, 1);
  assert.equal(f.receipts.size, 1);
});
test("definite dispatch rejection removes pending request and leaves a retryable task", async (t) => {
  const f = await setup(t);
  f.setReject(true);
  await assert.rejects(
    f.api("PUT", `/app/tasks/${f.task.id}/runs/${randomUUID()}`, {
      revision: f.task.revision,
    }),
    /invalid/,
  );
  assert.equal(f.store.list("sessions").length, 0);
  assert.ok(
    f.store.list("activity").some((x) => x.type === "dispatch_rejected"),
  );
  f.setReject(false);
  const task = f.store.get("tasks", f.task.id);
  await f.api("PUT", `/app/tasks/${task.id}/runs/${randomUUID()}`, {
    revision: task.revision,
  });
  assert.equal(f.receipts.size, 1);
});
async function feishu(t) {
  const f = await setup(t),
    sent = [];
  await f.api("PUT", `/app/feishu/bots/${botId}`, {
    revision: 0,
    app_id: "cli_0123456789abcdef",
    app_secret: "never-return-me",
    enabled: true,
    routes: [
      {
        chat_id: "oc_test",
        agent_id: f.agent.id,
        runtime_id: f.runtime.id,
        project_id: f.project.id,
      },
    ],
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
    { chatId: "oc_other" },
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
  assert.equal(f.sent.length, 2);
  assert.ok(f.sent.every((x) => x.data.reply_in_thread));
  await restarted.tick();
  assert.equal(f.sent.length, 3);
  assert.equal(f.sent[2].data.msg_type, "text");
  assert.match(
    JSON.parse(f.sent[2].data.content).text,
    /<at user_id="ou_user">/,
  );
  await restarted.tick();
  assert.equal(f.sent.length, 3);
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
  assert.equal(f.calls.filter((x) => x.path.endsWith("/events")).length, 1);
  assert.equal(
    f.calls.find((x) => x.path.endsWith("/events")).body.events[0].input[0]
      .content[0].text,
    "Also check mobile",
  );
  assert.ok(
    f.store
      .list("activity")
      .some((x) => x.task_id === thread.task_id && x.actor === "ou_user"),
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
test("scheduled occurrences survive lost responses and do not overlap active prior work", async (t) => {
  const f = await setup(t),
    id = randomUUID();
  let schedule = await f.api("PUT", `/app/schedules/${id}`, {
    name: "Daily review",
    task_id: f.task.id,
    interval_minutes: 5,
    enabled: true,
  });
  const worker = scheduleWorker(f.store, f.api, f.core);
  f.setLose();
  await worker.tick(schedule.next_at);
  schedule = f.store.get("schedules", id);
  assert.equal(schedule.enabled, true);
  assert.ok(schedule.pending);
  const occurrence = schedule.pending.task_id;
  await worker.tick(schedule.next_at + 1);
  assert.equal(f.receipts.size, 1);
  schedule = f.store.get("schedules", id);
  assert.equal(schedule.last_task_id, occurrence);
  const run = f.store.list("sessions")[0];
  f.resources.get(run.core_session_id).status = "in_progress";
  await worker.tick(schedule.next_at);
  assert.equal(f.receipts.size, 1);
  assert.match(f.store.get("schedules", id).last_error, /跳过/);
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
test("uncertain follow-up retains its event when task state changes, and history resumes at its cursor", async (t) => {
  const f = await feishu(t),
    w = f.create();
  w.receive("cli_0123456789abcdef", f.message);
  await w.tick();
  const thread = f.store.list("feishu_threads")[0],
    task = f.store.get("tasks", thread.task_id);
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_follow",
    rootId: "om_first",
    content: "Follow up",
  });
  f.setLose();
  await w.tick();
  const row = f.store.list("feishu_inbound")[0];
  f.store.put("tasks", { ...task, status: "done" });
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
test("schedule read failures keep overlap identity, and invalid edited templates pause before freezing", async (t) => {
  const f = await setup(t),
    id = randomUUID();
  let plan = await f.api("PUT", `/app/schedules/${id}`, {
    name: "Plan",
    task_id: f.task.id,
    interval_minutes: 5,
    enabled: true,
  });
  await scheduleWorker(f.store, f.api, f.core).tick(plan.next_at);
  plan = f.store.get("schedules", id);
  await scheduleWorker(f.store, f.api, async () => {
    throw new Error("offline");
  }).tick(plan.next_at);
  const retry = f.store.get("schedules", id);
  assert.equal(retry.enabled, true);
  assert.equal(retry.last_task_id, plan.last_task_id);
  f.store.put("tasks", { ...f.task, agent_id: "" });
  await scheduleWorker(f.store, f.api, f.core).tick(retry.next_at);
  const invalid = f.store.get("schedules", id);
  assert.equal(invalid.enabled, false);
  assert.equal(invalid.pending, undefined);
  assert.equal(invalid.last_task_id, plan.last_task_id);
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

test("scheduled and Feishu retries adopt a manual recovery after a definite rejection", async (t) => {
  const f = await feishu(t),
    planId = randomUUID();
  let plan = await f.api("PUT", `/app/schedules/${planId}`, {
    name: "Plan",
    task_id: f.task.id,
    interval_minutes: 5,
    enabled: true,
  });
  const scheduler = scheduleWorker(f.store, f.api, f.core);
  f.setReject(true);
  await scheduler.tick(plan.next_at);
  plan = f.store.get("schedules", planId);
  const task = f.store.get("tasks", plan.pending.task_id);
  f.setReject(false);
  const manual = await f.api(
    "PUT",
    `/app/tasks/${task.id}/runs/${randomUUID()}`,
    { revision: task.revision },
  );
  await f.api("PUT", `/app/schedules/${planId}`, { ...plan, enabled: true });
  await scheduler.tick(plan.next_at);
  assert.equal(f.receipts.size, 1);
  assert.equal(f.store.list("sessions")[0].id, manual.id);
  const w = f.create();
  w.receive("cli_0123456789abcdef", f.message);
  f.setReject(true);
  await w.tick();
  const thread = f.store.list("feishu_threads")[0],
    incoming = f.store.list("feishu_inbound")[0];
  assert.equal(incoming.state, "failed");
  f.setReject(false);
  const feishuTask = f.store.get("tasks", thread.task_id),
    recovered = await f.api(
      "PUT",
      `/app/tasks/${thread.task_id}/runs/${randomUUID()}`,
      { revision: feishuTask.revision },
    );
  await f.api("PUT", `/app/feishu/deliveries/inbound/${incoming.id}`, {});
  await w.tick();
  assert.equal(f.receipts.size, 2);
  assert.equal(
    f.store.get("feishu_threads", thread.id).session_id,
    recovered.id,
  );
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
test("schedule HTTP 503 during the settle check retries without losing overlap protection", async (t) => {
  const f = await setup(t),
    id = randomUUID();
  let plan = await f.api("PUT", `/app/schedules/${id}`, {
    name: "Plan",
    task_id: f.task.id,
    interval_minutes: 5,
    enabled: true,
  });
  await scheduleWorker(f.store, f.api, f.core).tick(plan.next_at);
  plan = f.store.get("schedules", id);
  await scheduleWorker(f.store, f.api, async () => {
    throw new AppError(503, "Unavailable");
  }).tick(plan.next_at);
  const retry = f.store.get("schedules", id);
  assert.equal(retry.enabled, true);
  assert.equal(retry.last_task_id, plan.last_task_id);
  assert.equal(retry.next_at, plan.next_at + 15000);
  assert.equal(f.receipts.size, 1);
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

test("shared Feishu selection is durable, sender-scoped, and applies only to new task threads", async (t) => {
  const f = await feishu(t),
    config = await f.api("GET", `/app/feishu/bots/${botId}`);
  const other = await f.put("agents", {
    ...f.agent,
    id: undefined,
    revision: undefined,
    name: "Designer",
  });
  await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...config,
    routes: config.routes.map((route) => ({
      ...route,
      allow_agent_selection: true,
    })),
  });
  const w = f.create();
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_list",
    content: "/list",
  });
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_select",
    content: `/select ${other.id}`,
  });
  w.receive("cli_0123456789abcdef", { ...f.message, messageId: "om_work" });
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_colleague",
    senderId: "ou_colleague",
  });
  await w.tick();
  const mine = f.store
      .list("feishu_threads")
      .find((row) => row.root_id === "om_work"),
    colleague = f.store
      .list("feishu_threads")
      .find((row) => row.root_id === "om_colleague");
  assert.equal(f.store.get("tasks", mine.task_id).agent_id, other.id);
  assert.equal(f.store.get("tasks", colleague.task_id).agent_id, f.agent.id);
  const deliveries = await f.api("GET", "/app/feishu/deliveries");
  const command = f.store
    .list("feishu_inbound")
    .find((row) => row.message_id === "om_select");
  assert.equal(
    deliveries.find((row) => row.id === command.id).task_id,
    undefined,
  );
  const restarted = f.create();
  restarted.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_reset",
    rootId: "om_work",
    content: `/select ${f.agent.id}`,
  });
  await restarted.tick();
  restarted.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_followup",
    rootId: "om_work",
    content: "Continue this task",
  });
  await restarted.tick();
  assert.equal(f.store.get("feishu_threads", mine.id).agent_id, other.id);
  assert.equal(f.store.list("sessions").length, 2);
  const current = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...current,
    routes: current.routes.map((route) => ({
      ...route,
      allow_agent_selection: false,
    })),
  });
  restarted.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_denied",
    content: `/select ${other.id}`,
  });
  await restarted.tick();
  assert.equal(f.store.list("feishu_inbound")[0].state, "rejected");
});

test("automatic Feishu dispatch revalidates a runtime edited after bot setup", async (t) => {
  const f = await feishu(t),
    w = f.create();
  f.store.put("runtimes", { ...f.runtime, environment: "self_hosted" });
  w.receive("cli_0123456789abcdef", f.message);
  await w.tick();
  assert.equal(f.receipts.size, 0);
  const receipt = f.store.list("feishu_inbound")[0];
  assert.equal(receipt.state, "failed");
  assert.match(receipt.error, /托管环境模板/);
});

test("a new schedule ignores unrelated standalone Sessions when checking overlap", async (t) => {
  const f = await setup(t),
    id = randomUUID();
  f.store.put("sessions", {
    id: randomUUID(),
    agent_id: f.agent.id,
    runtime_id: f.runtime.id,
    core_session_id: "unrelated-standalone",
  });
  const plan = await f.api("PUT", `/app/schedules/${id}`, {
    name: "Plan",
    task_id: f.task.id,
    interval_minutes: 5,
    enabled: true,
  });
  await scheduleWorker(f.store, f.api, f.core).tick(plan.next_at);
  assert.equal(f.receipts.size, 1);
  assert.equal(
    f.calls.some((call) => call.path.includes("unrelated-standalone")),
    false,
  );
  assert.ok(f.store.get("schedules", id).last_task_id);
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
test("Feishu can withdraw an unstarted task and recover a lost creation before cancelling", async (t) => {
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

test("automation can be disabled after runtime eligibility changes but cannot resume", async (t) => {
  const f = await feishu(t),
    id = randomUUID();
  const plan = await f.api("PUT", `/app/schedules/${id}`, {
    name: "Plan",
    task_id: f.task.id,
    interval_minutes: 5,
    enabled: true,
  });
  f.store.put("runtimes", { ...f.runtime, environment: "self_hosted" });
  const paused = await f.api("PUT", `/app/schedules/${id}`, {
    ...plan,
    enabled: false,
  });
  assert.equal(paused.enabled, false);
  await assert.rejects(
    f.api("PUT", `/app/schedules/${id}`, {
      ...paused,
      enabled: true,
    }),
    { status: 400 },
  );
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  const disabled = await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...config,
    enabled: false,
  });
  assert.equal(disabled.enabled, false);
  assert.deepEqual(disabled.routes, config.routes);
  await assert.rejects(
    f.api("PUT", `/app/feishu/bots/${botId}`, {
      ...disabled,
      enabled: true,
    }),
    { status: 400 },
  );
  assert.equal(f.store.get("feishu_bots", botId).enabled, false);
});

test("multiple Feishu bots route independently and private chats require their own route", async (t) => {
  const f = await feishu(t),
    secondId = randomUUID();
  const first = await f.api("GET", `/app/feishu/bots/${botId}`);
  await f.api("PUT", `/app/feishu/bots/${secondId}`, {
    ...first,
    revision: 0,
    name: "Dedicated",
    app_id: "cli_fedcba9876543210",
    app_secret: "second-secret",
    routes: [{ ...first.routes[0], chat_id: "p2p" }],
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
  assert.ok(JSON.parse(f.sent[0].data.content).elements.length);
  await assert.rejects(
    f.api("PUT", `/app/feishu/bots/${randomUUID()}`, {
      ...first,
      revision: 0,
      app_secret: "secret",
    }),
    { status: 409 },
  );
});

test("Cron plans honor timezone, manual runs on paused plans are idempotent and leave history", async (t) => {
  const { nextOccurrence } = await import("../server/schedules.mjs");
  assert.equal(
    new Date(
      nextOccurrence(
        { cron: "0 9 * * 1-5", timezone: "Asia/Shanghai" },
        Date.parse("2026-10-16T02:00:00Z"),
      ),
    ).toISOString(),
    "2026-10-19T01:00:00.000Z",
  );
  const f = await setup(t),
    id = randomUUID(),
    request_id = randomUUID();
  await f.api("PUT", `/app/schedules/${id}`, {
    name: "Daily",
    task_id: f.task.id,
    cron: "0 9 * * *",
    timezone: "Asia/Shanghai",
    enabled: false,
  });
  await f.api("POST", `/app/schedules/${id}/run`, { request_id });
  await scheduleWorker(f.store, f.api, f.core).tick(Date.now() + 1);
  assert.equal(f.receipts.size, 1);
  await f.api("POST", `/app/schedules/${id}/run`, { request_id });
  await scheduleWorker(f.store, f.api, f.core).tick(Date.now() + 86400000);
  assert.equal(f.receipts.size, 1);
  const history = await f.api("GET", `/app/schedules/${id}/history`);
  assert.equal(history.length, 1);
  assert.equal(f.store.get("schedules", id).enabled, false);
  await f.api("POST", `/app/schedules/${id}/run`, { request_id: randomUUID() });
  await scheduleWorker(f.store, f.api, f.core).tick(Date.now() + 1);
  assert.equal(f.receipts.size, 2);
  await f.api("POST", `/app/schedules/${id}/run`, { request_id });
  await scheduleWorker(f.store, f.api, f.core).tick(Date.now() + 2);
  assert.equal(
    f.receipts.size,
    2,
    "an old request remains idempotent after a newer execution",
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
      runtime_id: f.runtime.id,
    },
    f.agent.id,
  );
  await f.api("PUT", `/app/capabilities/${id}`, {
    ...cap,
    content: "Updated rules",
    default_version: "",
  });
  const run = await f.api(
    "PUT",
    `/app/tasks/${f.task.id}/runs/${randomUUID()}`,
    { revision: f.task.revision },
  );
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
      runtime_id: f.runtime.id,
      input: "hi",
    }),
    { status: 400 },
  );
  assert.ok(run.core_session_id);
  assert.ok((await f.api("GET", "/app/audit")).length > 0);
});

test("scheduled Feishu delivery anchors one durable thread and completes its progress card", async (t) => {
  const f = await feishu(t),
    scheduleId = randomUUID(),
    created = [],
    patches = [];
  f.channel.rawClient.im.message.create = async (payload) => {
    created.push(payload);
    return { code: 0, data: { message_id: "om_scheduled" } };
  };
  f.channel.rawClient.im.message.patch = async (payload) => {
    patches.push(payload);
    return { code: 0 };
  };
  await f.api("PUT", `/app/schedules/${scheduleId}`, {
    name: "Digest",
    task_id: f.task.id,
    interval_minutes: 5,
    enabled: true,
    delivery: { bot_id: botId, chat_id: "oc_test" },
  });
  await scheduleWorker(f.store, f.api, f.core).tick(Date.now() + 300001);
  const worker = f.create();
  await worker.tick();
  assert.equal(created.length, 1);
  assert.equal(created[0].data.receive_id, "oc_test");
  assert.equal(f.store.list("feishu_threads")[0].root_id, "om_scheduled");
  assert.equal(f.sent.at(-1).path.message_id, "om_scheduled");
  assert.match(patches[0].data.content, /执行结束/);
  await worker.tick();
  await f.create().tick();
  assert.equal(
    created.length,
    1,
    "worker restart reuses the scheduled thread and outbound receipt",
  );
  assert.equal(patches.length, 1);
  const current = await f.api("GET", `/app/schedules/${scheduleId}`);
  await assert.rejects(
    f.api("PUT", `/app/schedules/${scheduleId}`, {
      ...current,
      delivery: { bot_id: botId, chat_id: "oc_unlisted" },
    }),
    { status: 400 },
  );
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
    runtime_id: runtime.id,
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

test("Feishu optional history is bounded and permission failures are visible without dropping the message", async () => {
  const { feishuContextReader } = await import("../server/feishu-context.mjs");
  let requests = 0;
  const reader = feishuContextReader(
    {
      im: {
        message: {
          list: async ({ params }) => {
            requests++;
            assert.equal(params.page_size, 20);
            return { code: 99991672 };
          },
        },
      },
    },
    { openId: "ou_bot" },
    true,
  );
  const row = {
    message_id: "om_current",
    chat_id: "oc_test",
    chat_type: "group",
    text: "Current task",
    created_at: Date.now(),
  };
  const first = await reader(row, true),
    second = await reader(row, true);
  assert.equal(first.text, "Current task");
  assert.match(first.notice, /历史消息权限/);
  assert.equal(second.notice, first.notice);
  assert.equal(requests, 1);
  const continuation = await reader(row, false);
  assert.equal(continuation.notice, "");
});

test("Agent MCP authenticates a local client and dispatches through the same public Session flow", async (t) => {
  const { createServer } = await import("node:http");
  const { Client } = await import("@modelcontextprotocol/sdk/client/index.js");
  const { StreamableHTTPClientTransport } = await import(
    "@modelcontextprotocol/sdk/client/streamableHttp.js"
  );
  const { createHandler } = await import("../server.mjs");
  const f = await setup(t);
  f.store.put("agents", { ...f.agent, runtime_id: f.runtime.id });
  const config = {
    port: 0,
    target: "https://core.example",
    key: "private-project-key",
  };
  let observationUnavailable = false;
  const server = createServer(
    createHandler(config, {
      store: f.store,
      productHandler: f.api,
      fetchImpl: async (url, options) => {
        if (observationUnavailable)
          throw new Error("temporary observation failure");
        assert.equal(
          options.headers.Authorization,
          "Bearer private-project-key",
        );
        const parsed = new URL(url);
        const data = await f.core(
          `${parsed.pathname}${parsed.search}`,
          options.method,
          options.body ? JSON.parse(options.body) : undefined,
          options.headers["Idempotency-Key"],
        );
        return Response.json(data || {});
      },
    }),
  );
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  config.port = server.address().port;
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  const path = `/app/agents/${f.agent.id}/mcp-token`;
  const issued = await f.api("POST", path, {});
  assert.equal(
    JSON.stringify(await f.api("GET", path)).includes(issued.token),
    false,
  );
  assert.equal(
    JSON.stringify(f.store.get("agent_mcp_tokens", f.agent.id)).includes(
      issued.token,
    ),
    false,
  );
  const url = new URL(
    `http://127.0.0.1:${config.port}/mcp/agents/${f.agent.id}`,
  );
  assert.equal((await fetch(url, { method: "POST", body: "{}" })).status, 401);
  const client = new Client({ name: "parsar-test", version: "1.0.0" });
  t.after(() => client.close());
  await client.connect(
    new StreamableHTTPClientTransport(url, {
      requestInit: { headers: { Authorization: `Bearer ${issued.token}` } },
    }),
  );
  assert.deepEqual(
    (await client.listTools()).tools.map((tool) => tool.name),
    ["ask_agent", "get_run"],
  );
  const started = await client.callTool({
    name: "ask_agent",
    arguments: { prompt: "Review the MCP workflow" },
  });
  assert.equal(started.isError, undefined, JSON.stringify(started));
  const run = started.structuredContent;
  const record = f.store.get("sessions", run.run_id);
  assert.ok(record.core_session_id);
  assert.equal(record.agent_id, f.agent.id);
  const read = await client.callTool({
    name: "get_run",
    arguments: { run_id: run.run_id },
  });
  assert.equal(read.structuredContent.run_id, run.run_id);
  observationUnavailable = true;
  const accepted = await client.callTool({
    name: "ask_agent",
    arguments: { prompt: "Keep my recovery handle" },
  });
  assert.equal(accepted.isError, undefined);
  assert.equal(accepted.structuredContent.status, "unknown");
  const recoveredId = accepted.structuredContent.run_id;
  assert.ok(f.store.get("sessions", recoveredId));
  observationUnavailable = false;
  const recovered = await client.callTool({
    name: "get_run",
    arguments: { run_id: recoveredId },
  });
  assert.equal(recovered.structuredContent.run_id, recoveredId);
  assert.equal(f.store.list("sessions").length, 2);
  const forbidden = await client.callTool({
    name: "get_run",
    arguments: { run_id: randomUUID() },
  });
  assert.equal(forbidden.isError, true);
  await f.api("DELETE", path, {});
  assert.equal(
    (
      await fetch(url, {
        method: "POST",
        headers: { Authorization: `Bearer ${issued.token}` },
        body: "{}",
      })
    ).status,
    401,
  );
});

test("mentioning the assigned Agent starts a task once and subsequent comments steer its public Session", async (t) => {
  const f = await setup(t),
    id = randomUUID();
  const path = `/app/tasks/${f.task.id}/activity/${id}`;
  const comment = {
    message: "Start by reviewing the checkout flow",
    notify_agent: true,
  };
  await f.api("PUT", path, comment);
  await f.api("PUT", path, comment);
  assert.equal(f.store.list("sessions").length, 1);
  assert.match(
    f.calls.find((call) => call.path === "/v1/agents/sessions").body.input,
    /Task comment: Start by reviewing/,
  );
  assert.equal(f.store.get("activity", id).delivery, "sent");
  const next = randomUUID(),
    nextPath = `/app/tasks/${f.task.id}/activity/${next}`;
  await f.api("PUT", nextPath, {
    message: "Also check mobile",
    notify_agent: true,
  });
  await f.api("PUT", nextPath, {
    message: "Also check mobile",
    notify_agent: true,
  });
  const events = f.calls.filter((call) => call.path.endsWith("/events"));
  assert.equal(events.length, 1);
  assert.equal(events[0].key, next);
  assert.equal(events[0].body.events[0].type, "agent.session.input.message");
  assert.equal(
    events[0].body.events[0].input[0].content[0].text,
    "Also check mobile",
  );
  assert.equal(f.store.list("sessions").length, 1);
  await assert.rejects(
    f.api("PUT", nextPath, { message: "Changed", notify_agent: true }),
    /标识已被使用/,
  );
});

test("schedules pause after five observed failed runs and re-enabling resets the failure count", async (t) => {
  const f = await setup(t),
    id = randomUUID();
  let schedule = await f.api("PUT", `/app/schedules/${id}`, {
    name: "Failure policy",
    task_id: f.task.id,
    interval_minutes: 5,
    enabled: true,
  });
  const failedCore = async (path, method, ...args) => {
    const result = await f.core(path, method, ...args);
    if (method === "GET" && path.includes("/turns"))
      return {
        ...result,
        data: result.data.map((turn) => ({ ...turn, status: "failed" })),
      };
    return result;
  };
  const worker = scheduleWorker(f.store, f.api, failedCore);
  for (let count = 0; count < 5; count++) {
    await worker.tick(schedule.next_at);
    schedule = f.store.get("schedules", id);
  }
  assert.equal(f.store.list("schedule_occurrences").length, 5);
  await worker.tick(schedule.next_at);
  schedule = f.store.get("schedules", id);
  assert.equal(schedule.enabled, false);
  assert.equal(schedule.consecutive_failures, 5);
  assert.equal(f.store.list("schedule_occurrences").length, 5);
  await worker.tick(schedule.next_at + 1);
  assert.equal(f.store.get("schedules", id).consecutive_failures, 5);
  const enabled = await f.api("PUT", `/app/schedules/${id}`, {
    ...schedule,
    enabled: true,
  });
  assert.equal(enabled.consecutive_failures, 0);
});

test("an uneditable Feishu progress card does not block final results or other threads", async (t) => {
  const f = await feishu(t);
  let created = 0;
  f.channel.rawClient.im.message.create = async () => ({
    code: 0,
    data: { message_id: `om_scheduled_${++created}` },
  });
  f.channel.rawClient.im.message.patch = async () => {
    throw new Error("message deleted");
  };
  for (let i = 0; i < 2; i++) {
    await f.api("PUT", `/app/schedules/${randomUUID()}`, {
      name: `Digest ${i}`,
      task_id: f.task.id,
      interval_minutes: 5,
      enabled: true,
      delivery: { bot_id: botId, chat_id: "oc_test" },
    });
  }
  await scheduleWorker(f.store, f.api, f.core).tick(Date.now() + 300001);
  await f.create().tick();
  const replies = f.store
    .list("feishu_outbound")
    .filter((row) => row.payload.includes("请验收结果"));
  assert.equal(replies.length, 2);
  assert.ok(replies.every((row) => row.state === "delivered"));
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
          row.payload.includes("请验收结果") && row.state === "delivered",
      ),
  );
});

test("Feishu private informational and rejected commands retain the active conversation", async (t) => {
  const f = await feishu(t);
  const bot = f.store.get("feishu_bots", botId);
  f.store.put("feishu_bots", {
    ...bot,
    routes: [
      { ...bot.routes[0], chat_id: "p2p", allow_agent_selection: false },
    ],
  });
  const worker = f.create();
  for (const [i, content] of [
    "Task one",
    "/list",
    "/select missing",
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

test("Feishu inherits defaults for new topics, freezes existing topics, and validates shared selections", async (t) => {
  const f = await feishu(t);
  const agent = await f.api("PUT", `/app/agents/${f.agent.id}`, {
    ...f.agent,
    runtime_id: f.runtime.id,
  });
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  const inherited = await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...config,
    routes: config.routes.map((route) => ({
      ...route,
      runtime_id: "",
      allow_agent_selection: true,
    })),
  });
  assert.equal(inherited.routes[0].runtime_id, "");
  const w = f.create();
  w.receive("cli_0123456789abcdef", f.message);
  await w.tick();
  const thread = f.store.list("feishu_threads")[0];
  assert.equal(thread.runtime_id, f.runtime.id);
  const other = await f.put("runtimes", {
    name: "Another text environment",
    environment: "openai_hosted",
  });
  await f.api("PUT", `/app/agents/${agent.id}`, {
    ...agent,
    runtime_id: other.id,
  });
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_followup_default",
    rootId: f.message.messageId,
  });
  await w.tick();
  assert.equal(
    f.store.get("feishu_threads", thread.id).runtime_id,
    f.runtime.id,
  );
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_new_default",
  });
  await w.tick();
  assert.equal(
    f.store
      .list("feishu_threads")
      .find((row) => row.root_id === "om_new_default").runtime_id,
    other.id,
  );
  const missing = await f.put("agents", {
    ...f.agent,
    id: undefined,
    revision: undefined,
    name: "No default",
  });
  w.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_select_missing",
    content: `/select ${missing.id}`,
  });
  await w.tick();
  assert.equal(
    f.store
      .list("feishu_inbound")
      .find((row) => row.message_id === "om_select_missing").state,
    "rejected",
  );
  assert.equal(f.store.list("feishu_selections").length, 0);
  await w.stop();
});

test("Session configuration summary is frozen with the creation request and excludes credentials", async (t) => {
  const f = await setup(t);
  const id = randomUUID();
  f.setLose();
  await assert.rejects(
    f.api("PUT", `/app/sessions/${id}`, {
      name: "Snapshot",
      agent_id: f.agent.id,
      runtime_id: f.runtime.id,
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

test("an inherited Feishu route can be disabled after clearing its Agent default", async (t) => {
  const f = await feishu(t);
  const agent = await f.api("PUT", `/app/agents/${f.agent.id}`, {
    ...f.agent,
    runtime_id: f.runtime.id,
  });
  const config = await f.api("GET", `/app/feishu/bots/${botId}`);
  const inherited = await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...config,
    routes: config.routes.map((route) => ({ ...route, runtime_id: "" })),
  });
  await f.api("PUT", `/app/agents/${agent.id}`, { ...agent, runtime_id: "" });
  const disabled = await f.api("PUT", `/app/feishu/bots/${botId}`, {
    ...inherited,
    enabled: false,
  });
  assert.equal(disabled.enabled, false);
  await assert.rejects(
    f.api("PUT", `/app/feishu/bots/${botId}`, { ...disabled, enabled: true }),
    /默认环境/,
  );
});

test("personal-connection Feishu threads reject other senders and cancel waiting authorization without creating a Session", async (t) => {
  const f = await feishu(t),
    worker = f.create();
  await worker.receive("cli_0123456789abcdef", f.message);
  await worker.tick();
  const thread = f.store.list("feishu_threads")[0],
    run = f.store.get("sessions", thread.session_id);
  const requirement = {
    service: "feishu",
    bot_id: botId,
    app_id: "cli_0123456789abcdef",
    scopes: "contact:user.base:readonly",
  };
  f.store.put("sessions", { ...run, connection_requirement: requirement });
  const before = f.calls.filter((c) => c.method === "POST").length;
  await worker.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_foreign",
    rootId: f.message.messageId,
    senderId: "ou_other",
    content: "Read private docs",
  });
  await worker.tick();
  assert.equal(f.calls.filter((c) => c.method === "POST").length, before);
  assert.ok(
    f.store
      .list("feishu_inbound")
      .some(
        (r) =>
          r.message_id === "om_foreign" &&
          r.state === "rejected" &&
          r.error.includes("个人连接"),
      ),
  );
  f.store.put("sessions", {
    ...run,
    core_session_id: undefined,
    connection_requirement: requirement,
    connection_wait: {
      id: randomUUID(),
      status: "pending",
      expires_at: Date.now() + 60000,
    },
  });
  await worker.receive("cli_0123456789abcdef", {
    ...f.message,
    messageId: "om_cancel_wait",
    rootId: f.message.messageId,
    content: "/cancel",
  });
  await worker.tick();
  assert.ok(f.store.get("sessions", run.id).deleted_at);
  assert.equal(f.calls.filter((c) => c.method === "POST").length, before);
});
