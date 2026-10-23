import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

// Actual execute()/child lifetime with a controlled SDK stream, not model evidence.
const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { sdkSource, startupFrom, runTurn } from ${JSON.stringify(new URL("./executor_run.mjs", import.meta.url).href)};
registerHooks({resolve(s,c,n){if(s==="@anthropic-ai/claude-agent-sdk")return {url:"data:text/javascript,"+encodeURIComponent(sdkSource),shortCircuit:true};return n(s,c);}});
const mode=process.argv[1], abort=new AbortController();
globalThis.startupFixture=startupFrom(({prompt})=>({async *[Symbol.asyncIterator](){
  const input=(await prompt[Symbol.asyncIterator]().next()).value;
  yield {type:"system",subtype:"init",session_id:"native",tools:[],mcp_servers:[]};
  yield {type:"assistant",session_id:"native",parent_tool_use_id:mode==="child"?"child":null,
   uuid:"assistant",user_message_uuid:mode==="foreign-input"?"foreign":input.uuid,error:"authentication_failed",message:{content:[]}};
  if(mode==="missing-result")throw new Error("unstructured 401 failure");
  if(mode==="recovered")yield {type:"assistant",session_id:"native",parent_tool_use_id:null,uuid:"recovery",user_message_uuid:input.uuid,message:{content:[]}};
  if(mode==="cancel")await globalThis.cancelTurn();
  yield {type:"result",uuid:"result",session_id:"native",user_message_uuid:input.uuid,
   subtype:mode==="recovered"?"success":mode==="budget"?"error_max_budget_usd":"error_during_execution",
   is_error:mode!=="recovered",result:"answer",usage:{input_tokens:2,output_tokens:1},modelUsage:{},total_cost_usd:0.01};
}}),{exitCode:mode==="cancel"?0:1});
const {frames:events}=await runTurn({type:"executor_prepare",model:"fixture",system_prompt:"",cwd:process.cwd()},abort,[{content:[{type:"input_text",text:"hello"}]}]);
const last=events.at(-1);
// A nonzero native exit is expected on a structured provider rejection; the bridge
// still retains its validated result and usage. Recovery + nonzero exit is generic.
assert.equal(last.type,"error");
assert.equal(last.code,mode==="cancel"?"cancelled":"execution_failed");
assert.equal(last.engine_error_code,mode==="classified"?"authentication_error":undefined);
if(mode==="classified"){assert.equal(last.session_id,"native");assert.equal(last.result_id,"result");}
assert.equal(events.filter(e=>e.type==="usage").length,mode==="missing-result"?0:1);
`;
for (const mode of ["classified","child","foreign-input","missing-result","recovered","budget","cancel"]) {
 test(`settled classified SDK failure: ${mode}`,{timeout:15000},()=>{
  const child=spawnSync(process.execPath,["--input-type=module","-e",fixture,mode],{encoding:"utf8",timeout:10000});
  assert.equal(child.status,0,child.stderr||child.error?.message);
 });
}
