import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parseRequest } from "../dist/request.js";
import { assertFrame } from "./frames.mjs";

// Exercise the packaged entrypoint and real child ownership with a controlled SDK.
// Native dependency enforcement and model effects need separate native acceptance.
const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
const mode = process.argv[1];
const sdk = 'export function startup(args){return globalThis.startupFixture(args);} export function query(){throw new Error("Direct query used for preparation");} export async function getSessionInfo(){process.send({kind:"history"});return process.argv[1] === "missing-history" ? undefined : {sessionId:"native"};}';
registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return {url:"data:text/javascript,"+encodeURIComponent(sdk),shortCircuit:true};
  return next(specifier,context);
}});
globalThis.startupFixture = async ({options, initializeTimeoutMs}) => {
  assert.equal(initializeTimeoutMs,15000);
  assert.equal(options.model,"fixed-model");
  assert.equal(options.env.UNSELECTED_CANARY,"must-not-inherit");
  assert.equal(options.env.HOME,process.env.HOME);
  assert.deepEqual(options.tools,{type:"preset",preset:"claude_code"});
  assert.equal(options.sandbox,undefined);
  assert.equal(options.hooks,undefined);
  const child = options.spawnClaudeCodeProcess({command:process.execPath,env:options.env,signal:options.abortController.signal,
    args:["-e","process.stdin.resume();process.stdin.on('end',()=>process.exit(0));"]});
  process.send({kind:"spawn",pid:child.pid});
  const closed = new Promise(resolve=>child.once("close",()=>{process.send({kind:"native_close"});resolve();}));
  const close = () => child.stdin.end();
  await new Promise(resolve=>setTimeout(resolve,mode === "slow-startup" ? 200 : 30));
  if(mode === "startup-error") {close();await closed;throw new Error("Private native diagnostics");}
  let consumed=false;
  return {
    close(){if(!consumed)close();},
    query(prompt){
      assert.equal(consumed,false);consumed=true;
      if(mode === "native-exit")setTimeout(()=>child.kill("SIGTERM"),100);
      return {
        close,
        async initializationResult(){return {hooks_applied:true};},
        async *[Symbol.asyncIterator](){
          const command={type:"assistant",session_id:"native",parent_tool_use_id:null,
            message:{content:[{type:"tool_use",id:"command",name:"Bash",input:{command:"printf 'prepared'"}}]}};
          const iterator=prompt[Symbol.asyncIterator]();
          const first=await Promise.race([iterator.next(),closed.then(()=>({done:true}))]);
          if(first.done)return;
          process.send({kind:"input",text:first.value.message.content[0].text});
          yield {type:"system",subtype:"init",session_id:mode === "resume-mismatch"?"other":"native",mcp_servers:[],
            tools:["Bash","Read","Edit"]};
          const ids=[first.value.uuid];
          if(mode === "commands") {
            yield command;
            yield {type:"user",session_id:"native",parent_tool_use_id:null,
              message:{content:[{type:"tool_result",tool_use_id:"command",content:"prepared",is_error:false}]}};
          }
          if(mode === "steer") {
            const second=await Promise.race([iterator.next(),closed.then(()=>({done:true}))]);
            if(second.done)return;
            ids.push(second.value.uuid);
          }
          await new Promise(resolve=>setTimeout(resolve,80));
          if(options.abortController.signal.aborted)return;
          yield {type:"result",uuid:"result",session_id:"native",user_message_uuids:ids,subtype:"success",is_error:false,
            result:"answer",usage:{input_tokens:1,output_tokens:1},modelUsage:{}};
        }
      };
    }
  };
};
await import(${JSON.stringify(new URL("../dist/main.js", import.meta.url).href)});
process.disconnect();
`;

function placement() {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "oac-prepare-")));
  const dirs = Object.fromEntries(["workspace", "home", "state", "scratch", "deps"].map(name => {
    const path = join(root, name); mkdirSync(path); return [name, path];
  }));
  return { root, request: { type: "executor_prepare", model: "fixed-model", system_prompt: "", cwd: dirs.workspace,
    workspace: { home: dirs.home, state: dirs.state, scratch: dirs.scratch,   env_names: [] } } };
}

test("prepare validates a workspace-only immutable configuration", () => {
  const { root, request } = placement();
  try {
    assert.deepEqual(parseRequest(JSON.stringify(request)), request);
    assert.deepEqual(parseRequest(JSON.stringify({ ...request, functions: [] })), { ...request, functions: [] });
    for (const fields of [{ input: [{ content: [{ type: "input_text", text: "input" }] }] },
      { mcp_http_servers: [] }, { env: {} }, { resume: "" }, { type: "start" }]) {
      assert.throws(() => parseRequest(JSON.stringify({ ...request, ...fields })), /invalid_request/);
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});

async function launch(t, mode) {
  const { root, request } = placement();
  const env = { ...process.env, HOME: request.workspace.home, CLAUDE_CONFIG_DIR: request.workspace.state, UNSELECTED_CANARY: "must-not-inherit" };
  delete env.CLAUDE_CODE_PROJECT_DIR_NAME;
  const child = spawn(process.execPath, ["--input-type=module", "-e", fixture, mode], { env, stdio: ["pipe", "pipe", "pipe", "ipc"] });
  const events = [], observations = [];
  let stderr = "", buffer = "", notify = () => {};
  child.stdout.setEncoding("utf8"); child.stderr.setEncoding("utf8");
  child.stdout.on("data", value => {
    buffer += value;
    while (buffer.includes("\n")) {
      const end = buffer.indexOf("\n"); const event = JSON.parse(buffer.slice(0, end)); assertFrame(event); events.push(event); buffer = buffer.slice(end + 1);
    }
    notify();
  });
  child.stderr.on("data", value => { stderr += value; });
  child.on("message", value => { observations.push(value); notify(); });
  const closed = new Promise(resolve => child.once("close", (code, signal) => resolve({ code, signal })));
  t.after(async () => { child.kill("SIGKILL"); await closed; rmSync(root, { recursive: true, force: true }); });
  const wait = async predicate => {
    if (predicate()) return;
    let timer;
    try {
      await new Promise((resolve, reject) => {
        notify = () => { if (predicate()) resolve(); };
        timer = setTimeout(() => reject(new Error("Bridge observation timeout: " + stderr)), 5000);
      });
    } finally { clearTimeout(timer); notify = () => {}; }
  };
  const send = value => child.stdin.write(JSON.stringify(value) + "\n");
  const turn = text => send({ type: "turn_start", turn_id: "run", input: [{ content: [{ type: "input_text", text }] }] });
  const ready = () => wait(() => events.some(event => event.type === "executor_ready"));
  const finish = async code => {
    await wait(() => events.some(event => event.type === "turn_settled"));
    child.stdin.end();
    assert.deepEqual(await closed, { code: 0, signal: null }, stderr);
    const terminal = events.filter(event => event.type === "error" || event.type === "result");
    assert.equal(terminal.length, 1);
    if (code) assert.equal(terminal[0].code, code);
    else assert.equal(terminal[0].type, "result");
    assert.equal(observations.filter(value => value.kind === "native_close").length, observations.filter(value => value.kind === "spawn").length);
  };
  if (["missing-history", "resume", "resume-mismatch"].includes(mode)) request.resume = "native";
  return { child, request, events, observations, closed, send, wait, turn, ready, finish };
}

for (const mode of ["resume", "steer", "commands", "resume-mismatch"]) {
  test(`prepared entrypoint lifecycle: ${mode}`, { timeout: 10000 }, async t => {
    const { request, events, observations, send, wait, turn, ready, finish } = await launch(t, mode);
    send(request);
    await ready();
    await new Promise(resolve => setTimeout(resolve, 30));
    assert.equal(observations.filter(value => value.kind === "spawn").length, 1);
    assert.equal(observations.some(value => value.kind === "input"), false);
    turn("first");
    if (mode === "resume-mismatch") {
      await finish("execution_failed");
      assert.equal(events.some(event => event.type === "input_ready"), false);
      return;
    }
    await wait(() => events.some(event => event.type === "input_ready"));
    if (mode === "steer") send({ type: "steer", turn_id: "run", input_id: "extra", input: [{ content: [{ type: "input_text", text: "second" }] }] });
    await finish();
    assert.deepEqual(observations.filter(value => value.kind === "input"), [{ kind: "input", text: "first" }]);
    if (mode === "steer") assert.ok(events.some(event => event.type === "input_applied" && event.input_id === "extra"));
    if (mode === "resume") assert.equal(observations[0].kind, "history");
    if (mode === "commands") assert.deepEqual(events.filter(event => event.type === "command_observation").map(event =>
      [event.session_id, event.id, event.stage, event.observation.output]),
      [["native", "command", "before", undefined], ["native", "command", "after", "prepared"]]);
  });
}

test("an owner signal or a lost native process ends an idle Executor", { timeout: 10000 }, async t => {
  for (const mode of ["owner-cancel", "native-exit"]) {
    const { child, request, events, closed, send, ready } = await launch(t, mode);
    send(request);
    await ready();
    if (mode === "owner-cancel") child.kill("SIGTERM");
    assert.deepEqual(await closed, { code: 0, signal: null });
    assert.equal(events.some(event => event.type === "error"), mode === "native-exit");
  }
});

for (const mode of ["missing-history", "startup-error", "cancel-startup"]) {
  test(`preparation fails before readiness: ${mode}`, { timeout: 10000 }, async t => {
    const { child, request, events, observations, closed, send, wait } = await launch(t, mode === "cancel-startup" ? "slow-startup" : mode);
    send(request);
    if (mode === "cancel-startup") { await wait(() => observations.some(value => value.kind === "spawn")); child.kill("SIGTERM"); }
    assert.deepEqual(await closed, { code: 0, signal: null });
    assert.equal(events.some(event => event.type === "executor_ready"), false);
    assert.deepEqual(events.map(event => event.code), mode === "missing-history" ? ["history_unavailable"] : mode === "startup-error" ? ["execution_failed"] : []);
    assert.equal(observations.some(value => value.kind === "input"), false);
    if (mode === "missing-history") assert.equal(observations.some(value => value.kind === "spawn"), false);
  });
}
