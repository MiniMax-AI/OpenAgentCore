import assert from "node:assert/strict";
import test from "node:test";
import { parseRequest } from "../dist/request.js";

const request = native_model_options => JSON.stringify({ type: "executor_prepare", preparation_deadline:Date.now()+60000, model: "fixture", system_prompt: "", cwd: process.cwd(), native_model_options });
test("private bridge accepts compiled native options and rejects malformed structure", () => {
  const native = { effort: "high", thinking: { type: "enabled", budgetTokens: 1024, display: "omitted" } };
  assert.deepEqual(parseRequest(request(native)).native_model_options, native);
  for (const config of [null, [], {env:{ANTHROPIC_API_KEY:"private-sentinel"}}, {thinking:{type:"adaptive",api_key:"private-sentinel"}}, {effort:{}}, {maxThinkingTokens:1024}]) {
    assert.throws(() => parseRequest(request(config)), {message:"invalid_request"});
  }
});

test("private bridge rejects the public configuration field", () => {
  assert.throws(() => parseRequest(JSON.stringify({type:"executor_prepare",preparation_deadline:Date.now()+60000, model:"fixture",system_prompt:"",cwd:process.cwd(),harness_config:{}})), {message:"invalid_request"});
});

test("Executor preparation requires an absolute integer deadline and other requests cannot carry one", () => {
 const base={type:"executor_prepare",model:"fixture",system_prompt:"",cwd:process.cwd()};
 for(const value of [undefined,null,0,-1,1.5,"1",Number.MAX_SAFE_INTEGER+1])assert.throws(()=>parseRequest(JSON.stringify({...base,preparation_deadline:value})),/invalid_request/);
 assert.equal(parseRequest(JSON.stringify({...base,preparation_deadline:1})).preparation_deadline,1);
 assert.throws(()=>parseRequest(JSON.stringify({...base,type:"start",input:[{content:[{type:"input_text",text:"x"}]}],preparation_deadline:1})),/invalid_request/);
});
