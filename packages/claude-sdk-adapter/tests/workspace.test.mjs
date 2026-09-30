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

test("workspace native options bypass isolation and preserve selected tool inventory", t => {
  const { dirs, config } = fixture(t);
  const profile = new WorkspaceProfile(dirs.workspace, config);
  const options = profile.options;
  assert.deepEqual(options.tools, ["Bash", "Read", "Edit"]);
  assert.deepEqual(options.allowedTools, []);
  assert.deepEqual(options.settingSources, []);
  assert.deepEqual(options.mcpServers, {});
  assert.equal(options.strictMcpConfig, true);
  assert.equal(options.permissionMode, "default");
  assert.equal(options.persistSession, true);
  assert.equal(options.sandbox.enabled, false);
  assert.deepEqual(options.sandbox, { enabled: false });
  assert.equal(options.allowDangerouslySkipPermissions, undefined);
  assert.deepEqual(options.settings, {});
  profile.verify(["Read", "Edit", "Bash"], []);
  for (const tools of [[], ["Bash", "Read", "Read"], ["Bash", "Read", "Write"], ["Bash", "Read", "Edit", "Agent"]]) {
    assert.throws(() => profile.verify(tools, []), /unexpected native workspace inventory/);
  }
  assert.throws(() => profile.verify(options.tools, [{ name: "untrusted", status: "connected" }]), /unexpected native workspace inventory/);
});

test("structured workspace admits only its configured native terminal tool", async t => {
  const { dirs, config, request } = fixture(t);
  const output_format = { type: "json_schema", schema: { type: "object" } };
  const configured = { ...request, observe_messages: true, output_format };
  assert.deepEqual(parseStart(JSON.stringify(configured)), configured);
  const ordinary = new WorkspaceProfile(dirs.workspace, config);
  const structured = new WorkspaceProfile(dirs.workspace, config, [], undefined, undefined, true);
  const { canUseTool: ordinaryPermission, hooks: ordinaryHooks, ...ordinaryOptions } = ordinary.options;
  const { canUseTool: structuredPermission, hooks: structuredHooks, ...structuredOptions } = structured.options;
  assert.deepEqual(structuredOptions, ordinaryOptions);
  const inventory = ["Bash", "Read", "Edit", "StructuredOutput"];
  structured.verify(inventory, []);
  assert.throws(() => ordinary.verify(inventory, []));
  assert.throws(() => structured.verify(["Bash", "Read", "Edit"], []));
  assert.throws(() => structured.verify([...inventory, "Write"], []));
  const context = { signal: new AbortController().signal, toolUseID: "terminal", requestId: "request" };
  assert.equal((await ordinary.canUseTool("StructuredOutput", {}, context)).behavior, "deny");
  assert.equal((await structured.canUseTool("StructuredOutput", {}, context)).behavior, "allow");
  for (const extra of [{ agentID: "child" }, { signal: AbortSignal.abort() }]) {
    assert.equal((await structured.canUseTool("StructuredOutput", {}, { ...context, ...extra })).behavior, "deny");
  }
  const hook = { hook_event_name: "PreToolUse", tool_name: "StructuredOutput", tool_input: {}, tool_use_id: "terminal" };
  assert.deepEqual(await structured.beforeTool(hook, "terminal", context), {});
  for (const [input, id, ctx] of [[{ ...hook, agent_id: "child" }, "terminal", context], [hook, "wrong", context], [hook, "terminal", { ...context, signal: AbortSignal.abort() }]]) {
    assert.equal((await structured.beforeTool(input, id, ctx)).hookSpecificOutput.permissionDecision, "deny");
  }
});

test("workspace permissions preserve host authority and synchronous command ownership", async t => {
  const { dirs, config } = fixture(t);
  writeFileSync(join(dirs.workspace, "file.txt"), "fixture");
  symlinkSync(dirs.protected, join(dirs.workspace, "escape"));
  symlinkSync(join(dirs.protected, "missing"), join(dirs.workspace, "dangling"));
  symlinkSync(join(dirs.workspace, "file.txt"), join(dirs.workspace, "inside"));
  const profile = new WorkspaceProfile(dirs.workspace, config);
  const options = { signal: new AbortController().signal, toolUseID: "tool", requestId: "request" };
  const hook = async (name, input) => profile.beforeTool({ hook_event_name: "PreToolUse", session_id: "native", cwd: dirs.workspace,
    transcript_path: join(dirs.state, "session"), tool_name: name, tool_input: input, tool_use_id: "tool" }, "tool", options);
  for (const [name, input] of [["Bash", { command: "printf value" }], ["Bash", { command: "true", dangerouslyDisableSandbox: true }], ["Read", { file_path: "file.txt" }],
    ["Read", { file_path: "inside" }], ["Edit", { file_path: "new/file.txt", old_string: "", new_string: "value" }]]) {
    const permission = await profile.canUseTool(name, input, options);
    assert.equal(permission.behavior, "allow");
    if (name === "Bash") assert.deepEqual(await hook(name, input), {});
    else {
      assert.equal(permission.updatedInput.file_path, join(dirs.workspace, input.file_path));
      assert.deepEqual((await hook(name, input)).hookSpecificOutput.updatedInput, permission.updatedInput);
    }
  }
  for (const [name, input] of [["Bash", { command: "true", run_in_background: true }],
    ["Bash", { command: "true", run_in_background: "false" }],
    ["Write", { file_path: "file.txt" }]]) {
    assert.equal((await profile.canUseTool(name, input, options)).behavior, "deny");
    assert.equal((await hook(name, input)).hookSpecificOutput.permissionDecision, "deny");
  }
  assert.equal((await profile.canUseTool("Bash", { command: "true" }, { ...options, agentID: "child" })).behavior, "deny");
  assert.equal((await profile.canUseTool("Bash", { command: "true" }, { ...options, signal: AbortSignal.abort() })).behavior, "deny");
});

test("Runtime rejects network isolation requests", t => {
 const {dirs,config}=fixture(t);
 assert.deepEqual(new WorkspaceProfile(dirs.workspace,{...config,network_access:"enabled"}).options.sandbox,{enabled:false});
 for(const network_access of ["disabled","restricted"]) assert.throws(()=>parseWorkspace({...config,network_access,allowed_domains:network_access==="restricted"?["example.com"]:[]},dirs.workspace),/invalid_request/);
});

test("workspace functions retain native sandbox and exact tool authority", async t => {
  const { dirs, config, request } = fixture(t);
  const functions = [{ name: "lookup", description: "Lookup", parameters: { type: "object" } }];
  assert.deepEqual(parseStart(JSON.stringify({ ...request, functions })).functions, functions);
  const profile = new WorkspaceProfile(dirs.workspace, config, ["mcp__functions__lookup"]);
  profile.verify(["Bash", "Read", "Edit", "mcp__functions__lookup"], [{ name: "functions", status: "connected" }]);
  for (const servers of [[], [{ name: "functions", status: "failed" }], [{ name: "external", status: "connected" }]]) {
    assert.throws(() => profile.verify(["Bash", "Read", "Edit", "mcp__functions__lookup"], servers));
  }
  assert.throws(() => profile.verify(["Bash", "Read", "Edit", "mcp__functions__unknown"], [{ name: "functions", status: "connected" }]));
  const controller = new AbortController();
  const input = { id: "fixture" };
  const options = { signal: controller.signal };
  assert.deepEqual(await profile.canUseTool("mcp__functions__lookup", input, options), { behavior: "allow", updatedInput: input });
  assert.equal((await profile.canUseTool("mcp__functions__unknown", input, options)).behavior, "deny");
  assert.equal((await profile.canUseTool("mcp__functions__lookup", input, { ...options, agentID: "child" })).behavior, "deny");
  const event = { hook_event_name: "PreToolUse", tool_name: "mcp__functions__lookup", tool_input: input, tool_use_id: "call" };
  assert.deepEqual(await profile.beforeTool(event, "call", options), {});
  assert.equal((await profile.beforeTool({ ...event, tool_name: "mcp__external__lookup" }, "call", options)).hookSpecificOutput.permissionDecision, "deny");
  assert.equal((await profile.canUseTool("Bash", { command: "true", dangerouslyDisableSandbox: true }, options)).behavior, "allow");
  assert.equal((await profile.canUseTool("Read", { file_path: dirs.state + "/history" }, options)).behavior, "allow");
  controller.abort();
  assert.equal((await profile.canUseTool("mcp__functions__lookup", input, options)).behavior, "deny");
});

test("initialized environment is ordinary child environment on every layout", t => {
 const {dirs,config}=fixture(t);
 {
  const profile=new WorkspaceProfile(dirs.workspace,{...config,tool_env:{USER_VALUE:"initialized",HOME:"/wrong",CLAUDE_CONFIG_DIR:"/wrong",home:"/wrong"}});
  assert.equal(profile.options.env.USER_VALUE,"initialized");
  assert.equal(profile.options.env.HOME,dirs.home);
  assert.equal(profile.options.env.CLAUDE_CONFIG_DIR,dirs.state);
  assert.equal(profile.options.env.home,undefined);
  assert.deepEqual(profile.options.sandbox,{enabled:false});
 }
});

test("host tools can access paths outside the workspace", async t => {
 const {dirs,config}=fixture(t);
 const profile=new WorkspaceProfile(dirs.workspace,config);
 for(const file_path of ["../protected/value",join(dirs.home,"credentials")]) {
  assert.equal((await profile.canUseTool("Read",{file_path},{signal:new AbortController().signal})).behavior,"allow");
 }
});


test("workspace discovery preserves native file tools and the function boundary", async t => {
  const { dirs, config, request } = fixture(t);
  const functions = [{name:"lookup", description:"Lookup", parameters:{type:"object"}, defer_loading:true}];
  const configured = {...request, tool_search:true, functions};
  assert.deepEqual(parseStart(JSON.stringify(configured)), configured);
  const profile = new WorkspaceProfile(dirs.workspace, config, ["mcp__functions__lookup"], undefined, undefined, false, true);
  assert.deepEqual(profile.options.tools, ["Bash", "Read", "Edit", "ToolSearch"]);
  assert.deepEqual(profile.options.allowedTools, ["mcp__functions__lookup", "ToolSearch"]);
  const tools = [...profile.options.tools, "mcp__functions__lookup"];
  profile.verify(tools, [{name:"functions",status:"connected"}]);
  assert.throws(() => profile.verify(tools.filter(name => name !== "ToolSearch"), [{name:"functions",status:"connected"}]));
  const ordinary = new WorkspaceProfile(dirs.workspace, config);
  const context = {signal:new AbortController().signal, toolUseID:"search", requestId:"request"};
  assert.equal((await profile.canUseTool("ToolSearch", {query:"lookup"}, context)).behavior,"allow");
  assert.equal((await ordinary.canUseTool("ToolSearch", {query:"lookup"}, context)).behavior,"deny");
  assert.equal((await profile.canUseTool("mcp__functions__undeclared", {}, context)).behavior,"deny");
  assert.equal((await profile.canUseTool("ToolSearch", {}, {...context, agentID:"child"})).behavior,"deny");
  assert.equal((await profile.canUseTool("ToolSearch", {}, {...context, signal:AbortSignal.abort()})).behavior,"deny");
});
