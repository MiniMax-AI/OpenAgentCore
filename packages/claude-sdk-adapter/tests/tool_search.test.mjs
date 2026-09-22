import assert from "node:assert/strict";
import test from "node:test";
import { parseStart } from "../dist/request.js";
import { toolSearchEnvironment } from "../dist/tool_search.js";

const request = () => ({type:"start", input:[{content:[{type:"input_text",text:"lookup"}]}], model:"model", system_prompt:"", cwd:process.cwd(),
  tool_search:true, functions:[{name:"lookup",description:"Lookup",parameters:{type:"object"},defer_loading:true}]});

test("discovery preserves intent and rejects unqualified native combinations", () => {
  assert.deepEqual(parseStart(JSON.stringify(request())), request());
  for (const patch of [{tool_search:false}, {tool_search:null}, {output_format:{type:"json_schema",schema:{type:"object"}}},
    {mcp_http_servers:[]}, {subagents:{max_concurrent:2}}, {functions:[{...request().functions[0],defer_loading:false}]}]) {
    assert.throws(() => parseStart(JSON.stringify({...request(),...patch})), /invalid_request/);
  }
});

test("native search uses explicit mode without overriding conflicting operator settings", () => {
  const env = {ANTHROPIC_BASE_URL:"https://example.test/anthropic", RETAIN:"yes"};
  assert.deepEqual(toolSearchEnvironment(env,"model"), {...env, ENABLE_TOOL_SEARCH:"true"});
  assert.equal(env.ENABLE_TOOL_SEARCH,undefined);
  for (const patch of [{ENABLE_TOOL_SEARCH:"false"},{ENABLE_TOOL_SEARCH:"force"},{ENABLE_TOOL_SEARCH:"auto"},
    {CLAUDE_CODE_USE_BEDROCK:"1"},{CLAUDE_CODE_USE_VERTEX:"1"},{CLAUDE_CODE_USE_FOUNDRY:"1"},{CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS:"true"}]) {
    assert.throws(() => toolSearchEnvironment({...env,...patch},"model"), /unqualified/);
  }
  assert.throws(() => toolSearchEnvironment(env,"claude-3-5-haiku-latest"), /unqualified/);
});
