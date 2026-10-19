import assert from "node:assert/strict";
import test from "node:test";
import { parseStart } from "../dist/request.js";
import { toolSearchEnvironment } from "../dist/tool_search.js";

const request = () => ({type:"start", input:[{content:[{type:"input_text",text:"lookup"}]}], model:"model", system_prompt:"", cwd:process.cwd(),
  tool_search:true, functions:[{name:"lookup",description:"Lookup",parameters:{type:"object"},defer_loading:true}]});

test("discovery preserves intent and rejects a mismatch with deferred functions", () => {
  assert.deepEqual(parseStart(JSON.stringify(request())), request());
  for (const patch of [{tool_search:false}, {tool_search:null}, {functions:[{...request().functions[0],defer_loading:false}]}]) {
    assert.throws(() => parseStart(JSON.stringify({...request(),...patch})), /invalid_request/);
  }
});

test("discovery combines with structured output and MCP", () => {
  const combined = {...request(), observe_messages:true, output_format:{type:"json_schema",schema:{type:"object"}},
    mcp_http_servers:[{server_label:"docs",server_url:"https://example.test/mcp",allowed_tools:null}]};
  assert.deepEqual(parseStart(JSON.stringify(combined)), combined);
});

test("native search sets explicit mode and leaves provider qualification to the native harness", () => {
  const env = {ANTHROPIC_BASE_URL:"https://example.test/anthropic", RETAIN:"yes", CLAUDE_CODE_USE_BEDROCK:"1"};
  assert.deepEqual(toolSearchEnvironment(env), {...env, ENABLE_TOOL_SEARCH:"true"});
  assert.equal(env.ENABLE_TOOL_SEARCH,undefined);
});
