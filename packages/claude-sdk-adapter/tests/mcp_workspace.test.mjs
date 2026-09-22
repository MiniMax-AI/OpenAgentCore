import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MCPProfile } from "../dist/mcp.js";
import { parseEnvironmentMCP } from "../dist/mcp_environment.js";
import { immediateInput, parseStart } from "../dist/request.js";
import { WorkspaceProfile } from "../dist/workspace.js";

const stdio = { server_label: "installed", command: "/usr/bin/python3", allowed_tools: null,
  args: ["-I", "-S", "/usr/local/bin/agents-api-runtime-initialize", "stdio", "plugins/installed", "installed"] };
const native = "mcp__installed__echo_v1";
const statuses = [{ name: "installed", status: "connected", tools: [{ name: "echo.v1" }] }];
const baseline = ["Bash", "Read", "Edit"];

function fixture(t, declarations = [stdio]) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "parsar-mcp-workspace-")));
  const dirs = Object.fromEntries(["work", "home", "state", "scratch", "secrets", "deps"].map(name => {
    const path = join(root, name); mkdirSync(path); return [name, path];
  }));
  const config = { home: dirs.home, state: dirs.state, scratch: dirs.scratch, protected_dirs: [dirs.secrets],
    dependency_path: dirs.deps, env_names: ["ANTHROPIC_AUTH_TOKEN"], network_access: "enabled", mcp: declarations };
  const previous = process.env;
  process.env = { HOME: dirs.home, CLAUDE_CONFIG_DIR: dirs.state, ANTHROPIC_AUTH_TOKEN: "model-secret" };
  t.after(() => { process.env = previous; rmSync(root, { recursive: true, force: true }); });
  const request = { type: "start", input: [{ content: [{ type: "input_text", text: "fixture" }] }], model: "fixture", system_prompt: "", cwd: dirs.work, workspace: config };
  return { dirs, config, request };
}

test("installed MCP private projection cannot launch arbitrary unsandboxed commands", t => {
  const { request } = fixture(t);
  assert.deepEqual(parseStart(JSON.stringify(request)), request);
  assert.throws(() => parseStart(JSON.stringify({ ...request, observe_messages: true,
    output_format: { type: "json_schema", schema: { type: "object" } } })), /invalid_request/);
  assert.equal(immediateInput(request), undefined);
  assert.deepEqual(parseEnvironmentMCP([stdio]), [stdio]);
  for (const value of [[stdio, stdio], [{ ...stdio, command: "/bin/sh" }], [{ ...stdio, env: { TOKEN: "secret" } }],
    [{ ...stdio, args: ["-c", "untrusted"] }], [{ ...stdio, allowed_tools: ["*"] }],
    [{ ...stdio, server_url: "https://example.invalid" }], [{ ...stdio, args: [...stdio.args.slice(0, 4), "../escape", "installed"] }]]) {
    assert.throws(() => parseEnvironmentMCP(value), /invalid_request/);
  }
  assert.throws(() => parseStart(JSON.stringify({ ...request, workspace: { ...request.workspace, network_access: "disabled" } })), /invalid_request/);
});

test("combined inventory admits exact MCP identities without granting local file authority", async t => {
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
  assert.equal((await workspace.canUseTool("Read", { file_path: join(dirs.secrets, "model.key") }, { signal })).behavior, "deny");
  assert.equal((await workspace.canUseTool("Bash", { command: "pwd", dangerouslyDisableSandbox: true }, { signal })).behavior, "deny");
  assert.equal((await workspace.beforeTool({ ...input, tool_name: "Read", tool_input: { file_path: "ok.txt" } }, "call", { signal })).hookSpecificOutput.updatedInput.file_path, join(dirs.work, "ok.txt"));
  for (const fields of [{ session_id: "other" }, { agent_id: "child" }, { tool_name: "mcp__ambient__echo" }]) {
    assert.equal((await workspace.beforeTool({ ...input, ...fields }, "call", { signal })).hookSpecificOutput.permissionDecision, "deny");
  }
  assert.deepEqual(workspace.options.allowedTools, ["mcp__installed__*"]);
  assert.deepEqual(workspace.options.tools, baseline);
  assert.ok(workspace.options.sandbox.filesystem.denyWrite.includes("/environment/initialization/capabilities"));
  assert.equal(workspace.options.sandbox.allowUnsandboxedCommands, false);
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

test("workspace bearer references reach native HTTP and stay denied to Bash", t => {
  const reference = "PARSAR_MCP_BEARER_ABCDEFGHIJKLMNOPQRSTUVWXYZ";
  const http = { server_label: "remote", server_url: "https://example.invalid/mcp", allowed_tools: null, bearer_token_env_var: reference };
  const { dirs, config } = fixture(t, [http]);
  process.env[reference] = "selected-user-token";
  process.env.PARSAR_MCP_BEARER_UNSELECTED = "other-token";
  const mcp = new MCPProfile([http], []);
  const workspace = new WorkspaceProfile(dirs.work, config, [], mcp);
  assert.equal(workspace.options.env[reference], "selected-user-token");
  assert.equal(workspace.options.env.PARSAR_MCP_BEARER_UNSELECTED, undefined);
  assert.deepEqual(mcp.servers.remote.headers, { Authorization: `Bearer \${${reference}}` });
  assert.ok(workspace.options.sandbox.credentials.envVars.some(entry => entry.name === reference && entry.mode === "deny"));
  assert.equal(JSON.stringify(mcp.servers).includes("selected-user-token"), false);
  delete process.env[reference];
  assert.throws(() => new WorkspaceProfile(dirs.work, config, [], mcp), /invalid_request/);
});

test("MCP identity validation preserves host functions and Bash environment wrapping", async t => {
  const { dirs, config } = fixture(t);
  const functions = ["mcp__functions__lookup"];
  const mcp = new MCPProfile([stdio], functions);
  const workspace = new WorkspaceProfile(dirs.work, { ...config, tool_environment: true }, functions, mcp);
  workspace.verify([...baseline, native, ...functions], [...statuses, { name: "functions", status: "connected" }], "session");
  const signal = new AbortController().signal;
  const input = { hook_event_name: "PreToolUse", session_id: "session", tool_use_id: "call", tool_name: "Bash", tool_input: { command: "printf ok" } };
  assert.match((await workspace.beforeTool(input, "call", { signal })).hookSpecificOutput.updatedInput.command, /^\. \/environment\/initialization\/tool-env.sh/);
  assert.equal((await workspace.canUseTool(functions[0], {}, { signal })).behavior, "allow");
  mcp.close();
});
