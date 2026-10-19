import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parseStart } from "../dist/adapter.js";
import { parseWorkspace, WorkspaceProfile } from "../dist/workspace.js";

function fixture(t) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "oac-workspace-")));
  const dirs = Object.fromEntries(["workspace", "home", "state", "scratch", "protected", "deps"].map(name => {
    const path = join(root, name);
    mkdirSync(path);
    return [name, path];
  }));
  const config = { home: dirs.home, state: dirs.state, scratch: dirs.scratch,
     env_names: ["ANTHROPIC_API_KEY", "HTTP_PROXY"] };
  const request = { type: "start", input: [{ content: [{ type: "input_text", text: "fixture" }] }], model: "fixture", system_prompt: "", cwd: dirs.workspace, workspace: config };
  const previous = process.env;
  process.env = { HOME: dirs.home, CLAUDE_CONFIG_DIR: dirs.state, ANTHROPIC_API_KEY: "fixture-secret",
    HTTP_PROXY: "http://fixture-proxy", UNSELECTED_CANARY: "must-not-inherit", NODE_OPTIONS: "unsafe",
    CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "0", ANTHROPIC_AUTH_TOKEN: "not-selected" };
  t.after(() => { process.env = previous; rmSync(root, { recursive: true, force: true }); });
  return { root, dirs, config, request };
}

test("workspace is explicit, typed and rejects external MCP declarations", t => {
  const { config, request } = fixture(t);
  assert.deepEqual(parseStart(JSON.stringify(request)), request);
  for (const fields of [{ mcp_http_servers: [] }, { functions: null }, { mcp_http_servers: null }]) {
    assert.throws(() => parseStart(JSON.stringify({ ...request, ...fields })), /invalid_request/);
  }
  for (const workspace of [null, [], {}, { ...config, native: {} }, { ...config, env: {} },
    { ...config, env_names: ["ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY"] },
    { ...config, env_names: ["NODE_OPTIONS"] }, { ...config, env_names: ["HOME"] }]) {
    assert.throws(() => parseStart(JSON.stringify({ ...request, workspace })), /invalid_request/);
  }
  const { workspace, ...ordinary } = request;
  assert.deepEqual(parseStart(JSON.stringify(ordinary)), ordinary);
  assert.equal(parseWorkspace(undefined, ordinary.cwd), undefined);
  assert.deepEqual(parseStart(JSON.stringify({ ...ordinary, functions: [], mcp_http_servers: [] })),
    { ...ordinary, functions: [], mcp_http_servers: [] });
});

test("workspace paths use ordinary host directories", t => {
 const {root,dirs,config}=fixture(t);
 for(const home of ["relative",join(root,"missing")]) assert.throws(()=>parseWorkspace({...config,home},dirs.workspace),/invalid_request/);
 const alias=join(root,"alias");symlinkSync(dirs.home,alias);
 assert.equal(parseWorkspace({...config,home:alias},dirs.workspace).home,alias);
 assert.equal(parseWorkspace({...config,scratch:dirs.home},dirs.workspace).scratch,dirs.home);
});

test("workspace environment copies only selected refs and fixed values", t => {
  const { dirs, config } = fixture(t);
  const options = new WorkspaceProfile(dirs.workspace, config).options;
  assert.equal(options.env.UNSELECTED_CANARY,"must-not-inherit");
  assert.equal(options.env.HOME,dirs.home);
  assert.equal(options.env.ANTHROPIC_API_KEY,"fixture-secret");

  delete process.env.ANTHROPIC_API_KEY;
  assert.throws(() => new WorkspaceProfile(dirs.workspace, config), /invalid_request/);
  process.env.ANTHROPIC_API_KEY = "fixture-secret";
  for (const key of ["HOME", "CLAUDE_CONFIG_DIR"]) {
    const previous = process.env[key];
    process.env[key] = "/incorrect";
    assert.throws(() => new WorkspaceProfile(dirs.workspace, config), /invalid_request/);
    process.env[key] = previous;
  }
  process.env.CLAUDE_CODE_PROJECT_DIR_NAME = "ambient-history-override";
  assert.throws(() => new WorkspaceProfile(dirs.workspace, config), /invalid_request/);
});
test("workspace runs the native tool preset and removes only what cannot run unattended", async t => {
  const { dirs, config } = fixture(t);
  const options = new WorkspaceProfile(dirs.workspace, config).options;
  assert.deepEqual(options.tools, { type: "preset", preset: "claude_code" });
  assert.deepEqual(options.disallowedTools, ["AskUserQuestion", "CronCreate", "CronDelete", "CronList", "ScheduleWakeup", "Monitor", "Workflow", "ListAgents", "Agent", "SendMessage"]);
  assert.equal(options.permissionMode, undefined);
  for (const key of ["allowedTools", "settingSources", "mcpServers", "strictMcpConfig", "persistSession", "sandbox", "settings", "allowDangerouslySkipPermissions"]) {
    assert.equal(options[key], undefined, key);
  }
  const signal = new AbortController().signal;
  for (const [name, input] of [["Bash", { command: "true" }], ["Write", { file_path: "file.txt" }], ["Read", { file_path: "../protected/value" }]]) {
    assert.deepEqual(await options.canUseTool(name, input, { signal }), { behavior: "allow", updatedInput: input });
  }
});

test("Core declarations remove subagent and search tools only when declared", t => {
  const { dirs, config } = fixture(t);
  const subagents = new WorkspaceProfile(dirs.workspace, config, undefined, {}).options.disallowedTools;
  assert.ok(!subagents.includes("Agent") && !subagents.includes("SendMessage") && !subagents.includes("WebSearch"));
  const disabled = new WorkspaceProfile(dirs.workspace, config, undefined, undefined, true).options.disallowedTools;
  assert.ok(disabled.includes("WebSearch") && disabled.includes("WebFetch"));
});

test("workspace functions are parsed", async t => {
  const { request } = fixture(t);
  const functions = [{ name: "lookup", description: "Lookup", parameters: { type: "object" } }];
  assert.deepEqual(parseStart(JSON.stringify({ ...request, functions })).functions, functions);
});

test("initialized environment is ordinary child environment on every layout", t => {
 const {dirs,config}=fixture(t);
 {
  const profile=new WorkspaceProfile(dirs.workspace,{...config,tool_env:{USER_VALUE:"initialized",HOME:"/wrong",CLAUDE_CONFIG_DIR:"/wrong",home:"/wrong"}});
  assert.equal(profile.options.env.USER_VALUE,"initialized");
  assert.equal(profile.options.env.HOME,dirs.home);
  assert.equal(profile.options.env.CLAUDE_CONFIG_DIR,dirs.state);
  assert.equal(profile.options.env.home,undefined);
 }
});

test("Core sets only the environment that cloud operation needs", t => {
  const { dirs, config } = fixture(t);
  const env = new WorkspaceProfile(dirs.workspace, config).options.env;
  for (const name of ["DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"]) assert.equal(env[name], undefined, name);
});

test("workspace discovery keeps the function boundary on the native tool set", t => {
  const { request } = fixture(t);
  const functions = [{name:"lookup", description:"Lookup", parameters:{type:"object"}, defer_loading:true}];
  const configured = {...request, tool_search:true, functions};
  assert.deepEqual(parseStart(JSON.stringify(configured)), configured);
});

test("web search control is the disabled or live mode", t => {
  const { request } = fixture(t);
  assert.equal(parseStart(JSON.stringify({ ...request, web_search: "live" })).web_search, "live");
  assert.throws(() => parseStart(JSON.stringify({ ...request, web_search: true })), /invalid_request/);
});
