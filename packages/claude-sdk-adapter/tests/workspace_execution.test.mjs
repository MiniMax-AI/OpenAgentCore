import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

const fixture = `
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { sdkSource, startupFrom, runTurn } from ${JSON.stringify(new URL("./executor_run.mjs", import.meta.url).href)};
registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return {url:"data:text/javascript,"+encodeURIComponent(sdkSource),shortCircuit:true};
  return next(specifier,context);
}});
const root = realpathSync(mkdtempSync(join(tmpdir(), "oac-workspace-execute-")));
const dirs = Object.fromEntries(["workspace", "home", "state", "scratch", "deps", "capability"].map(name => {
  const path = join(root, name); mkdirSync(path); return [name, path];
}));
const mode = process.argv[1];
mkdirSync(join(dirs.capability, "pkg", "skill"), { recursive: true });
writeFileSync(join(dirs.capability, "pkg", "skill", "SKILL.md"), "---\\nname: fixture\\ndescription: Fixture.\\n---\\nDo it.\\n");
const workspace = { home: dirs.home, state: dirs.state, scratch: dirs.scratch,
    env_names: ["ANTHROPIC_API_KEY"], ...(mode.startsWith("skills") ? { capability_root: dirs.capability,
      skills: [{ metadata: { type: "inline", name: "fixture", description: "Fixture." }, relative_root: "pkg/skill", package_root: "pkg" }] } : {}) };
const request = { type: "executor_prepare", model: "fixture", system_prompt: mode === "instructions" ? "Be brief." : "", cwd: dirs.workspace,
  workspace, ...(mode.startsWith("resume") ? { resume: "native" } : {}),
  ...(mode.startsWith("functions") ? { functions: [{ name: "lookup", description: "Lookup", parameters: { type: "object" }, defer_loading: false }] } : {}) };
process.env.HOME = dirs.home;
process.env.CLAUDE_CONFIG_DIR = dirs.state;
process.env.ANTHROPIC_API_KEY = "fixture-secret";
process.env.UNSELECTED_CANARY = "must-not-inherit";
delete process.env.CLAUDE_CODE_PROJECT_DIR_NAME;
const events = [], calls = [];
const abort = new AbortController();
globalThis.historyFixture = async (id, options) => {
  calls.push("history");
  assert.equal(id, "native");
  assert.deepEqual(options, { dir: dirs.workspace });
  assert.equal(process.env.HOME, dirs.home);
  assert.equal(process.env.CLAUDE_CONFIG_DIR, dirs.state);
  return mode === "resume-missing" ? undefined : { sessionId: "native" };
};
globalThis.startupFixture = startupFrom(({prompt, options}) => {
  calls.push("query");
  assert.equal(options.env.UNSELECTED_CANARY, "must-not-inherit");
  assert.equal(options.env.ANTHROPIC_API_KEY, "fixture-secret");
  assert.equal(options.env.HOME, dirs.home);
  assert.deepEqual(options.tools, {type:"preset",preset:"claude_code"});
  assert.equal(options.settingSources, undefined);
  // A project's settings cannot bring background work back.
  assert.deepEqual(options.settings, {env:{CLAUDE_CODE_DISABLE_BACKGROUND_TASKS:"1",DISABLE_AUTOUPDATER:"1"}});
  assert.deepEqual(options.systemPrompt, {type:"preset",preset:"claude_code",...(mode === "instructions" ? {append:"Be brief."} : {})});
  assert.equal(options.resume, request.resume);
  return { async *[Symbol.asyncIterator]() {
    const first = (await prompt[Symbol.asyncIterator]().next()).value;
    yield { type: "system", subtype: "init", session_id: "native", mcp_servers: request.functions ? [{ name: "functions", status: "connected" }] : [],
      tools: ["Bash", "Read", "Edit", ...(mode === "functions" ? ["mcp__functions__lookup"] : []), ...(mode.startsWith("skills") ? ["Skill"] : [])],
      // A project's settings can disable a user Skill while the Skill tool stays.
      skills: mode === "skills" ? ["fixture"] : [] };
    if (mode.startsWith("commands")) {
      const call = id => ({type:"assistant",session_id:"native",parent_tool_use_id:null,
        message:{content:[{type:"tool_use",name:"Bash",id,input:{command:"printf 'observed'"}}]}});
      yield call("observed");
      yield {type:"user",session_id:"native",parent_tool_use_id:null,
        message:{content:[{type:"tool_result",tool_use_id:"observed",content:"observed",is_error:false}]},
        tool_use_result:{stdout:"observed",stderr:"",interrupted:false}};
      if (mode !== "commands-success") yield call("unfinished");
      if (mode === "commands-cancel") {await globalThis.cancelTurn();return;}
    }
    yield { type: "result", uuid: "result", session_id: "native", user_message_uuids: [first.uuid],
      subtype: "success", is_error: false, result: "fixture", usage: {input_tokens:1,output_tokens:1}, modelUsage:{} };
  } };
}, { args: ["-e", "const assert=require('node:assert/strict');assert.equal(process.env.UNSELECTED_CANARY,'must-not-inherit');assert.equal(process.env.ANTHROPIC_API_KEY,'fixture-secret');process.stdin.resume();process.stdin.on('end',()=>process.exit(0));"] });
const input = [{ content: [{ type: "input_text", text: "fixture" }] }];
try {
  if (mode === "resume-mismatch") {
    process.env.CLAUDE_CONFIG_DIR = dirs.home;
    await assert.rejects(runTurn(request, abort, input), /invalid_request/);
    assert.deepEqual(calls, []);
  } else {
    events.push(...(await runTurn(request, abort, input)).frames);
    if (mode === "resume-missing") {
      assert.deepEqual(calls, ["history"]);
      assert.deepEqual(events, [{type:"error",code:"history_unavailable"}]);
    } else {
      assert.deepEqual(calls, mode === "resume-existing" ? ["history", "query"] : ["query"]);
      if (mode.startsWith("commands")) {
        const observations=events.filter(e=>e.type==="command_observation");
        assert.deepEqual(observations.slice(0,2).map(e=>[e.id,e.stage,e.observation.output]),
          [["observed","before",undefined],["observed","after","observed"]]);
        assert.equal(events.some(e=>e.type==="command_output"),false);
        if(mode === "commands-success") {
          assert.equal(observations.length,2);assert.equal(events.at(-1).type,"result");
        } else {
          assert.equal(observations.length,4);
          assert.deepEqual(observations.at(-1).observation,{kind:"command",status:"incomplete",command:"printf 'observed'"});
          assert.deepEqual(events.at(-1),{type:"error",code:mode === "commands-cancel"?"cancelled":"execution_failed"});
        }
      } else if (mode === "functions-missing" || mode === "skills-missing") assert.deepEqual(events.at(-1), {type:"error",code:"execution_failed"});
      else assert.equal(events.at(-1).type, "result");
    }
  }
} finally { rmSync(root, {recursive:true,force:true}); }
`;

for (const mode of ["fresh", "instructions", "resume-existing", "resume-missing", "resume-mismatch", "commands-success", "commands-cancel", "commands-missing", "functions", "functions-missing", "skills", "skills-missing"]) {
  test(`workspace execution boundary: ${mode}`, { timeout: 15000 }, () => {
    const child = spawnSync(process.execPath, ["--input-type=module", "-e", fixture, mode], { encoding: "utf8", timeout: 10000 });
    assert.equal(child.status, 0, child.stderr || child.error?.message);
  });
}
