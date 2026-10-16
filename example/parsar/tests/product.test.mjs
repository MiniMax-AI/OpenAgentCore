import { test } from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { homedir } from "node:os";
import { openStore, dataPath, AppError } from "../server/store.mjs";
import { productAPI } from "../server/product.mjs";

test("environment templates prepare hosted Sessions, protect variables and freeze retries", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  let failed = true;
  const calls = [];
  const { api, put, agent } = await setup(
    store,
    async (_path, _method, body) => {
      calls.push(structuredClone(body));
      if (failed) throw new Error("lost response");
      return { id: randomUUID() };
    },
  );
  const runtime = await put("runtimes", {
    name: "Office",
    packages: { npm: ["typescript@5.8.3"], python: ["packaging==26.0"] },
    setup_script: "set -eu\nprintf ready > outputs/ready",
    env: { OFFICE_TOKEN: "private-env" },
    files: [{ path: "/workspace/config.txt", content: "initial content" }],
  });
  assert.deepEqual(runtime.env, { OFFICE_TOKEN: null });
  assert.ok(
    !JSON.stringify(await api("GET", "/app/runtimes")).includes("private-env"),
  );
  const id = randomUUID();
  await assert.rejects(
    put(
      "sessions",
      {
        name: "Prepare",
        input: "Read config",
        agent_id: agent.id,
        runtime_id: runtime.id,
        files: [
          { type: "inline", path: "/workspace/inputs/input.txt", data: "eA==" },
        ],
      },
      id,
    ),
    /lost response/,
  );
  const frozen = calls[0];
  assert.equal(frozen.environment.type, "openai_hosted");
  assert.equal(frozen.environment.env.OFFICE_TOKEN, "private-env");
  assert.deepEqual(frozen.environment.packages, runtime.packages);
  assert.equal(
    frozen.environment.setup_commands[0].command,
    runtime.setup_script,
  );
  assert.deepEqual(
    frozen.environment.files.map((f) => f.path),
    ["/workspace/config.txt", "/workspace/inputs/input.txt"],
  );
  assert.ok(
    !JSON.stringify(await api("GET", `/app/sessions/${id}`)).includes(
      "private-env",
    ),
  );
  const next = await put(
    "runtimes",
    {
      ...runtime,
      setup_script: "echo changed",
      env: { OFFICE_TOKEN: null, NEW: "value" },
    },
    runtime.id,
  );
  assert.equal(
    store.get("runtimes", runtime.id).env.OFFICE_TOKEN,
    "private-env",
  );
  failed = false;
  const record = await put("sessions", {}, id);
  assert.deepEqual(calls.at(-1), frozen);
  assert.deepEqual(record.configuration.env_names, ["OFFICE_TOKEN"]);
  await put("runtimes", { ...next, env: {} }, runtime.id);
  assert.deepEqual(store.get("runtimes", runtime.id).env, {});
  for (const body of [
    { environment: "self_hosted" },
    { environment: "none" },
    { packages: { system: ["curl"] } },
    { packages: { npm: ["--ignore-scripts"] } },
    { env: { PATH: "x" } },
    { env: { OAC_KEY: "x" } },
    { env: { NEW: null } },
    { files: [{ path: "/workspace/../outside", content: "x" }] },
    {
      files: [
        { path: "/workspace/a", content: "x" },
        { path: "/workspace/a", content: "y" },
      ],
    },
  ])
    await assert.rejects(
      put("runtimes", { name: "Invalid", ...body }),
      (error) => error.status === 400,
    );
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
  const provider = await put("providers", {
    name: "Moonshot",
    base_url: "https://provider.example/v1",
    api_key: "provider-secret",
  });
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
    protocol: "anthropic",
    base_url: "https://provider.example/v1",
    api_key: "provider-secret",
  });
  assert.equal(JSON.stringify(first).includes("provider-secret"), false);
  assert.equal(calls[0].body.agent.tools.length, 2);
  assert.deepEqual(calls[0].body.agent.tools[1], {
    type: "mcp",
    server_label: "docs",
    transport: { type: "http", server_url: "https://mcp.example/docs" },
    connection_origin: "environment",
    required: true,
  });
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
  const runtime = {
    id: randomUUID(),
    name: "Unsupported",
    environment: "none",
  };
  store.put("runtimes", runtime);
  await assert.rejects(
    put("sessions", { ...input, runtime_id: runtime.id }),
    /仅支持托管/,
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

test("MiniMax MCP bindings reject before Core", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  let calls = 0;
  const { put, agent } = await setup(store, async () => {
    calls++;
    return { id: randomUUID() };
  });
  const config = await put(
    "agents",
    { ...agent, harness: "mcode", skill_ids: [] },
    agent.id,
  );
  for (const environment of ["openai_hosted"]) {
    const runtime = await put("runtimes", { name: environment, environment });
    await assert.rejects(
      put("sessions", {
        name: "Rejected",
        input: "hello",
        agent_id: config.id,
        runtime_id: runtime.id,
      }),
      /MiniMax Code.*MCP/,
    );
  }
  assert.equal(calls, 0);
});

test("Skills directory preview uses the pinned CLI, bounds imports, and cleans its temporary checkout", async () => {
  const { previewRegistrySkill } = await import(
    "../server/skills-directory.mjs"
  );
  const { writeFile, access, symlink } = await import("node:fs/promises");
  const { unzipSync, strFromU8 } = await import("fflate");
  let temporary;
  const result = await previewRegistrySkill(
    { source: "example/skills", slug: "review" },
    async (command, args, options) => {
      assert.equal(command, "npx");
      assert.deepEqual(args, [
        "--yes",
        "skills@1.5.23",
        "add",
        "example/skills",
        "--skill",
        "review",
        "--agent",
        "claude-code",
        "--copy",
        "--yes",
      ]);
      temporary = options.cwd;
      const directory = join(temporary, ".claude", "skills", "review");
      await mkdir(directory, { recursive: true });
      await writeFile(
        join(directory, "SKILL.md"),
        "---\nname: review\ndescription: Review changes\n---\nCheck tests.",
      );
    },
  );
  assert.match(
    strFromU8(
      unzipSync(Buffer.from(result.archive, "base64"))["review/SKILL.md"],
    ),
    /Check tests/,
  );
  await assert.rejects(access(temporary));
  await assert.rejects(
    previewRegistrySkill({ source: "--evil/repo", slug: "review" }, () =>
      assert.fail("must not run"),
    ),
    { status: 400 },
  );
  await assert.rejects(
    previewRegistrySkill({ source: "example/repo", slug: "../escape" }, () =>
      assert.fail("must not run"),
    ),
    { status: 400 },
  );
  await assert.rejects(
    previewRegistrySkill(
      { source: "example/skills", slug: "review" },
      async (_command, _args, options) => {
        temporary = options.cwd;
        const directory = join(temporary, ".claude", "skills", "review");
        await mkdir(directory, { recursive: true });
        await symlink("/etc/passwd", join(directory, "SKILL.md"));
      },
    ),
    { status: 400 },
  );
  await assert.rejects(access(temporary));
});

test("MCP OAuth uses SDK PKCE, persists a public OAuth Vault credential and redacts completed state", async () => {
  const { mcpOAuthAPI } = await import("../server/mcp-oauth.mjs");
  const rows = new Map(),
    credentials = [],
    requests = [];
  const store = {
    get: (kind, id) => rows.get(`${kind}:${id}`),
    put: (kind, value) =>
      rows.set(`${kind}:${value.id}`, structuredClone(value)),
    remove: (kind, id) => rows.delete(`${kind}:${id}`),
  };
  const vault = randomUUID(),
    id = randomUUID();
  const core = async (path, method, body) => {
    assert.ok(path.startsWith("/v1/vaults/"));
    requests.push({ method, path, body });
    if (method === "GET" && path.includes("/credentials"))
      return { data: credentials, has_more: false };
    if (method === "GET") return { id: vault };
    const value = {
      id: randomUUID(),
      name: body.name,
      auth: { type: "mcp_oauth", mcp_server_url: body.auth.mcp_server_url },
    };
    credentials.push(value);
    return value;
  };
  const metadata = {
    issuer: "https://auth.example",
    authorization_endpoint: "https://auth.example/authorize",
    token_endpoint: "https://auth.example/token",
    registration_endpoint: "https://auth.example/register",
    response_types_supported: ["code"],
    code_challenge_methods_supported: ["S256"],
    token_endpoint_auth_methods_supported: ["none"],
  };
  const network = async (url, options) => {
    const path = String(url);
    if (path.includes("oauth-protected-resource"))
      return Response.json({
        resource: "https://mcp.example/docs",
        authorization_servers: ["https://auth.example"],
      });
    if (path.includes(".well-known")) return Response.json(metadata);
    if (path.endsWith("/register"))
      return Response.json(
        {
          client_id: "parsar-client",
          redirect_uris: ["http://127.0.0.1:18190/oauth/callback"],
        },
        { status: 201 },
      );
    if (path.endsWith("/token")) {
      assert.equal(options.body.get("code"), "authorized-code");
      assert.ok(options.body.get("code_verifier"));
      return Response.json({
        access_token: "private-access",
        token_type: "Bearer",
        refresh_token: "private-refresh",
        expires_in: 3600,
      });
    }
    throw new Error(`Unexpected fixture URL ${path}`);
  };
  const api = mcpOAuthAPI(store, core, "http://127.0.0.1:18190", network);
  const start = await api("POST", `/app/mcp-oauth/${id}`, {
    action: "begin",
    vault_id: vault,
    name: "Docs",
    url: "https://mcp.example/docs",
  });
  const redirect = new URL(start.authorization_url);
  assert.equal(redirect.origin, "https://auth.example");
  assert.equal(redirect.searchParams.get("code_challenge_method"), "S256");
  const state = redirect.searchParams.get("state").split(":")[1];
  await assert.rejects(
    api("POST", `/app/mcp-oauth/${id}`, {
      action: "complete",
      state: "wrong",
      code: "authorized-code",
    }),
    /state does not match/,
  );
  const result = await api("POST", `/app/mcp-oauth/${id}`, {
    action: "complete",
    state,
    code: "authorized-code",
  });
  const saved = requests.find((row) => row.method === "POST").body.auth;
  assert.equal(saved.type, "mcp_oauth");
  assert.equal(saved.refresh.refresh_token, "private-refresh");
  assert.equal(saved.refresh.token_endpoint_auth.type, "none");
  assert.equal(JSON.stringify(result).includes("private-"), false);
  assert.equal(
    JSON.stringify(store.get("mcp_oauth", id)).includes("private-"),
    false,
  );
  await api("POST", `/app/mcp-oauth/${id}`, {
    action: "complete",
    state,
    code: "authorized-code",
  });
  assert.equal(credentials.length, 1);
});

test("model availability and pricing remain product-owned and freeze with a Session", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const { api, model, agent } = await setup(store, async () => ({
    id: randomUUID(),
  }));
  const priced = await api("PUT", `/app/models/${model.id}`, {
    ...model,
    pricing: { input: 2, output: 8 },
    enabled: false,
  });
  store.put("agents", { ...agent, skill_ids: [], mcp_ids: [] });
  const runtime = {
    id: randomUUID(),
    name: "Workspace",
    environment: "openai_hosted",
    revision: 1,
  };
  store.put("runtimes", runtime);
  const body = {
    agent_id: agent.id,
    runtime_id: runtime.id,
    name: "Priced task",
    input: "Check usage",
  };
  await assert.rejects(
    api("PUT", `/app/sessions/${randomUUID()}`, body),
    /资源已不可用/,
  );
  await api("PUT", `/app/models/${model.id}`, { ...priced, enabled: true });
  const record = await api("PUT", `/app/sessions/${randomUUID()}`, body);
  assert.deepEqual(record.pricing, { input: 2, output: 8 });
  const current = store.get("models", model.id);
  await api("PUT", `/app/models/${model.id}`, {
    ...current,
    pricing: { input: 3, output: 9 },
  });
  assert.deepEqual(store.get("sessions", record.id).pricing, {
    input: 2,
    output: 8,
  });
  await assert.rejects(
    api("PUT", `/app/models/${model.id}`, {
      ...store.get("models", model.id),
      pricing: { input: -1, output: 0 },
    }),
    /价格/,
  );
});
