import { test } from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { homedir } from "node:os";
import { unzipSync, strFromU8 } from "fflate";
import { openStore, dataPath, AppError } from "../server/store.mjs";
import { productAPI } from "../server/product.mjs";

test("native runtimes keep host paths, freeze provider secrets, and create without running a Turn", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  let fail = true,
    sent;
  const { api, put, model, agent } = await setup(
    store,
    async (_path, _method, body) => {
      sent = body;
      if (fail) throw new Error("network");
      return { id: randomUUID() };
    },
  );
  const provider = store.get("providers", model.provider_id);
  await put(
    "providers",
    {
      ...provider,
      base_url: "https://provider.example/v1",
      api_key: "provider-secret",
    },
    provider.id,
  );
  const config = await put(
    "agents",
    { ...agent, harness: "codex", skill_ids: [], mcp_ids: [] },
    agent.id,
  );
  for (const [platform, directory] of [
    ["linux", "/home/user/project"],
    ["macos", "/Users/user/project"],
    ["windows", "C:\\Users\\user\\project"],
  ]) {
    const runtime = await put("runtimes", {
      name: platform,
      environment: "self_hosted",
      platform,
      workspace_directory: directory,
      capability_directories: [directory],
    });
    const id = randomUUID();
    fail = true;
    await assert.rejects(
      put(
        "sessions",
        { name: "Native", agent_id: config.id, runtime_id: runtime.id },
        id,
      ),
      /network/,
    );
    assert.equal(sent.input, undefined);
    assert.equal(sent.environment.workspace_directory, directory);
    assert.deepEqual(sent.environment.capability_directories, [directory]);
    assert.equal(sent.x_agents_core.model_provider.api_key, "provider-secret");
    assert.equal(
      JSON.stringify(await api("GET", `/app/sessions/${id}`)).includes(
        "provider-secret",
      ),
      false,
    );
    assert.equal(
      JSON.stringify(await api("GET", "/app/sessions")).includes(
        "provider-secret",
      ),
      false,
    );
    fail = false;
    const record = await put("sessions", {}, id);
    assert.equal(record.self_hosted.platform, platform);
    assert.equal(record.request, undefined);
  }
  for (const [platform, directory] of [
    ["linux", "relative"], ["linux", "/tmp/work/"], ["linux", "/tmp//work"],
    ["linux", "/tmp/../work"], ["linux", "/tmp/a\tb"],
    ["windows", "\\work"], ["windows", "C:\\work\\"], ["windows", "C:\\a?b"],
  ]) {
    await assert.rejects(put("runtimes", {
      name: "bad", environment: "self_hosted", platform, workspace_directory: directory,
    }), /绝对目录/);
  }
  const runtime = await put("runtimes", {
    name: "Local", environment: "self_hosted", platform: "linux", workspace_directory: "/workspace",
  });
  await put("agents", { ...config, mcp_ids: agent.mcp_ids }, config.id);
  sent = undefined;
  await assert.rejects(put("sessions", {
    name: "Unsupported MCP", agent_id: config.id, runtime_id: runtime.id,
  }), /本地 Plugin/);
  assert.equal(sent, undefined);

});

test("Provider discovery uses only its credential; selected and custom models save together", async () => {
  const store = openStore(":memory:");
  const id = randomUUID();
  const api = productAPI(store, null, async (url, init) => {
    assert.equal(url, "https://models.example/v1/models");
    assert.equal(init.method, "GET");
    assert.equal(init.redirect, "manual");
    assert.equal(init.headers.Authorization, "Bearer provider-secret");
    assert.equal(init.headers["OpenAI-Beta"], undefined);
    return Response.json({
      data: [{ id: "one" }, { id: "two" }, { id: "one" }],
    });
  });
  const body = {
    name: "Team",
    base_url: "https://models.example/v1/",
    api_key: "provider-secret",
  };
  assert.deepEqual(await api("POST", "/app/providers/discover", body), {
    models: ["one", "two"],
  });
  assert.equal(store.list("providers").length, 0);
  const saved = await api("PUT", `/app/providers/${id}`, {
    ...body,
    models: [
      { model: "one", name: "One" },
      { model: "custom", name: "Custom" },
    ],
  });
  const snapshot = await api("GET", `/app/providers/${id}`);
  assert.equal(snapshot.revision, saved.revision);
  assert.equal(snapshot.models.length, 2);
  assert.ok(snapshot.models.every((model) => model.provider_id === id));
  assert.equal(saved.has_api_key, true);
  assert.equal(JSON.stringify(saved).includes("provider-secret"), false);
  assert.equal(
    JSON.stringify(await api("GET", "/app/providers")).includes(
      "provider-secret",
    ),
    false,
  );
  assert.equal(
    JSON.stringify(await api("GET", `/app/providers/${id}`)).includes(
      "provider-secret",
    ),
    false,
  );
  assert.deepEqual(await api("POST", "/app/providers/discover", { id }), {
    models: ["one", "two"],
  });
  const one = store.list("models").find((row) => row.model === "one");
  store.put("agents", { id: randomUUID(), model_id: one.id, mcp_ids: [] });
  await assert.rejects(
    api("PUT", `/app/providers/${id}`, {
      ...saved,
      name: "Changed",
      models: [],
    }),
    /仍被 Agent 引用/,
  );
  assert.equal(store.get("providers", id).name, "Team");
  assert.equal(store.list("models").length, 2);
  const updated = await api("PUT", `/app/providers/${id}`, {
    ...saved,
    models: [{ model: "one", name: "One" }],
  });
  assert.equal(store.list("models").length, 1);
  assert.equal(store.list("models")[0].id, one.id);
  assert.equal(store.get("providers", id).api_key, "provider-secret");
  assert.equal(updated.has_api_key, true);
  store.close();
});

test("Provider discovery reports failures without leaking keys or upstream bodies", async () => {
  const store = openStore(":memory:");
  const body = { base_url: "https://models.example", api_key: "private-key" };
  for (const response of [
    () => Response.json({ error: "private-key" }, { status: 401 }),
    () =>
      new Response(null, {
        status: 302,
        headers: { location: "https://other.example" },
      }),
    () => Response.json({ data: [], has_more: true }),
    () => {
      throw new Error("private-key");
    },
  ]) {
    const api = productAPI(store, null, response);
    await assert.rejects(
      api("POST", "/app/providers/discover", body),
      (error) => error.status === 502 && !error.message.includes("private-key"),
    );
  }
  store.close();
});

async function setup(store, core) {
  const api = productAPI(store, core);
  const put = (kind, body, id = randomUUID()) =>
    api("PUT", `/app/${kind}/${id}`, body);
  const provider = await put("providers", { name: "Moonshot", base_url: "https://provider.example/v1", api_key: "provider-secret" });
  const model = await put("models", {
    name: "Kimi",
    model: "kimi-k2.6",
    provider_id: provider.id,
  });
  const runtime = await put("runtimes", {
    name: "Sandbox",
    environment: "openai_hosted",
  });
  const mcp = await put("mcps", {
    name: "Docs",
    label: "docs",
    url: "https://mcp.example/docs",
  });
  const agent = await put("agents", {
    name: "Reviewer",
    model_id: model.id,
    harness: "claude_sdk",
    instructions: "Review carefully",
    mcp_ids: [mcp.id],
    skill_ids: ["skill_review"],
  });
  return {
    api,
    put,
    model,
    runtime,
    agent,
    input: {
      name: "Review",
      agent_id: agent.id,
      runtime_id: runtime.id,
      input: "Review this",
    },
  };
}
test("one Agent creates independent Sessions; edits do not mutate prior execution; retries survive restart", async (t) => {
  const root = join(homedir(), ".oac", "tests");
  await mkdir(root, { recursive: true });
  const dir = await mkdtemp(join(root, "parsar-store-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const path = join(dir, "test.sqlite");
  let store = openStore(path);
  const calls = [],
    receipts = new Map();
  let lose = false;
  const core = async (path, method, body, key) => {
    calls.push({ path, body, key });
    if (!receipts.has(key)) receipts.set(key, { id: randomUUID() });
    if (lose) {
      lose = false;
      throw new AppError(502, "lost response");
    }
    return receipts.get(key);
  };
  const { api, put, agent, model, input } = await setup(store, core);
  assert.equal(calls.length, 0);
  const first = await put("sessions", input);
  assert.deepEqual(calls[0].body.environment.skills, [
    { type: "skill_reference", skill_id: "skill_review" },
  ]);
  assert.deepEqual(calls[0].body.x_agents_core.model_provider, {
    protocol: "anthropic", base_url: "https://provider.example/v1", api_key: "provider-secret",
  });
  assert.equal(JSON.stringify(first).includes("provider-secret"), false);
  assert.equal(calls[0].body.agent.tools.length, 1);
  const zip = unzipSync(
    Buffer.from(calls[0].body.environment.plugins[0].source.data, "base64"),
  );
  assert.equal(
    JSON.parse(strFromU8(zip["agent/.mcp.json"])).mcpServers.docs.url,
    "https://mcp.example/docs",
  );
  await put("agents", { ...agent, instructions: "Updated" }, agent.id);
  const second = await put("sessions", input);
  assert.notEqual(first.core_session_id, second.core_session_id);
  assert.equal(calls[0].body.agent.instructions, "Review carefully");
  assert.equal(calls[1].body.agent.instructions, "Updated");
  await assert.rejects(api("DELETE", `/app/models/${model.id}`), /仍被引用/);
  await assert.rejects(api("DELETE", `/app/agents/${agent.id}`), /仍被引用/);
  const pending = randomUUID();
  lose = true;
  await assert.rejects(put("sessions", input, pending), /lost response/);
  const frozen = calls.at(-1).body;
  store.close();
  store = openStore(path);
  t.after(() => store.close());
  const resumed = await productAPI(store, core)(
    "PUT",
    `/app/sessions/${pending}`,
    {},
  );
  assert.equal(resumed.core_session_id, receipts.get(pending).id);
  assert.deepEqual(calls.at(-1).body, frozen);
  assert.equal(store.get("sessions", pending).request, undefined);
  assert.equal(receipts.size, 3);
});
test("compatibility validation precedes execution, and old configurations migrate without fake Sessions", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const { api, put, agent, input } = await setup(store, () => {
    throw new Error("must not execute");
  });
  const runtime = await put("runtimes", { name: "Text", environment: "none" });
  await assert.rejects(
    put("sessions", { ...input, runtime_id: runtime.id }),
    /Skills 需要/,
  );
  await assert.rejects(
    put("mcps", {
      name: "MCP",
      label: "docs",
      url: "https://secret@example.com/mcp",
    }),
    /不含凭据/,
  );
  const legacy = { ...agent, id: randomUUID(), runtime_id: runtime.id };
  store.put("instances", legacy);
  productAPI(store, () => {});
  assert.equal(store.get("agents", legacy.id).name, agent.name);
  assert.equal(store.get("agents", legacy.id).runtime_id, undefined);
  assert.equal(store.list("sessions").length, 0);
  assert.equal(store.list("instances").length, 0);
  assert.notEqual(
    dataPath({ target: "https://core.example", key: "a" }),
    dataPath({ target: "https://core.example", key: "b" }),
  );
});

test("Providers are explicit groups with many models, guarded deletion and no automatic defaults", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const api = productAPI(store, () => {});
  assert.deepEqual(await api("GET", "/app/providers"), []);
  const id = randomUUID();
  const provider = await api("PUT", `/app/providers/${id}`, { name: "Vendor" });
  for (const model of ["model-a", "model-b"])
    await api("PUT", `/app/models/${randomUUID()}`, {
      name: model,
      model,
      provider_id: id,
    });
  assert.equal((await api("GET", "/app/models")).length, 2);
  await assert.rejects(api("DELETE", `/app/providers/${id}`), /仍被引用/);
  await api("PUT", `/app/providers/${id}`, {
    ...(await api("GET", `/app/providers/${id}`)),
    name: "Renamed",
  });
  assert.ok(store.list("models").every((m) => m.provider_id === id));
  await assert.rejects(
    api("PUT", `/app/models/${randomUUID()}`, { name: "Missing", model: "x" }),
    /有效的资源/,
  );
});

test("stale Provider selections cannot overwrite model edits, moves or removals", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const api = productAPI(store, null);
  const id = randomUUID(),
    other = randomUUID();
  await api("PUT", `/app/providers/${id}`, {
    name: "First",
    models: [{ model: "old-id", name: "Model" }],
  });
  await api("PUT", `/app/providers/${other}`, { name: "Other" });
  const snapshot = () => api("GET", `/app/providers/${id}`);
  const model = store.list("models")[0];
  const old = await snapshot();
  const edited = await api("PUT", `/app/models/${model.id}`, {
    ...model,
    model: "new-id",
  });
  await assert.rejects(
    api("PUT", `/app/providers/${id}`, {
      ...old,
      models: [{ model: "old-id", name: "Model" }],
    }),
    /配置已更新/,
  );
  assert.equal(store.get("models", model.id).model, "new-id");
  const beforeMove = await snapshot();
  await api("PUT", `/app/models/${model.id}`, {
    ...edited,
    provider_id: other,
  });
  await assert.rejects(
    api("PUT", `/app/providers/${id}`, { ...beforeMove, models: [] }),
    /配置已更新/,
  );
  const beforeRemove = await api("GET", `/app/providers/${other}`);
  await api("DELETE", `/app/models/${model.id}`);
  await assert.rejects(
    api("PUT", `/app/providers/${other}`, {
      ...beforeRemove,
      models: [{ model: "new-id", name: "Model" }],
    }),
    /配置已更新/,
  );
  assert.equal(store.list("models").length, 0);
});

test("model editing and moving retain unique IDs within each Provider", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const api = productAPI(store, null);
  const first = randomUUID(),
    second = randomUUID();
  await api("PUT", `/app/providers/${first}`, {
    name: "First",
    models: [
      { model: "one", name: "One" },
      { model: "two", name: "Two" },
    ],
  });
  await api("PUT", `/app/providers/${second}`, {
    name: "Second",
    models: [{ model: "one", name: "Other One" }],
  });
  const two = store.list("models").find((row) => row.model === "two");
  const other = store.list("models").find((row) => row.provider_id === second);
  await assert.rejects(
    api("PUT", `/app/models/${two.id}`, { ...two, model: " one " }),
    /已有相同的模型 ID/,
  );
  await assert.rejects(
    api("PUT", `/app/models/${other.id}`, { ...other, provider_id: first }),
    /已有相同的模型 ID/,
  );
  assert.equal(store.get("models", two.id).model, "two");
  assert.equal(store.get("models", other.id).provider_id, second);
  const renamed = await api("PUT", `/app/models/${two.id}`, {
    ...two,
    model: " three ",
  });
  assert.equal(renamed.model, "three");
});


test("MiniMax MCP bindings reject before Core for text-only and hosted placements", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  let calls = 0;
  const { put, agent } = await setup(store, async () => { calls++; return { id: randomUUID() }; });
  const config = await put("agents", { ...agent, harness: "mcode", skill_ids: [] }, agent.id);
  for (const environment of ["none", "openai_hosted"]) {
    const runtime = await put("runtimes", { name: environment, environment });
    await assert.rejects(put("sessions", { name: "Rejected", input: "hello", agent_id: config.id, runtime_id: runtime.id }), /MiniMax Code.*MCP/);
  }
  assert.equal(calls, 0);
});
