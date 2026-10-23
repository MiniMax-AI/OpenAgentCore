import assert from "node:assert/strict";
import test from "node:test";
import { parseRequest } from "../dist/request.js";

const request = () => ({type:"executor_prepare", model:"model", system_prompt:"", cwd:process.cwd(),
  tool_search:true, functions:[{name:"lookup",description:"Lookup",parameters:{type:"object"},defer_loading:true}]});

test("discovery preserves intent and rejects a mismatch with deferred functions", () => {
  assert.deepEqual(parseRequest(JSON.stringify(request())), request());
  for (const patch of [{tool_search:false}, {tool_search:null}, {functions:[{...request().functions[0],defer_loading:false}]}]) {
    assert.throws(() => parseRequest(JSON.stringify({...request(),...patch})), /invalid_request/);
  }
});

test("discovery combines with structured output and MCP", () => {
  const combined = {...request(), output_format:{type:"json_schema",schema:{type:"object"}},
    mcp_http_servers:[{server_label:"docs",server_url:"https://example.test/mcp",allowed_tools:null}]};
  assert.deepEqual(parseRequest(JSON.stringify(combined)), combined);
});
