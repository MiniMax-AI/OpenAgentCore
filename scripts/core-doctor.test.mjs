import assert from "node:assert/strict";
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  CORE_DOCTOR_EXIT_CODES,
  PARSAR_PROTOCOL_BASELINE_REVISION,
  parseDaemonStatus,
  runCoreDoctor,
} from "./core-doctor.mjs";

const testRoot = join(homedir(), ".oac", "tests");
await mkdir(testRoot, { recursive: true });

const fixtureRoot = new URL("./fixtures/core-doctor/", import.meta.url);
const fixtureToken = "fixture-bearer";
const fixtureTarget = "https://core.fixture.invalid";

async function fixture(name) {
  return readFile(new URL(name, fixtureRoot), "utf8");
}

function captureStream() {
  let value = "";
  return {
    stream: {
      write(chunk) {
        value += String(chunk);
        return true;
      },
    },
    value: () => value,
  };
}

async function createLocalState(t, { token = fixtureToken } = {}) {
  const root = await mkdtemp(join(testRoot, "agents-core-doctor-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const homeDir = join(root, "home");
  const stateDir = join(homeDir, ".oac", "dev");
  await mkdir(stateDir, { recursive: true, mode: 0o700 });
  await writeFile(join(stateDir, "web-token"), token, { mode: 0o600 });
  await chmod(join(stateDir, "web-token"), 0o600);
  return { root, homeDir, stateDir };
}

async function successfulFetchRecorder({ apiStatus = 200, apiBody, healthStatus = 200, healthBody } = {}) {
  const requests = [];
  const agentsBody = apiBody ?? await fixture("agents-list.json");
  const fetchImpl = async (url, init = {}) => {
    const parsed = new URL(url);
    requests.push({
      url: parsed,
      method: init.method,
      headers: new Headers(init.headers),
      redirect: init.redirect,
    });
    if (parsed.pathname === "/healthz") {
      return new Response(healthBody ?? JSON.stringify({ status: "ok" }), {
        status: healthStatus,
        headers: { "content-type": "application/json" },
      });
    }
    return new Response(agentsBody, {
      status: apiStatus,
      headers: { "content-type": "application/json" },
    });
  };
  return { fetchImpl, requests };
}

function canonicalAgent(overrides = {}) {
  return {
    id: "agent_fixture",
    object: "agent",
    model: "fixture/model",
    name: "Fixture Agent",
    instructions: null,
    metadata: { fixture: "safe" },
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [],
    created_at: 1_789_438_200,
    updated_at: 1_789_438_800,
    ...overrides,
  };
}

function canonicalMCPTool(overrides = {}) {
  return {
    type: "mcp",
    server_label: "records",
    transport: { type: "http", server_url: "https://mcp.fixture.invalid/tools", headers: {} },
    allowed_tools: null,
    connection_origin: "service",
    credential_id: null,
    request_metadata: {},
    required: false,
    ...overrides,
  };
}

function canonicalAgentPage(agent = canonicalAgent(), overrides = {}) {
  const cursor = agent && typeof agent === "object" ? agent.id : null;
  return {
    object: "list",
    data: [agent],
    first_id: cursor,
    last_id: cursor,
    has_more: false,
    ...overrides,
  };
}

async function runScenario({
  argv = [],
  env = {},
  cwd,
  homeDir,
  platform = "darwin",
  fetchImpl,
  runCommand = async () => ({ code: 0, stdout: await fixture("daemon-status-absent.txt"), stderr: "" }),
} = {}) {
  const stdout = captureStream();
  const stderr = captureStream();
  const result = await runCoreDoctor({
    argv,
    env,
    cwd,
    homeDir,
    platform,
    fetchImpl,
    runCommand,
    stdout: stdout.stream,
    stderr: stderr.stream,
  });
  return { result, stdout: stdout.value(), stderr: stderr.value() };
}

test("documents the read-only command and exit-code contract", async () => {
  const result = await runScenario({
    argv: ["--help"],
    env: {},
    cwd: process.cwd(),
    homeDir: testRoot,
    fetchImpl: async () => assert.fail("help must not make a request"),
    runCommand: async () => assert.fail("help must not inspect a daemon"),
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.ok);
  assert.match(result.stdout, /performs only GET requests/);
  assert.match(result.stdout, new RegExp(PARSAR_PROTOCOL_BASELINE_REVISION));
  assert.match(result.stdout, /additive JSON fields and unknown\nnonempty tool-type discriminants are accepted/);
  assert.match(result.stdout, /does not prove\s+that the Web supports those tools/);
  assert.match(result.stdout, /Exit codes:\n  0[\s\S]*\n  1[\s\S]*\n  2/);
  assert.equal(result.stderr, "");
});

test("authenticates a basic GET without leaking the token or daemon output", async (t) => {
  const state = await createLocalState(t);
  const { fetchImpl, requests } = await successfulFetchRecorder();
  const daemonOutput = await fixture("daemon-status-paired.txt");
  let commandCall;
  const result = await runScenario({
    env: {
      OAC_WEB_DEV_PROXY_TARGET: fixtureTarget,
      OPENAI_API_KEY: "provider-secret-marker",
      PATH: "/synthetic/bin",
      HOME: state.homeDir,
    },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl,
    runCommand: async (call) => {
      commandCall = call;
      return { code: 0, stdout: daemonOutput, stderr: "runner_credential=fixture-super-secret-token" };
    },
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.ok);
  assert.match(result.stdout, new RegExp(PARSAR_PROTOCOL_BASELINE_REVISION));
  assert.match(result.stdout, /Core API authenticated; basic Agent resource envelope parsed/);
  assert.match(result.stdout, /tool\/Web compatibility, full protocol compatibility/);
  assert.match(result.stdout, /paired profile and pid file reported; process and connection remain unknown/);
  assert.doesNotMatch(result.stdout, /\[PASS\] Daemon/);
  assert.match(result.stdout, /executor, model, and provider readiness were not verified/);
  assert.doesNotMatch(result.stdout, new RegExp(fixtureToken));
  assert.doesNotMatch(result.stdout, /synthetic\/private|synthetic-runtime|synthetic-host/);
  assert.equal(result.stderr, "");
  assert.deepEqual(requests.map(({ method }) => method), ["GET", "GET"]);
  assert.deepEqual(requests.map(({ url }) => `${url.pathname}${url.search}`), ["/healthz", "/v1/agents?limit=1"]);
  assert.equal(requests[0].headers.has("authorization"), false);
  assert.equal(requests[1].headers.get("authorization"), `Bearer ${fixtureToken}`);
  assert.equal(requests[1].headers.get("openai-beta"), "agents=v1");
  assert.equal(requests[1].redirect, "error");
  assert.deepEqual(commandCall.args, ["status", "--profile", "default"]);
  assert.equal(commandCall.env.OPENAI_API_KEY, undefined);
});

test("reports missing conventional credential files and skips the authenticated read", async (t) => {
  const root = await mkdtemp(join(testRoot, "agents-core-doctor-missing-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const { fetchImpl, requests } = await successfulFetchRecorder();
  const result = await runScenario({
    env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
    cwd: root,
    homeDir: root,
    fetchImpl,
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.match(result.stdout, /Caller token: file is missing or unreadable/);
  assert.match(result.stdout, /authenticated read skipped because no valid caller token/);
  assert.equal(requests.length, 1);
});

test("refuses to read a group/world-accessible token file", async (t) => {
  const state = await createLocalState(t);
  await chmod(join(state.stateDir, "web-token"), 0o644);
  const { fetchImpl, requests } = await successfulFetchRecorder();
  const result = await runScenario({
    env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl,
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.match(result.stdout, /Caller token: file is group\/world accessible/);
  assert.equal(requests.length, 1);
  assert.doesNotMatch(result.stdout, new RegExp(state.stateDir.replaceAll("/", "\\/")));
});

test("accepts an issued caller token using the authenticated GET probe", async (t) => {
  const root = await mkdtemp(join(testRoot, "agents-core-doctor-inline-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const { fetchImpl, requests } = await successfulFetchRecorder();
  const result = await runScenario({
    env: {
      OAC_WEB_DEV_PROXY_TARGET: fixtureTarget,
      OAC_WEB_DEV_PROXY_TOKEN: fixtureToken,
    },
    cwd: root,
    homeDir: root,
    fetchImpl,
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.ok);
  assert.match(result.stdout, /Core API authenticated; basic Agent resource envelope parsed/);
  assert.equal(requests.length, 2);
});

test("treats conflicting server-side token sources as an actionable configuration failure", async (t) => {
  const state = await createLocalState(t);
  const { fetchImpl } = await successfulFetchRecorder();
  const result = await runScenario({
    env: {
      OAC_WEB_DEV_PROXY_TARGET: fixtureTarget,
      OAC_WEB_DEV_PROXY_TOKEN: fixtureToken,
      OAC_WEB_DEV_PROXY_TOKEN_FILE: join(state.stateDir, "web-token"),
    },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl,
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.match(result.stdout, /choose only one server-side token source/);
});

test("distinguishes an unreachable Core without retrying", async (t) => {
  const state = await createLocalState(t);
  let calls = 0;
  const result = await runScenario({
    env: { OAC_WEB_DEV_PROXY_TARGET: "http://127.0.0.1:1" },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl: async () => {
      calls += 1;
      throw new TypeError("synthetic connection refused");
    },
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.match(result.stdout, /Core is unreachable or the liveness request timed out/);
  assert.match(result.stdout, /authenticated read skipped because Core was unreachable/);
  assert.equal(calls, 1);
});

test("distinguishes a 401 from liveness and discards the response body", async (t) => {
  const state = await createLocalState(t);
  const { fetchImpl } = await successfulFetchRecorder({
    apiStatus: 401,
    apiBody: JSON.stringify({ error: { message: "session-private-marker", code: "invalid_api_key" } }),
  });
  const result = await runScenario({
    env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl,
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.match(result.stdout, /authentication was rejected \(HTTP 401\)/);
  assert.doesNotMatch(result.stdout, /session-private-marker|invalid_api_key/);
});

test("accepts only HTTP 200 for health and authenticated Agents reads", async (t) => {
  const state = await createLocalState(t);
  for (const status of [202, 206]) {
    await t.test(`health HTTP ${status}`, async () => {
      const { fetchImpl, requests } = await successfulFetchRecorder({ healthStatus: status });
      const result = await runScenario({
        env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
        cwd: state.root,
        homeDir: state.homeDir,
        fetchImpl,
      });

      assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
      assert.match(result.stdout, new RegExp(`Core health endpoint returned HTTP ${status}`));
      assert.deepEqual(requests.map(({ method }) => method), ["GET", "GET"]);
    });

    await t.test(`Agents HTTP ${status}`, async () => {
      const { fetchImpl, requests } = await successfulFetchRecorder({ apiStatus: status });
      const result = await runScenario({
        env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
        cwd: state.root,
        homeDir: state.homeDir,
        fetchImpl,
      });

      assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
      assert.match(result.stdout, new RegExp(`basic Agents read failed \\(HTTP ${status}\\)`));
      assert.deepEqual(requests.map(({ method }) => method), ["GET", "GET"]);
    });
  }
});

test("accepts a canonical non-empty page with additive and unknown tool variants", async (t) => {
  const state = await createLocalState(t);
  const { fetchImpl, requests } = await successfulFetchRecorder({
    apiBody: JSON.stringify(canonicalAgentPage(canonicalAgent({
      model: "",
      multi_agent: { enabled: true, max_concurrent_subagents: 6, additive_nested: true },
      reasoning: { effort: "max", summary: "detailed", additive_nested: true },
      service_tier: "fast",
      text: {
        format: { type: "json_schema", schema: { type: "object" }, additive_nested: true },
        verbosity: "high",
        additive_nested: true,
      },
      tools: [
        { type: "function", name: "", description: "", parameters: {}, defer_loading: false },
        { type: "tool_search", additive_nested: true },
        { type: "programmatic_tool_calling", enabled: true },
        canonicalMCPTool({
          transport: {
            type: "http",
            server_url: "https://mcp.fixture.invalid/tools",
            headers: {},
            additive_nested: true,
          },
          additive_nested: true,
        }),
        canonicalMCPTool({
          server_label: "records-without-headers",
          transport: { type: "http", server_url: "https://mcp.fixture.invalid/no-headers" },
        }),
        { type: "future_tool", additive_nested: true },
      ],
      additive_agent: true,
    }), { additive_page: true })),
  });
  const result = await runScenario({
    env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl,
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.ok);
  assert.match(result.stdout, /Core API authenticated; basic Agent resource envelope parsed/);
  assert.match(
    result.stdout,
    /tool\/Web compatibility, full protocol compatibility, and execution readiness remain unknown/,
  );
  assert.equal(requests.length, 2);
});

test("rejects malformed or non-canonical Agents list pages", async (t) => {
  const state = await createLocalState(t);
  const emptyPage = {
    object: "list",
    data: [],
    first_id: null,
    last_id: null,
    has_more: false,
  };
  const missingModel = canonicalAgent();
  delete missingModel.model;
  const scenarios = [
    ["wrong list object", { ...emptyPage, object: "agents" }],
    ["missing list object", Object.fromEntries(Object.entries(emptyPage).filter(([key]) => key !== "object"))],
    ["null Agent", canonicalAgentPage(null, { first_id: null, last_id: null })],
    ["missing Agent field", canonicalAgentPage(missingModel)],
    ["wrong Agent object", canonicalAgentPage(canonicalAgent({ object: "agent.snapshot" }))],
    ["wrong Agent field type", canonicalAgentPage(canonicalAgent({ created_at: "1789438200" }))],
    ["wrong nested Agent field type", canonicalAgentPage(canonicalAgent({
      multi_agent: { enabled: "false", max_concurrent_subagents: null },
    }))],
    ["null tool", canonicalAgentPage(canonicalAgent({ tools: [null] }))],
    ["missing tool discriminant", canonicalAgentPage(canonicalAgent({ tools: [{}] }))],
    ["empty tool discriminant", canonicalAgentPage(canonicalAgent({ tools: [{ type: "" }] }))],
    ["incomplete function tool", canonicalAgentPage(canonicalAgent({
      tools: [{ type: "function", name: "lookup", description: "", parameters: {} }],
    }))],
    ["wrong function tool field type", canonicalAgentPage(canonicalAgent({
      tools: [{ type: "function", name: 3, description: "", parameters: {}, defer_loading: false }],
    }))],
    ["incomplete programmatic tool", canonicalAgentPage(canonicalAgent({
      tools: [{ type: "programmatic_tool_calling" }],
    }))],
    ["wrong programmatic tool field type", canonicalAgentPage(canonicalAgent({
      tools: [{ type: "programmatic_tool_calling", enabled: "true" }],
    }))],
    ["incomplete MCP tool", canonicalAgentPage(canonicalAgent({
      tools: [{ type: "mcp" }],
    }))],
    ["empty MCP server label", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ server_label: "" })],
    }))],
    ["whitespace-only MCP server label", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ server_label: "   " })],
    }))],
    ["wrong MCP transport type", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({
        transport: { type: "stdio", server_url: "https://mcp.fixture.invalid/tools", headers: {} },
      })],
    }))],
    ["credential-bearing MCP server URL", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({
        transport: { type: "http", server_url: "https://user@mcp.fixture.invalid/tools", headers: {} },
      })],
    }))],
    ["MCP server URL with an empty query", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({
        transport: { type: "http", server_url: "https://mcp.fixture.invalid/tools?", headers: {} },
      })],
    }))],
    ["MCP server URL with surrounding whitespace", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({
        transport: { type: "http", server_url: " https://mcp.fixture.invalid/tools", headers: {} },
      })],
    }))],
    ["wrong MCP header field type", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({
        transport: { type: "http", server_url: "https://mcp.fixture.invalid/tools", headers: [] },
      })],
    }))],
    ["nonempty saved MCP headers", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({
        transport: {
          type: "http",
          server_url: "https://mcp.fixture.invalid/tools",
          headers: { authorization: "secret-marker" },
        },
      })],
    }))],
    ["wrong MCP allow-list item", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ allowed_tools: [""] })],
    }))],
    ["wrong MCP connection origin", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ connection_origin: "environment" })],
    }))],
    ["wrong MCP credential field type", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ credential_id: 7 })],
    }))],
    ["wrong MCP request metadata type", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ request_metadata: [] })],
    }))],
    ["nonempty saved MCP request metadata", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ request_metadata: { private: "marker" } })],
    }))],
    ["wrong MCP required field type", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ required: "false" })],
    }))],
    ["unsupported required MCP server", canonicalAgentPage(canonicalAgent({
      tools: [canonicalMCPTool({ required: true })],
    }))],
    ["inconsistent disabled multi-agent maximum", canonicalAgentPage(canonicalAgent({
      multi_agent: { enabled: false, max_concurrent_subagents: 4 },
    }))],
    ["missing enabled multi-agent maximum", canonicalAgentPage(canonicalAgent({
      multi_agent: { enabled: true, max_concurrent_subagents: null },
    }))],
    ["invalid cursor type", { ...emptyPage, first_id: 7 }],
    ["empty page with cursor", { ...emptyPage, first_id: "agent_fixture", last_id: "agent_fixture" }],
    ["empty page claiming more results", { ...emptyPage, has_more: true }],
    ["non-empty page with mismatched cursor", canonicalAgentPage(canonicalAgent(), { last_id: "agent_other" })],
    ["missing cursor", Object.fromEntries(Object.entries(emptyPage).filter(([key]) => key !== "last_id"))],
    ["more than requested limit", canonicalAgentPage(canonicalAgent(), {
      data: [canonicalAgent(), canonicalAgent({ id: "agent_second" })],
      last_id: "agent_second",
    })],
  ];

  for (const [name, payload] of scenarios) {
    await t.test(name, async () => {
      const { fetchImpl, requests } = await successfulFetchRecorder({ apiBody: JSON.stringify(payload) });
      const result = await runScenario({
        env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
        cwd: state.root,
        homeDir: state.homeDir,
        fetchImpl,
      });

      assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
      assert.match(result.stdout, /authenticated response did not match the expected list contract/);
      assert.equal(requests.length, 2);
    });
  }
});

test("does not read or authorize from a token file when private permissions cannot be proven", async (t) => {
  const state = await createLocalState(t);
  const { fetchImpl, requests } = await successfulFetchRecorder();
  const result = await runScenario({
    env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
    cwd: state.root,
    homeDir: state.homeDir,
    platform: "win32",
    fetchImpl,
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.match(result.stdout, /owner-only permissions cannot be verified on this platform; file was not read/);
  assert.doesNotMatch(result.stdout, /file is present with private permissions/);
  assert.deepEqual(requests.map(({ url }) => url.pathname), ["/healthz"]);
  assert.equal(requests[0].headers.has("authorization"), false);
  assert.doesNotMatch(result.stdout, new RegExp(fixtureToken));
});

test("loads Vite-style proxy dotenv configuration without exposing unrelated values", async (t) => {
  const state = await createLocalState(t);
  const { fetchImpl, requests } = await successfulFetchRecorder();
  await writeFile(join(state.root, ".env"), "OAC_WEB_DEV_PROXY_TARGET=https://base.fixture.invalid\n");
  await writeFile(
    join(state.root, ".env.local"),
    [
      "OAC_WEB_DEV_PROXY_TARGET='https://dotenv.fixture.invalid' # local override",
      "OAC_WEB_DEV_PROXY_TOKEN_FILE=${HOME}/.oac/dev/web-token",
      "OPENAI_API_KEY=provider-secret-marker",
    ].join("\n"),
    { mode: 0o600 },
  );
  const result = await runScenario({
    env: { HOME: state.homeDir },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl,
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.ok);
  assert.equal(requests[0].url.origin, "https://dotenv.fixture.invalid");
  assert.doesNotMatch(result.stdout, /provider-secret-marker/);
});

for (const [retiredName, replacement] of [
  ["AGENTS_API_PROXY_TARGET", "OAC_WEB_DEV_PROXY_TARGET"],
  ["AGENTS_API_PROXY_TOKEN", "OAC_WEB_DEV_PROXY_TOKEN"],
  ["AGENTS_API_PROXY_TOKEN_FILE", "OAC_WEB_DEV_PROXY_TOKEN_FILE"],
]) {
  for (const value of ["", "retired-private-value-marker"]) {
    test(`rejects retired ${retiredName} (${value ? "set" : "empty"}) before reads or daemon inspection`, async (t) => {
      const state = await createLocalState(t);
      const result = await runScenario({
        env: { [retiredName]: value, [replacement]: "current-private-value-marker" },
        cwd: state.root,
        homeDir: state.homeDir,
        fetchImpl: async () => assert.fail("retired settings must not make requests"),
        runCommand: async () => assert.fail("retired settings must not inspect a daemon"),
      });
      assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
      assert.deepEqual(result.result.checks, [{
        level: "FAIL", layer: "Configuration",
        message: `Retired Web settings: ${retiredName} is no longer supported; use ${replacement}.`,
      }]);
      assert.doesNotMatch(result.stdout + result.stderr, /private-value-marker|fixture-bearer/);
      assert.equal(result.stderr, "");
    });
  }
}

for (const file of [".env", ".env.local", ".env.development", ".env.development.local"]) {
  test(`rejects all retired proxy settings in ${file} without expanding or printing their values`, async (t) => {
    const state = await createLocalState(t);
    await writeFile(join(state.root, file), [
      "AGENTS_API_PROXY_TARGET=https://${OPENAI_API_KEY}.invalid",
      "AGENTS_API_PROXY_TOKEN=retired-token-marker",
      "AGENTS_API_PROXY_TOKEN_FILE=/private/retired-file-marker",
    ].join("\n"));
    const result = await runScenario({
      env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget, OPENAI_API_KEY: "provider-secret-marker" },
      cwd: state.root,
      homeDir: state.homeDir,
      fetchImpl: async () => assert.fail("retired settings must not make requests"),
      runCommand: async () => assert.fail("retired settings must not inspect a daemon"),
    });
    assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
    assert.match(result.stdout, /AGENTS_API_PROXY_TARGET is no longer supported; use OAC_WEB_DEV_PROXY_TARGET/);
    assert.match(result.stdout, /AGENTS_API_PROXY_TOKEN is no longer supported; use OAC_WEB_DEV_PROXY_TOKEN/);
    assert.match(result.stdout, /AGENTS_API_PROXY_TOKEN_FILE is no longer supported; use OAC_WEB_DEV_PROXY_TOKEN_FILE/);
    assert.doesNotMatch(result.stdout + result.stderr, /retired-token-marker|retired-file-marker|provider-secret-marker|fixture-bearer/);
    assert.equal(result.result.checks.length, 1);
  });
}

for (const source of ["environment", ".env"]) {
  for (const value of ["", "retired-private-value-marker"]) {
    test(`preserves ${source} retirement guidance (${value ? "set" : "empty"}) when a later dotenv path is a directory`, async (t) => {
      const state = await createLocalState(t);
      await mkdir(join(state.root, ".env.local"));
      const env = source === "environment" ? { AGENTS_API_PROXY_TOKEN: value } : {};
      if (source === ".env") await writeFile(join(state.root, ".env"), `AGENTS_API_PROXY_TOKEN=${value}\n`);
      const result = await runScenario({
        env, cwd: state.root, homeDir: state.homeDir,
        fetchImpl: async () => assert.fail("invalid configuration must not make requests"),
        runCommand: async () => assert.fail("invalid configuration must not inspect a daemon"),
      });
      assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
      assert.deepEqual(result.result.checks, [{
        level: "FAIL", layer: "Configuration",
        message: "Retired Web settings: AGENTS_API_PROXY_TOKEN is no longer supported; use OAC_WEB_DEV_PROXY_TOKEN.",
      }]);
      assert.doesNotMatch(result.stdout + result.stderr, /Caller token|retired-private-value-marker|fixture-bearer/);
    });
  }
}

test("stops on an unreadable configuration without inspecting conventional credentials or a daemon", async (t) => {
  const state = await createLocalState(t);
  await mkdir(join(state.root, ".env.local"));
  const result = await runScenario({
    cwd: state.root, homeDir: state.homeDir,
    fetchImpl: async () => assert.fail("invalid configuration must not make requests"),
    runCommand: async () => assert.fail("invalid configuration must not inspect a daemon"),
  });
  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.deepEqual(result.result.checks, [{
    level: "FAIL", layer: "Configuration", message: "local environment configuration is unreadable or unsafe.",
  }]);
  assert.doesNotMatch(result.stdout + result.stderr, /Caller token|fixture-bearer/);
});

test("never falls back to the retired conventional token file", async (t) => {
  const state = await createLocalState(t);
  await rm(join(state.stateDir, "web-token"));
  const retiredDirectory = join(state.homeDir, ".parsar", "agents-api");
  await mkdir(retiredDirectory, { recursive: true });
  await writeFile(join(retiredDirectory, "web-token"), fixtureToken, { mode: 0o600 });
  const { fetchImpl, requests } = await successfulFetchRecorder();
  const result = await runScenario({
    env: { OAC_WEB_DEV_PROXY_TARGET: fixtureTarget },
    cwd: state.root, homeDir: state.homeDir, fetchImpl,
  });
  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.match(result.stdout, /Caller token: file is missing or unreadable/);
  assert.deepEqual(requests.map(({ url }) => url.pathname), ["/healthz"]);
  assert.equal(requests[0].headers.has("authorization"), false);
});

test("fails closed when a network target tries to expand an unrelated secret", async (t) => {
  const state = await createLocalState(t);
  await writeFile(
    join(state.root, ".env.local"),
    [
      "OPENAI_API_KEY=provider-secret-marker",
      "OAC_WEB_DEV_PROXY_TARGET=https://${OPENAI_API_KEY}.invalid",
      "OAC_WEB_DEV_PROXY_TOKEN_FILE=${HOME}/.oac/dev/web-token",
    ].join("\n"),
    { mode: 0o600 },
  );
  let requests = 0;
  const result = await runScenario({
    env: { HOME: state.homeDir },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl: async () => {
      requests += 1;
      return new Response(JSON.stringify({ status: "ok" }));
    },
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.equal(requests, 0);
  assert.match(result.stdout, /local environment configuration is unreadable or unsafe/);
  assert.doesNotMatch(result.stdout, /provider-secret-marker|OPENAI_API_KEY/);
});

test("uses an explicit Parsar checkout only for an allowlisted daemon status command", async (t) => {
  const state = await createLocalState(t);
  const parsarPath = join(state.root, "private-parsar-checkout");
  await mkdir(join(parsarPath, "apps", "parsar-daemon", "cmd", "parsar-daemon"), { recursive: true });
  await writeFile(join(parsarPath, "go.mod"), "module fixture.invalid/parsar\n");
  await writeFile(join(parsarPath, "apps", "parsar-daemon", "cmd", "parsar-daemon", "main.go"), "package main\n");
  const { fetchImpl } = await successfulFetchRecorder();
  let commandCall;
  const result = await runScenario({
    argv: ["--parsar", parsarPath, "--profile", "fixture-profile"],
    env: {
      OAC_WEB_DEV_PROXY_TARGET: fixtureTarget,
      HOME: state.homeDir,
      PATH: "/synthetic/bin",
      OPENAI_API_KEY: "provider-secret-marker",
    },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl,
    runCommand: async (call) => {
      commandCall = call;
      return { code: 0, stdout: await fixture("daemon-status-absent.txt"), stderr: "" };
    },
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.ok);
  assert.equal(commandCall.command, "go");
  assert.equal(commandCall.cwd, parsarPath);
  assert.deepEqual(commandCall.args, [
    "run",
    "./apps/parsar-daemon/cmd/parsar-daemon",
    "status",
    "--profile",
    "fixture-profile",
  ]);
  assert.equal(commandCall.env.OPENAI_API_KEY, undefined);
  assert.doesNotMatch(result.stdout, /private-parsar-checkout|fixture-profile/);
});

test("parses only allowlisted daemon state and treats absence as non-fatal", async () => {
  assert.equal(parseDaemonStatus(await fixture("daemon-status-paired.txt")), "paired-background-observed");
  assert.equal(parseDaemonStatus(await fixture("daemon-status-absent.txt")), "not-observed");
  assert.equal(parseDaemonStatus("paired: ERROR — /synthetic/private/error"), "unknown");
});

test("never includes credential, response, URL suffix, provider, or private-path markers", async (t) => {
  const root = await mkdtemp(join(testRoot, "agents-core-doctor-redaction-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const privateDir = join(root, "synthetic-private-doctor-state");
  await mkdir(privateDir, { recursive: true, mode: 0o700 });
  const token = "fixture-super-secret-token";
  const { fetchImpl } = await successfulFetchRecorder({
    apiBody: JSON.stringify({
      object: "list",
      data: [canonicalAgent({ name: "session-private-marker" })],
      first_id: "agent_fixture",
      last_id: "agent_fixture",
      has_more: false,
    }),
  });
  const result = await runScenario({
    env: {
      OAC_WEB_DEV_PROXY_TARGET: fixtureTarget,
      OAC_WEB_DEV_PROXY_TOKEN: token,
      OPENAI_API_KEY: "provider-secret-marker",
      HOME: root,
      PATH: "/synthetic/bin",
    },
    cwd: root,
    homeDir: root,
    fetchImpl,
    runCommand: async () => ({
      code: 0,
      stdout: await fixture("daemon-status-paired.txt"),
      stderr: "Authorization: Bearer fixture-super-secret-token",
    }),
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.ok);
  const combined = `${result.stdout}\n${result.stderr}`;
  for (const marker of JSON.parse(await fixture("redaction-corpus.json"))) {
    assert.equal(combined.includes(marker), false, `report leaked marker: ${marker}`);
  }
});

test("rejects a malformed target before conventional credential, network or daemon checks", async (t) => {
  const state = await createLocalState(t);
  const result = await runScenario({
    env: { OAC_WEB_DEV_PROXY_TARGET: "not-a-url" },
    cwd: state.root, homeDir: state.homeDir,
    fetchImpl: async () => assert.fail("invalid targets must not make requests"),
    runCommand: async () => assert.fail("invalid targets must not inspect a daemon"),
  });
  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.deepEqual(result.result.checks, [{
    level: "FAIL", layer: "Configuration", message: "proxy target must be credential-free HTTPS or a loopback HTTP origin.",
  }]);
  assert.doesNotMatch(result.stdout + result.stderr, /Caller token|fixture-bearer|not-a-url/);
});

test("rejects credential-bearing target suffixes without reflecting them", async (t) => {
  const state = await createLocalState(t);
  const result = await runScenario({
    env: {
      OAC_WEB_DEV_PROXY_TARGET: "https://core.fixture.invalid/?access=query-secret-marker#fragment-secret-marker",
      HOME: state.homeDir,
    },
    cwd: state.root,
    homeDir: state.homeDir,
    fetchImpl: async () => assert.fail("invalid targets must not be requested"),
    runCommand: async () => assert.fail("invalid targets must not inspect a daemon"),
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.diagnosticFailure);
  assert.match(result.stdout, /credential-free HTTPS or a loopback HTTP origin/);
  assert.equal(result.result.checks.length, 1);
  assert.doesNotMatch(result.stdout, /Caller token|query-secret-marker|fragment-secret-marker/);
});

test("invalid options use exit 2 without reflecting untrusted argv", async () => {
  const result = await runScenario({
    argv: ["--profile", "../../query-secret-marker"],
    env: {},
    cwd: process.cwd(),
    homeDir: testRoot,
    fetchImpl: async () => assert.fail("invalid options must not make a request"),
  });

  assert.equal(result.result.exitCode, CORE_DOCTOR_EXIT_CODES.usageOrInternalError);
  assert.equal(result.stdout, "");
  assert.match(result.stderr, /Invalid Core Doctor options/);
  assert.doesNotMatch(result.stderr, /query-secret-marker/);
});
