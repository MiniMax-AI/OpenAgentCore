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
  const { api, put, agent: base } = await setup(
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
  const agent = await put("agents", { ...base, runtime_id: runtime.id }, base.id);
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
    { bot_env: { TOKEN: "access_token" } },
    { env: { A: "x" }, bot_env: { A: "app_id" } },
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

async function setup(store, core, fetchImpl) {
  const api = productAPI(store, core, fetchImpl);
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
    runtime_id: runtime.id,
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
  // Kept until the first output so a failed Environment can be replaced; never returned.
  assert.deepEqual(store.get("sessions", pending).request, frozen);
  assert.equal(resumed.request, undefined);
  assert.equal(receipts.size, 3);
});
test("compatibility validation precedes execution and every Agent requires an environment template", async (t) => {
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
  store.put("agents", { ...store.get("agents", agent.id), runtime_id: runtime.id });
  await assert.rejects(put("sessions", input), /仅支持托管/);
  const { runtime_id, ...unplaced } = agent;
  await assert.rejects(put("agents", unplaced), /请选择环境模板/);
  await assert.rejects(
    put("mcps", {
      name: "MCP",
      label: "docs",
      url: "https://secret@example.com/mcp",
    }),
    /不含凭据/,
  );
  assert.equal(store.list("sessions").length, 0);
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
  const body = {
    agent_id: agent.id,
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

test("the Agent's bound Feishu bot supplies mapped identity variables without exposing the secret", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const calls = [];
  const { api, put, agent, input } = await setup(store, async (_p, _m, body) => {
    calls.push(body);
    return { id: randomUUID() };
  });
  const runtime = await put("runtimes", {
    name: "Feishu",
    env: { LARKSUITE_CLI_DEFAULT_AS: "bot" },
    bot_env: {
      FEISHU_APP_ID: "app_id",
      FEISHU_APP_SECRET: "app_secret",
      FEISHU_BRAND: "brand",
    },
  });
  await put("agents", { ...agent, runtime_id: runtime.id }, agent.id);
  await assert.rejects(put("sessions", input), /飞书机器人身份/);
  const botId = randomUUID();
  await api("PUT", `/app/feishu/bots/${botId}`, {
    revision: 0,
    name: "Assistant",
    app_id: "cli_0123456789abcdef",
    app_secret: "bot-secret",
    brand: "lark",
    enabled: false,
    agent_id: agent.id,
    direct: true,
  });
  await assert.rejects(
    api("PUT", `/app/feishu/bots/${randomUUID()}`, {
      revision: 0,
      name: "Second",
      app_id: "cli_fedcba9876543210",
      app_secret: "other",
      enabled: false,
      agent_id: agent.id,
    }),
    /已绑定另一个机器人/,
  );
  const record = await put("sessions", input);
  assert.deepEqual(calls.at(-1).environment.env, {
    LARKSUITE_CLI_DEFAULT_AS: "bot",
    FEISHU_APP_ID: "cli_0123456789abcdef",
    FEISHU_APP_SECRET: "bot-secret",
    FEISHU_BRAND: "lark",
  });
  assert.equal(record.configuration.bot_name, "Assistant");
  assert.ok(record.configuration.env_names.includes("FEISHU_APP_SECRET"));
  assert.ok(!JSON.stringify(await api("GET", "/app/sessions")).includes("bot-secret"));
  assert.ok(!JSON.stringify(await api("GET", "/app/runtimes")).includes("bot-secret"));
  assert.ok(!JSON.stringify(await api("GET", "/app/feishu/bots")).includes("bot-secret"));
  await assert.rejects(api("DELETE", `/app/agents/${agent.id}`), /仍被引用/);
});

test("Agents carry Harness options into the Session request and reject undeclared values", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const sent = [];
  const { api, put, agent, input } = await setup(store, async (_path, _method, body) => {
    sent.push(body);
    return { id: randomUUID() };
  });
  const codex = await put(
    "agents",
    {
      ...agent,
      harness: "codex",
      reasoning_effort: "high",
      reasoning_summary: "auto",
      web_search: "live",
      verbosity: "low",
      service_tier: "priority",
    },
    agent.id,
  );
  await put("sessions", input);
  const request = sent[0];
  assert.deepEqual(request.agent.x_agents_core, { harness: "codex" });
  assert.deepEqual(request.agent.reasoning, { effort: "high", summary: "auto" });
  assert.deepEqual(request.agent.text, { verbosity: "low" });
  assert.equal(request.agent.service_tier, "priority");
  assert.deepEqual(
    request.agent.tools.find((tool) => tool.type === "web_search"),
    { type: "web_search", mode: "live" },
  );
  assert.equal(request.x_agents_core.model_provider.protocol, "responses");
  const plain = await put(
    "agents",
    { ...codex, harness: "claude_sdk", reasoning_effort: "", reasoning_summary: "", web_search: "", verbosity: "", service_tier: "" },
    agent.id,
  );
  assert.equal(plain.web_search, "disabled");
  await put("sessions", { ...input, name: "Plain" });
  assert.equal(sent[1].agent.reasoning, undefined);
  assert.equal(sent[1].agent.service_tier, undefined);
  assert.deepEqual(sent[1].agent.text, { verbosity: "medium" });
  assert.equal(sent[1].x_agents_core.model_provider.protocol, "anthropic");
  for (const [patch, message] of [
    [{ reasoning_summary: "auto" }, /Claude Code 不支持所选的推理摘要/],
    [{ harness: "mcode", web_search: "live" }, /MiniMax Code 不支持所选的网页搜索方式/],
    [{ reasoning_effort: "minimal" }, /Claude Code 不支持所选的推理强度/],
    [{ harness: "local" }, /请选择执行引擎/],
  ])
    await assert.rejects(put("agents", { ...plain, ...patch }, agent.id), message);
  await assert.rejects(
    put("agents", { ...plain, model_id: undefined }, agent.id),
    /请选择模型/,
  );
});

test("prepared environments hand a ready Session to the next conversation and follow configuration changes", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const calls = [];
  let failEvents = false;
  const core = async (path, method, body, key) => {
    calls.push({ path, method, body, key });
    if (method === "POST" && path === "/v1/agents/sessions") return { id: randomUUID() };
    if (method === "POST" && path.endsWith("/events") && failEvents)
      throw new AppError(502, "Core unavailable");
    return {};
  };
  const { api, put, runtime, agent, input } = await setup(store, core);
  const sessions = api.sessions;
  await put("runtimes", { ...runtime, prepare: true }, runtime.id);
  await sessions.refreshWarm();
  const created = calls.filter((c) => c.method === "POST" && c.path === "/v1/agents/sessions");
  assert.equal(created.length, 1);
  assert.equal(created[0].body.input, undefined);
  assert.equal(created[0].body.metadata.warm, "true");
  const [warm] = store.list("warm_sessions");

  calls.length = 0;
  const id = randomUUID();
  const record = await put("sessions", input, id);
  assert.equal(record.core_session_id, warm.core_session_id);
  assert.equal(record.name, input.name);
  const sent = calls.find((c) => c.path.endsWith("/events"));
  assert.equal(sent.key, id);
  assert.equal(sent.body.events[0].input[0].content[0].text, input.input);
  assert.equal(calls.some((c) => c.method === "POST" && c.path === "/v1/agents/sessions" && c.body.input), false);
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(store.list("warm_sessions").length, 1, "a claimed Session is replaced");

  calls.length = 0;
  const [refill] = store.list("warm_sessions");
  await put("agents", { ...agent, instructions: "Changed" }, agent.id);
  await sessions.refreshWarm();
  assert.ok(calls.some((c) => c.method === "DELETE" && c.path.endsWith(refill.core_session_id)));
  assert.notEqual(store.list("warm_sessions")[0].core_session_id, refill.core_session_id);

  failEvents = true;
  const retried = randomUUID();
  await assert.rejects(put("sessions", { ...input, name: "Retry" }, retried), /Core unavailable/);
  assert.ok(store.get("sessions", retried).pending_input);
  failEvents = false;
  calls.length = 0;
  const recovered = await api("PUT", `/app/sessions/${retried}`, {});
  assert.equal(recovered.pending_input, undefined);
  assert.equal(calls.find((c) => c.path.endsWith("/events")).key, retried);

  await new Promise((resolve) => setTimeout(resolve, 10));
  await put("runtimes", { ...store.get("runtimes", runtime.id), prepare: false }, runtime.id);
  await sessions.refreshWarm();
  assert.equal(store.list("warm_sessions").length, 0);
});

test("Agent resources follow Core's Environment limits instead of one shared budget", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const { put, agent, runtime } = await setup(store, async () => ({ id: randomUUID() }));
  const make = (kind, n) =>
    Array.from({ length: n }, (_, i) => {
      const id = randomUUID();
      store.put("capabilities", { id, kind, name: `${kind} ${i}`, default_version: "1", versions: [{ version: "1" }] });
      return id;
    });
  const prompts = make("prompt", 12),
    knowledge = make("knowledge", 9),
    plugins = make("plugin", 51);
  const mcps = [];
  for (let i = 0; i < 9; i++)
    mcps.push((await put("mcps", { name: `MCP ${i}`, label: `mcp_${i}`, url: `https://mcp.example/${i}` })).id);
  const skills = Array.from({ length: 51 }, (_, i) => `skill_${i}`);
  const saved = await put(
    "agents",
    { ...agent, capability_ids: [...prompts, ...knowledge, ...plugins.slice(0, 50)], mcp_ids: mcps, skill_ids: skills.slice(0, 50) },
    agent.id,
  );
  assert.equal(saved.capability_ids.length, 71);
  assert.equal(saved.mcp_ids.length, 9);
  await assert.rejects(put("agents", { ...saved, skill_ids: skills }, agent.id), /最多选择 50 个 Skill/);
  await assert.rejects(put("agents", { ...saved, capability_ids: plugins }, agent.id), /最多选择 50 个 Plugin/);
  store.put("runtimes", {
    ...store.get("runtimes", runtime.id),
    files: Array.from({ length: 42 }, (_, i) => ({ path: `/workspace/f${i}.txt`, content: "x" })),
  });
  await assert.rejects(
    put("agents", { ...saved, capability_ids: knowledge }, agent.id),
    /知识和环境模板文件合计最多 50 个/,
  );
});

// A minimal Core: Sessions with Turns and Items; `plan` decides each new Turn.
function scriptedCore(plan) {
  const sessions = new Map();
  const calls = [];
  const nowS = () => Math.floor(Date.now() / 1000);
  const addTurn = (session, input) => {
    const outcome = plan.turns.shift() || "complete";
    const turn = { id: `turn_${randomUUID()}`, status: "in_progress", created_at: outcome === "stall" ? nowS() - 60 : nowS() };
    session.turns.unshift(turn);
    session.items.push({ id: randomUUID(), type: "message", role: "user", turn_id: turn.id, content: input?.[0]?.content || [] });
    if (outcome === "complete") {
      turn.status = "completed";
      session.items.push({ id: randomUUID(), type: "message", role: "assistant", turn_id: turn.id, content: [{ type: "output_text", text: "OK" }] });
    } else if (outcome === "fail") {
      turn.status = "failed";
      turn.error = { message: "connection reset" };
      session.status = "idle";
      return;
    }
    session.status = turn.status === "in_progress" ? "in_progress" : "idle";
  };
  const core = async (path, method, body, key) => {
    calls.push({ path, method, body, key });
    const [, id, sub] = path.match(/^\/v1\/agents\/sessions(?:\/([^/?]+))?(?:\/([a-z]+))?/) || [];
    if (method === "POST" && !id) {
      const session = { id: randomUUID(), status: "idle", turns: [], items: [] };
      sessions.set(session.id, session);
      if (plan.environments.shift() === "fail") {
        session.status = "failed";
        session.error = 'Failed to provision environment: script "setup_commands[0]" failed with exit code 1';
      } else if (body.input) addTurn(session, typeof body.input === "string" ? [{ role: "user", content: [{ type: "input_text", text: body.input }] }] : body.input);
      return { id: session.id };
    }
    const session = sessions.get(id);
    if (method === "DELETE") return sessions.delete(id), {};
    if (!session) throw new AppError(404, "missing");
    if (method === "GET" && !sub) return { id, status: session.status, error: session.error };
    if (method === "GET" && sub === "turns") return { data: session.turns };
    if (method === "GET" && sub === "items") return { data: [...session.items].reverse() };
    if (method === "GET" && sub === "artifacts") return { data: session.artifacts || [] };
    if (method === "POST" && sub === "events") {
      const [event] = body.events;
      if (event.type === "agent.session.input.cancel") {
        const running = session.turns.find((t) => t.status === "in_progress");
        if (running) running.status = "cancelled";
        session.status = "idle";
      } else addTurn(session, event.input);
      return {};
    }
    throw new Error(`unexpected ${method} ${path}`);
  };
  return { core, calls, sessions };
}

test("the Turn monitor replaces a failed Environment, resends stalled or failed Turns once, and explains final failures", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const plan = { environments: ["fail"], turns: [] };
  const fake = scriptedCore(plan);
  const { api, put, runtime, input } = await setup(store, fake.core);
  const turns = api.sessions.turns;

  // A failed Environment is replaced with the identical frozen request.
  const id = randomUUID();
  const created = await put("sessions", input, id);
  await turns.monitor();
  const replaced = store.get("sessions", id);
  assert.notEqual(replaced.core_session_id, created.core_session_id);
  const creations = fake.calls.filter((c) => c.method === "POST" && c.path === "/v1/agents/sessions");
  assert.deepEqual(creations[1].body, creations[0].body);
  assert.equal(creations[1].key, `${id}:attempt:1`);
  await turns.monitor();
  assert.equal(turns.summary(id).outcome, "completed");
  assert.equal(turns.summary(id).retries, 1);
  assert.equal(store.get("sessions", id).request, undefined, "the frozen request is dropped after output");

  // A second failure is final and explained in plain language.
  plan.environments.push("fail", "fail");
  const broken = randomUUID();
  await put("sessions", { ...input, name: "Broken" }, broken);
  await turns.monitor();
  await turns.monitor();
  const failed = turns.summary(broken);
  assert.equal(failed.outcome, "failed");
  assert.match(failed.failure, new RegExp(`「${runtime.name}」的初始化脚本第 1 段失败（退出码 1）`));

  // A follow-up without output for 45 s is cancelled and resent once; the cancelled
  // Turn is superseded so no channel reports it.
  plan.turns.push("stall", "complete");
  const after = await api("POST", `/app/sessions/${id}/messages`, { text: "Continue", key: "k1" });
  assert.equal(after.turn.kind, "followup");
  const stalled = fake.sessions.get(replaced.core_session_id).turns[0].id;
  await turns.monitor();
  assert.equal(turns.summary(id).stage, "retrying");
  await turns.monitor();
  assert.deepEqual(store.get("sessions", id).superseded_turns, [stalled]);
  assert.ok(turns.superseded(id, stalled));
  assert.ok(fake.calls.some((c) => c.key === `${id}:k1:resend:1`));
  await turns.monitor();
  assert.equal(turns.summary(id).outcome, "completed");

  // A Turn that fails before any output is resent once, then reported.
  plan.turns.push("fail", "fail");
  await api("POST", `/app/sessions/${id}/messages`, { text: "Again", key: "k2" });
  await turns.monitor();
  await turns.monitor();
  const final = turns.summary(id);
  assert.equal(final.outcome, "failed");
  assert.equal(final.retries, 1);
  assert.match(final.failure, /connection reset/);

  // Manual retry sends the input again as a new Turn.
  plan.turns.push("complete");
  await api("POST", `/app/sessions/${id}/retry`, {});
  await turns.monitor();
  assert.equal(turns.summary(id).outcome, "completed");

  fake.sessions.get(store.get("sessions", id).core_session_id).artifacts = [
    { id: "file_1", path: "/workspace/outputs/report.md", size_bytes: 2048, created_at: 100 },
  ];
  const outputs = await api("GET", "/app/artifacts");
  assert.deepEqual(outputs.map((row) => [row.path, row.session_id, row.conversation]), [
    ["/workspace/outputs/report.md", id, input.name],
  ]);

  const health = await api("GET", `/app/agents/${input.agent_id}/health`);
  assert.equal(health.turns, 5);
  assert.equal(health.success_rate, 3 / 5);
  assert.equal(health.retried, 4);
  assert.equal(typeof health.median_first_output_s, "number");
});

test("model tests make one tiny generation and environment trials report a real first reply", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const requests = [];
  const fetchImpl = async (url, init) => {
    requests.push({ url, init });
    return new Response(JSON.stringify({ error: "bad key provider-secret" }), { status: requests.length === 1 ? 200 : 401 });
  };
  const plan = { environments: ["fail", "ok"], turns: [] };
  const fake = scriptedCore(plan);
  const { api, model, runtime } = await setup(store, fake.core, fetchImpl);
  const ok = await api("POST", `/app/models/${model.id}/test`, {});
  assert.equal(ok.ok, true);
  assert.equal(requests[0].url, "https://provider.example/v1/responses");
  assert.equal(JSON.parse(requests[0].init.body).model, "kimi-k2.6");
  assert.equal(requests[0].init.headers.Authorization, "Bearer provider-secret");
  const denied = await api("POST", `/app/models/${model.id}/test`, {});
  assert.equal(denied.ok, false);
  assert.match(denied.error, /HTTP 401/);
  assert.equal(denied.error.includes("provider-secret"), false, "the key is never echoed");

  const settle = async () => {
    for (let i = 0; i < 40 && api.sessions.trial.get(runtime.id)?.state === "running"; i++)
      await new Promise((resolve) => setTimeout(resolve, 50));
    return api("GET", `/app/runtimes/${runtime.id}/trial`);
  };
  assert.equal((await api("POST", `/app/runtimes/${runtime.id}/trial`, {})).state, "running");
  const failed = await settle();
  assert.equal(failed.state, "failed");
  assert.match(failed.failure, /初始化脚本第 1 段失败/);
  await api("POST", `/app/runtimes/${runtime.id}/trial`, {});
  const passed = await settle();
  assert.equal(passed.state, "succeeded");
  assert.equal(typeof passed.seconds, "number");
  assert.equal(fake.sessions.size, 0, "trial Sessions are deleted");
  store.put("agents", { ...store.list("agents")[0], runtime_id: randomUUID() });
  await assert.rejects(api("POST", `/app/runtimes/${runtime.id}/trial`, {}), /先把这个环境模板绑定到一个 Agent/);
});
