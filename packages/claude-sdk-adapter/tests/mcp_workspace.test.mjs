import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MCPProfile } from "../dist/mcp.js";
import { parseEnvironmentMCP } from "../dist/mcp_environment.js";
import { parseRequest } from "../dist/request.js";
import { WorkspaceProfile } from "../dist/workspace.js";

const stdio = { server_label: "installed", command: process.execPath, allowed_tools: null,
  args: ["runtime-mcp-exec", join(process.cwd(), "capabilities"), "plugins/installed", "installed"] };
const native = "mcp__installed__echo_v1";
const statuses = [{ name: "installed", status: "connected", tools: [{ name: "echo.v1" }] }];
const nativePreset = ["Bash", "Read", "Edit", "Write"];

function fixture(t, declarations = [stdio]) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "oac-mcp-workspace-")));
  const dirs = Object.fromEntries(["work", "home", "state", "scratch", "secrets", "deps", "capabilities"].map(name => {
    const path = join(root, name); mkdirSync(path); return [name, path];
  }));
  const config = { home: dirs.home, state: dirs.state, scratch: dirs.scratch,
    capability_root: dirs.capabilities,  env_names: ["ANTHROPIC_AUTH_TOKEN"], mcp: declarations };
  const previous = process.env;
  process.env = { HOME: dirs.home, CLAUDE_CONFIG_DIR: dirs.state, ANTHROPIC_AUTH_TOKEN: "model-secret" };
  t.after(() => { process.env = previous; rmSync(root, { recursive: true, force: true }); });
  const request = { type: "executor_prepare", model: "fixture", system_prompt: "", cwd: dirs.work, workspace: config };
  return { dirs, config, request };
}

test("installed MCP projection uses the common Runtime launcher", t => {
  const { request } = fixture(t);
  assert.deepEqual(parseRequest(JSON.stringify(request)), request);
  const structured = { ...request, output_format: { type: "json_schema", schema: { type: "object" } } };
  assert.deepEqual(parseRequest(JSON.stringify(structured)), structured);
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
});

test("workspace admits declared MCP identities and leaves other native tools alone", async t => {
  const { dirs, config } = fixture(t);
  const mcp = new MCPProfile([stdio], []);
  const workspace = new WorkspaceProfile(dirs.work, config, mcp);
  const signal = new AbortController().signal;
  const input = { hook_event_name: "PreToolUse", session_id: "session", tool_use_id: "call", tool_name: native, tool_input: {} };
  let ready = false;
  const waiting = mcp.beforeTool(input, "call", { signal }).then(value => { ready = true; return value; });
  await Promise.resolve();
  assert.equal(ready, false);
  mcp.verify([...nativePreset, native], statuses, "session", false);
  assert.deepEqual(await waiting, {});
  assert.deepEqual(mcp.identities.get(native), { server: "installed", name: "echo.v1" });
  for (const tool_name of ["Read", "Write", "Bash"]) assert.deepEqual(await mcp.beforeTool({ ...input, tool_name }, "call", { signal }), {});
  for (const fields of [{ session_id: "other" }, { agent_id: "child" }, { tool_name: "mcp__installed__undeclared" }]) {
    assert.equal((await mcp.beforeTool({ ...input, ...fields }, "call", { signal })).hookSpecificOutput.permissionDecision, "deny");
  }
  assert.equal(mcp.servers.installed.alwaysLoad, true);
  assert.deepEqual(workspace.options.tools, { type: "preset", preset: "claude_code" });
  mcp.close();
  assert.equal((await mcp.beforeTool(input, "call", { signal })).hookSpecificOutput.permissionDecision, "deny");
});

test("workspace inventory tolerates native tools and servers, but not unavailable or ambiguous declared ones", t => {
  const mcp = new MCPProfile([stdio], []);
  mcp.verify([...nativePreset, native, "mcp__ambient__echo"], [...statuses, { name: "ambient", status: "connected", tools: [{ name: "echo" }] }], "session", false);
  mcp.close();
  for (const [tools, servers] of [
    [[...nativePreset, native], [{ ...statuses[0], tools: [{ name: "echo.v1" }, { name: "echo_v1" }] }]],
    [nativePreset, []],
  ]) {
    const declared = new MCPProfile([stdio], []);
    assert.throws(() => declared.verify(tools, servers, "session", false));
    declared.close();
  }
  const exact = new MCPProfile([stdio], []);
  assert.throws(() => exact.verify([...nativePreset, native], statuses, "session"));
  exact.close();
  // An optional server that did not connect contributes no tools; a required one fails.
  const optional = new MCPProfile([stdio], []);
  optional.verify(nativePreset, [{ ...statuses[0], status: "failed" }], "session", false);
  assert.equal(optional.identities.size, 0);
  optional.close();
  const required = new MCPProfile([{ ...stdio, required: true }], []);
  assert.throws(() => required.verify(nativePreset, [{ ...statuses[0], status: "pending" }], "session", false), /required/);
  required.close();
});

test("workspace bearer references reach native HTTP without leaking values into the request", t => {
  const reference = "OAC_RUNTIME_MCP_BEARER_ABCDEFGHIJKLMNOPQRSTUVWXYZ";
  const http = { server_label: "remote", server_url: "https://example.invalid/mcp", allowed_tools: null, bearer_token_env_var: reference };
  const { dirs, config } = fixture(t, [http]);
  process.env[reference] = "selected-user-token";
  process.env.OAC_RUNTIME_MCP_BEARER_UNSELECTED = "other-token";
  const mcp = new MCPProfile([http], []);
  const workspace = new WorkspaceProfile(dirs.work, config, mcp);
  assert.equal(workspace.options.env[reference], "selected-user-token");
  assert.equal(workspace.options.env.OAC_RUNTIME_MCP_BEARER_UNSELECTED, "other-token");
  assert.deepEqual(mcp.servers.remote.headers, { Authorization: `Bearer \${${reference}}` });
  assert.equal(JSON.stringify(mcp.servers).includes("selected-user-token"), false);
  delete process.env[reference];
  assert.throws(() => new WorkspaceProfile(dirs.work, config, mcp), /invalid_request/);
});

test("MCP identity validation preserves host functions and ordinary child environment", async t => {
  const { dirs, config } = fixture(t);
  const functions = ["mcp__functions__lookup"];
  const mcp = new MCPProfile([stdio], functions);
  const workspace = new WorkspaceProfile(dirs.work, { ...config, tool_env: { USER_VALUE: "initialized" } }, mcp);
  mcp.verify([...nativePreset, native, ...functions], [...statuses, { name: "functions", status: "connected" }], "session", false);
  const signal = new AbortController().signal;
  const input = { hook_event_name: "PreToolUse", session_id: "session", tool_use_id: "call", tool_name: "Bash", tool_input: { command: "printf ok" } };
  assert.deepEqual(await mcp.beforeTool(input, "call", { signal }), {});
  assert.equal(workspace.options.env.USER_VALUE, "initialized");
  assert.deepEqual(await mcp.beforeTool({ ...input, tool_name: functions[0] }, "call", { signal }), {});
  mcp.close();
});

test("public workspace HTTP preserves required startup and null versus empty allowlists", t => {
  const http = {server_label:"remote",server_url:"https://example.invalid/mcp",allowed_tools:[],required:true};
  const {dirs,config,request}=fixture(t,[http]);
  assert.deepEqual(parseRequest(JSON.stringify(request)),request);
  for(const allowed_tools of [null,[],["prove"]]) {
    const declaration={...http,allowed_tools};
    assert.deepEqual(parseEnvironmentMCP([declaration]),[declaration]);
    const mcp=new MCPProfile([declaration],[]);
    const workspace=new WorkspaceProfile(dirs.work,{...config,mcp:[declaration]},mcp);
    assert.throws(()=>mcp.verifyRequired([{name:"remote",status:"pending"}]),/required/);
    const statuses=[{name:"remote",status:"connected",tools:[{name:"prove"},{name:"other"}]}];
    mcp.verifyRequired(statuses);
    const selected=allowed_tools===null?["prove","other"]:allowed_tools;
    mcp.verify([...nativePreset,...selected.map(name=>"mcp__remote__"+name)],statuses,"session",false);
    mcp.close();
  }
});

test("declared stdio MCP runs the caller's command natively", () => {
  const declared = { server_label: "local", command: "run", args: ["-c", "a\nb"], allowed_tools: null, required: true, declared: true };
  assert.deepEqual(parseEnvironmentMCP([declared]), [declared]);
  for (const value of [{ ...declared, allowed_tools: ["x"] }, { ...declared, declared: false }, { ...declared, command: " " }, { ...declared, env: {} }]) {
    assert.throws(() => parseEnvironmentMCP([value]), /invalid_request/);
  }
  assert.deepEqual(new MCPProfile([declared], []).servers.local, { type: "stdio", command: "run", args: ["-c", "a\nb"], env: {}, alwaysLoad: true });
});
