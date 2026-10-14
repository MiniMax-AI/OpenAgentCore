import assert from "node:assert/strict";
import test from "node:test";
import { NativeFailure } from "../dist/native_failure.js";
import { Inputs } from "../dist/inputs.js";

const assistant = (error, extra = {}) => ({ type: "assistant", session_id: "native", parent_tool_use_id: null, error, ...extra });
const result = (extra = {}) => ({ type: "result", session_id: "native", subtype: "error_during_execution", is_error: true, ...extra });

test("pinned SDK finite native mapping", () => {
  for (const [native, code] of Object.entries({ authentication_failed:"authentication_error", oauth_org_not_allowed:"authentication_error",
    account_on_hold:"authentication_error", verification_required:"authentication_error", cloud_credential_error:"authentication_error",
    billing_error:"usage_limit_exceeded", rate_limit:"rate_limit_exceeded", overloaded:"server_overloaded", invalid_request:"invalid_request",
    model_not_found:"resource_not_found", server_error:"server_error", unknown:undefined, max_output_tokens:undefined,
    future:undefined, toString:undefined, __proto__:undefined })) {
    const f = new NativeFailure();
    assert.equal(f.observe(assistant(native), "native", ["input"]), undefined);
    assert.equal(f.observe(result(), "native", ["input"]), code);
  }
});

test("a server error is a connection failure only after retries without an HTTP response", () => {
  const retry = error_status => ({ type: "system", subtype: "api_retry", session_id: "native", error_status, error: "unknown" });
  const text = { type: "stream_event", session_id: "native", parent_tool_use_id: null, event: { type: "content_block_start" } };
  for (const [before, code] of [[[retry(null)], "connection_failed"], [[retry(503)], "server_error"],
    [[retry(null), text, assistant(undefined)], "server_error"], [[], "server_error"]]) {
    const f = new NativeFailure();
    for (const message of before) f.observe(message, "native", message.type === "assistant" ? ["input"] : []);
    f.observe(assistant("server_error"), "native", ["input"]);
    assert.equal(f.observe(result({ subtype: "success", api_error_status: null }), "native", ["input"]), code);
  }
});

test("classification requires same root, input and unsuccessful terminal result", () => {
  for (const transform of [m=>({...m,parent_tool_use_id:"child"}),m=>({...m,session_id:"other"}),m=>({...m,isReplay:true}),m=>({...m,isSynthetic:true})]) {
    const f = new NativeFailure(); f.observe(transform(assistant("rate_limit")),"native",["input"]);
    assert.equal(f.observe(result(),"native",["input"]),undefined);
  }
  for (const terminal of [result({subtype:"success",is_error:false}),result({subtype:"error_max_turns"}),result({subtype:"error_max_budget_usd"}),result({subtype:"error_max_structured_output_retries"})]) {
    const f = new NativeFailure(); f.observe(assistant("rate_limit"),"native",["input"]);
    assert.equal(f.observe(terminal,"native",["input"]),undefined);
    assert.equal(f.observe(result(),"native",["input"]),undefined);
  }
  const f = new NativeFailure();
  f.observe(assistant("rate_limit"),"native",["old"]);
  assert.equal(f.observe(result(),"native",["new"]),undefined);
  f.observe(assistant("rate_limit"),"native",["input"]);
  f.observe(assistant(undefined),"native",["input"]);
  assert.equal(f.observe(result(),"native",["input"]),undefined);
  f.observe({type:"system",subtype:"api_retry",session_id:"native",error:"rate_limit"},"native",["retry"]);
  assert.equal(f.observe(result(),"native",["retry"]),undefined);
});

test("only current admitted input identities reach classification", async () => {
  const inputs = new Inputs([{content:[{type:"input_text",text:"hello"}]}]);
  const native = (await inputs[Symbol.asyncIterator]().next()).value;
  inputs.start("native");
  const good = result({user_message_uuid:native.uuid});
  assert.deepEqual(inputs.pendingInputIDs(good),[native.uuid]);
  assert.deepEqual(inputs.pendingInputIDs(result({user_message_uuids:[native.uuid,"foreign"]})),[]);
  inputs.consume(good);
  assert.deepEqual(inputs.pendingInputIDs(good),[]);
});


test("malformed native classifications cannot coerce to a finite code", () => {
  for (const malformed of [["rate_limit"], { toString: () => "authentication_failed" }, null, 429, true]) {
    const f = new NativeFailure();
    f.observe(assistant(malformed), "native", ["input"]);
    assert.equal(f.observe(result(), "native", ["input"]), undefined);
  }
});
