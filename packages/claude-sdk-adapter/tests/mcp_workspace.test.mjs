import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MCPProfile } from "../dist/mcp.js";
import { parseEnvironmentMCP } from "../dist/mcp_environment.js";
import { immediateInput, parseStart } from "../dist/request.js";
import { WorkspaceProfile } from "../dist/workspace.js";

const stdio = { server_label: "installed", command: process.execPath, allowed_tools: null,
  args: ["runtime-mcp-exec", join(process.cwd(), "capabilities"), "plugins/installed", "installed"] };
const native = "mcp__installed__echo_v1";
const statuses = [{ name: "installed", status: "connected", tools: [{ name: "echo.v1" }] }];
const baseline = ["Bash", "Read", "Edit"];

function fixture(t, declarations = [stdio]) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "oac-mcp-workspace-")));
  const dirs = Object.fromEntries(["work", "home", "state", "scratch", "secrets", "deps", "capabilities"].map(name => {
    const path = join(root, name); mkdirSync(path); return [name, path];
  }));
  const config = { home: dirs.home, state: dirs.state, scratch: dirs.scratch,
    capability_root: dirs.capabilities,  env_names: ["ANTHROPIC_AUTH_TOKEN"], network_access: "enabled", mcp: declarations };
  const previous = process.env;
  process.env = { HOME: dirs.home, CLAUDE_CONFIG_DIR: dirs.state, ANTHROPIC_AUTH_TOKEN: "model-secret" };
  t.after(() => { process.env = previous; rmSync(root, { recursive: true, force: true }); });
  const request = { type: "start", input: [{ content: [{ type: "input_text", text: "fixture" }] }], model: "fixture", system_prompt: "", cwd: dirs.work, workspace: config };
  return { dirs, config, request };
}

test("installed MCP projection uses the common Runtime launcher", t => {
  const { request } = fixture(t);
  assert.deepEqual(parseStart(JSON.stringify(request)), request);
  assert.throws(() => parseStart(JSON.stringify({ ...request, observe_messages: true,
    output_format: { type: "json_schema", schema: { type: "object" } } })), /invalid_request/);
  assert.equal(immediateInput(request), undefined);
  assert.deepEqual(parseEnvironmentMCP([stdio]), [stdio]);
  for (const value of [[stdio, stdio], [{ ...stdio, command: "relative" }], [{ ...stdio, env: { TOKEN: "secret" } }],
    [{ ...stdio, args: ["-c", "untrusted"] }], [{ ...stdio, allowed_tools: ["*"] }],
    [{ ...stdio, server_url: "https://example.invalid" }], [{ ...stdio, args: [...stdio.args.slice(0, 2), "../escape", "installed"] }]]) {
    assert.throws(() => parseEnvironmentMCP(value), /invalid_request/);
  }
  for (const index of [1]) {
    for (const invalid of ["/", "relative", "/tmp/../escape", "/tmp/line\n"]) {
      const args = [...stdio.args]; args[index] = invalid;
      assert.throws(() => parseEnvironmentMCP([{ ...stdio, args }]), /invalid_request/);
    }
  }
  assert.throws(() => parseStart(JSON.stringify({ ...request, workspace: { ...request.workspace, network_access: "disabled" } })), /invalid_request/);
});

test("combined inventory admits exact MCP identities with host file authority", async t => {
  const { dirs, config } = fixture(t);
  const mcp = new MCPProfile([stdio], []);
  const workspace = new WorkspaceProfile(dirs.work, config, [], mcp);
  const signal = new AbortController().signal;
  const input = { hook_event_name: "PreToolUse", session_id: "session", tool_use_id: "call", tool_name: native, tool_input: {} };
  let ready = false;
  const waiting = workspace.beforeTool(input, "call", { signal }).then(value => { ready = true; return value; });
  await Promise.resolve();
  assert.equal(ready, false);
  workspace.verify([...baseline, native], statuses, "session");
  assert.deepEqual(await waiting, {});
  assert.deepEqual(mcp.identities.get(native), { server: "installed", name: "echo.v1" });
  assert.equal((await workspace.canUseTool(native, {}, { signal })).behavior, "allow");
  assert.equal((await workspace.canUseTool("mcp__installed__undeclared", {}, { signal })).behavior, "deny");
  assert.equal((await workspace.canUseTool("Read", { file_path: join(dirs.secrets, "model.key") }, { signal })).behavior, "allow");
  assert.equal((await workspace.canUseTool("Bash", { command: "pwd", dangerouslyDisableSandbox: true }, { signal })).behavior, "allow");
  assert.equal((await workspace.beforeTool({ ...input, tool_name: "Read", tool_input: { file_path: "ok.txt" } }, "call", { signal })).hookSpecificOutput.updatedInput.file_path, join(dirs.work, "ok.txt"));
  for (const fields of [{ session_id: "other" }, { agent_id: "child" }, { tool_name: "mcp__ambient__echo" }]) {
    assert.equal((await workspace.beforeTool({ ...input, ...fields }, "call", { signal })).hookSpecificOutput.permissionDecision, "deny");
  }
  assert.deepEqual(workspace.options.allowedTools, ["mcp__installed__*"]);
  assert.equal(mcp.servers.installed.alwaysLoad, true);
  assert.deepEqual(workspace.options.tools, baseline);
  assert.deepEqual(workspace.options.sandbox, {enabled:false});
  mcp.close();
  assert.equal((await workspace.canUseTool(native, {}, { signal })).behavior, "deny");
});

test("combined inventory rejects extra servers, tools and normalized identity collisions", t => {
  const { dirs, config } = fixture(t);
  for (const [tools, servers] of [
    [[...baseline, native, "Write"], statuses],
    [[...baseline, native], [...statuses, { name: "ambient", status: "connected", tools: [] }]],
    [[...baseline, native], [{ ...statuses[0], tools: [{ name: "echo.v1" }, { name: "echo_v1" }] }]],
    [[...baseline, native], [{ ...statuses[0], status: "pending" }]],
  ]) {
    const mcp = new MCPProfile([stdio], []);
    assert.throws(() => new WorkspaceProfile(dirs.work, config, [], mcp).verify(tools, servers, "session"));
    mcp.close();
  }
});

test("workspace bearer references reach native HTTP without leaking values into the request", t => {
  const reference = "OAC_RUNTIME_MCP_BEARER_ABCDEFGHIJKLMNOPQRSTUVWXYZ";
  const http = { server_label: "remote", server_url: "https://example.invalid/mcp", allowed_tools: null, bearer_token_env_var: reference };
  const { dirs, config } = fixture(t, [http]);
  process.env[reference] = "selected-user-token";
  process.env.OAC_RUNTIME_MCP_BEARER_UNSELECTED = "other-token";
  const mcp = new MCPProfile([http], []);
  const workspace = new WorkspaceProfile(dirs.work, config, [], mcp);
  assert.equal(workspace.options.env[reference], "selected-user-token");
  assert.equal(workspace.options.env.OAC_RUNTIME_MCP_BEARER_UNSELECTED, "other-token");
  assert.deepEqual(mcp.servers.remote.headers, { Authorization: `Bearer \${${reference}}` });
  assert.equal(JSON.stringify(mcp.servers).includes("selected-user-token"), false);
  delete process.env[reference];
  assert.throws(() => new WorkspaceProfile(dirs.work, config, [], mcp), /invalid_request/);
});

test("MCP identity validation preserves host functions and ordinary child environment", async t => {
  const { dirs, config } = fixture(t);
  const functions = ["mcp__functions__lookup"];
  const mcp = new MCPProfile([stdio], functions);
  const workspace = new WorkspaceProfile(dirs.work, { ...config, tool_env: { USER_VALUE: "initialized" } }, functions, mcp);
  workspace.verify([...baseline, native, ...functions], [...statuses, { name: "functions", status: "connected" }], "session");
  const signal = new AbortController().signal;
  const input = { hook_event_name: "PreToolUse", session_id: "session", tool_use_id: "call", tool_name: "Bash", tool_input: { command: "printf ok" } };
  assert.deepEqual(await workspace.beforeTool(input, "call", { signal }), {});
  assert.equal(workspace.options.env.USER_VALUE, "initialized");
  assert.equal((await workspace.canUseTool(functions[0], {}, { signal })).behavior, "allow");
  mcp.close();
});

test("public workspace HTTP preserves required startup and null versus empty allowlists", t => {
  const http = {server_label:"remote",server_url:"https://example.invalid/mcp",allowed_tools:[],required:true};
  const {dirs,config,request}=fixture(t,[http]);
  assert.deepEqual(parseStart(JSON.stringify(request)),request);
  for(const allowed_tools of [null,[],["prove"]]) {
    const declaration={...http,allowed_tools};
    assert.deepEqual(parseEnvironmentMCP([declaration]),[declaration]);
    const mcp=new MCPProfile([declaration],[]);
    const workspace=new WorkspaceProfile(dirs.work,{...config,mcp:[declaration]},[],mcp);
    assert.throws(()=>mcp.verifyRequired([{name:"remote",status:"pending"}]),/required/);
    const statuses=[{name:"remote",status:"connected",tools:[{name:"prove"},{name:"other"}]}];
    mcp.verifyRequired(statuses);
    const selected=allowed_tools===null?["prove","other"]:allowed_tools;
    workspace.verify([...baseline,...selected.map(name=>"mcp__remote__"+name)],statuses,"session");
    assert.equal(mcp.permits("mcp__remote__prove"),allowed_tools===null||allowed_tools.includes("prove"));
    assert.equal(mcp.permits("mcp__remote__other"),allowed_tools===null);
    mcp.close();
  }
});
