import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

// Exercise execute() against a controlled SDK event interleaving. The native child
// is real, but this case is not live provider acceptance.
const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { sdkSource, startupFrom, runTurn } from ${JSON.stringify(new URL("./executor_run.mjs", import.meta.url).href)};
registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return {url:"data:text/javascript,"+encodeURIComponent(sdkSource),shortCircuit:true};
  return next(specifier,context);
}});
const abort = new AbortController();
globalThis.startupFixture = startupFrom(({prompt, options}) => {
  assert.equal(options.effort, "high");
  assert.deepEqual(options.thinking, {type:"enabled",budgetTokens:1024});
  assert.equal(options.model, "fixture");
  assert.equal(options.systemPrompt, "Translate.");
  assert.deepEqual(options.settings, {env:{CLAUDE_CODE_DISABLE_BACKGROUND_TASKS:"1",DISABLE_AUTOUPDATER:"1"}});
  return {
    async *[Symbol.asyncIterator](){
      const iterator = prompt[Symbol.asyncIterator]();
      const first = (await iterator.next()).value;
      yield {type:"system",subtype:"init",session_id:"native",tools:[],mcp_servers:[]};
      const second = (await iterator.next()).value;
      yield {type:"result",uuid:"failed",session_id:"native",user_message_uuids:[first.uuid,second.uuid],subtype:"error_during_execution",is_error:true,
        result:"later answer",usage:{input_tokens:2,output_tokens:1},modelUsage:{},total_cost_usd:0.01};
    },
  };
});
const {frames:events} = await runTurn({type:"executor_prepare",model:"fixture",system_prompt:"Translate.",cwd:process.cwd(),native_model_options:{effort:"high",thinking:{type:"enabled",budgetTokens:1024}}},
  abort,[{ content: [{ type: "input_text", text: "opening text" }] }],
  async (frame, turns) => { if (frame.type === "input_ready") await turns.submit({type:"steer",turn_id:"run",input_id:"extra",input:[{content:[{type:"input_text",text:"later text"}]}]}); });
assert.equal(events.filter(e=>e.type === "input_applied" && e.input_id === "extra").length,1);
assert.equal(events.at(-1).type,"error");
assert.equal(events.at(-1).code,"execution_failed");
assert.equal(events.filter(e=>e.type === "usage").length,1);
assert.equal(events.some(e=>e.type === "result"),false);
`;

test("a failed native result keeps usage and receipts without a result", { timeout: 15000 }, () => {
  const child = spawnSync(process.execPath, ["--input-type=module", "-e", fixture], { encoding: "utf8", timeout: 10000 });
  assert.equal(child.status, 0, child.stderr || child.error?.message);
});
