import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { ExecutorTurns } from "../dist/executor_protocol.js";
import { Inputs } from "../dist/inputs.js";

test("a Turn applies only the settings that changed", async () => {
  const calls = [];
  const stream = { setModel: async model => calls.push(["model", model]), setPermissionMode: async mode => calls.push(["mode", mode]),
    applyFlagSettings: async settings => calls.push(["flags", settings]) };
  const turns = new ExecutorTurns(async () => {}, new AbortController());
  turns.configure(stream, input => { calls.push(["input"]); return new Inputs(input); }, async () => {}, { model: "a", effort: "high", review: "" });
  const input = [{ content: [{ type: "input_text", text: "hi" }] }];
  const run = async (id, settings) => { await turns.start({ type: "turn_start", turn_id: id, input, settings }); await turns.settled(true, true, ""); };
  await run("one", { model: "a", effort: "high", review: "" });
  assert.deepEqual(calls, [["input"]]);
  calls.length = 0;
  await run("two", { model: "b", effort: null, review: "auto_review" });
  assert.deepEqual(calls, [["input"], ["model", "b"], ["flags", { effortLevel: null }], ["mode", "auto"]]);
  calls.length = 0;
  await run("three", { model: "b", effort: "max", review: "" });
  assert.deepEqual(calls, [["input"], ["flags", { effortLevel: "max" }], ["mode", "default"]]);
});

const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { mkdtempSync, mkdirSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { sdkSource, startupFrom, runTurn } from ${JSON.stringify(new URL("./executor_run.mjs", import.meta.url).href)};
registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return {url:"data:text/javascript,"+encodeURIComponent(sdkSource),shortCircuit:true};
  return next(specifier,context);
}});
const root = realpathSync(mkdtempSync(join(tmpdir(), "oac-review-")));
const dirs = Object.fromEntries(["workspace", "home", "state", "scratch"].map(name => { const path = join(root, name); mkdirSync(path); return [name, path]; }));
process.env.HOME = dirs.home; process.env.CLAUDE_CONFIG_DIR = dirs.state; process.env.ANTHROPIC_API_KEY = "fixture";
const request = { type: "executor_prepare", model: "fixture", system_prompt: "", cwd: dirs.workspace,
  workspace: { home: dirs.home, state: dirs.state, scratch: dirs.scratch, env_names: ["ANTHROPIC_API_KEY"] } };
const review = process.argv[1];
let canUseTool, answer;
globalThis.startupFixture = startupFrom(({prompt, options}) => {
  canUseTool = options.canUseTool;
  return { async *[Symbol.asyncIterator]() {
    const first = (await prompt[Symbol.asyncIterator]().next()).value;
    yield { type: "system", subtype: "init", session_id: "native", mcp_servers: [], tools: ["Bash", "Read", "Edit"], skills: [] };
    yield { type: "result", uuid: "result", session_id: "native", user_message_uuids: [first.uuid], subtype: "success", is_error: false, result: "ok", usage: {input_tokens:1,output_tokens:1}, modelUsage:{} };
  } };
});
try {
  await runTurn(request, new AbortController(), [{ content: [{ type: "input_text", text: "hi" }] }], async frame => {
    if (frame.type === "usage") answer = await canUseTool("Bash", { command: "ls" }, { signal: new AbortController().signal });
  }, review);
  assert.equal(answer.behavior, review ? "deny" : "allow");
} finally { rmSync(root, { recursive: true, force: true }); }
`;

for (const review of ["", "auto_review"]) {
  test(`the permission callback under review ${review || "none"}`, { timeout: 15000 }, () => {
    const child = spawnSync(process.execPath, ["--input-type=module", "-e", fixture, review], { encoding: "utf8", timeout: 10000 });
    assert.equal(child.status, 0, child.stderr || child.error?.message);
  });
}
