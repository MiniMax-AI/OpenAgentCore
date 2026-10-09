import test from "node:test";
import assert from "node:assert/strict";
import { StructuredOutput } from "../dist/structured_output.js";
import { parseStart } from "../dist/request.js";

const stream = event => ({type:"stream_event", uuid:crypto.randomUUID(), session_id:"session", parent_tool_use_id:null, event});
const receipt = (id, error=false) => ({type:"user", uuid:`receipt-${id}`, session_id:"session", parent_tool_use_id:null,
  message:{role:"user", content:[{type:"tool_result", tool_use_id:id, is_error:error, content:"Structured output provided successfully"}]}});
const result = (raw, extra={}) => ({type:"result", uuid:"result", session_id:"session", subtype:"success", is_error:false,
  structured_output:JSON.parse(raw), result:'{"memory":"unrelated summary"}', ...extra});
const candidate = (id, raw, error=false, input=JSON.parse(raw)) => [
  stream({type:"message_start", message:{id:`message-${id}`}}),
  stream({type:"content_block_start", index:1, content_block:{type:"tool_use", id, name:"StructuredOutput", input:{}}}),
  ...[raw.slice(0, -2), raw.slice(-2)].map(partial_json => stream({type:"content_block_delta", index:1, delta:{type:"input_json_delta", partial_json}})),
  {type:"assistant", uuid:`snapshot-${id}`, session_id:"session", parent_tool_use_id:null,
    message:{id:`message-${id}`, role:"assistant", content:[{type:"tool_use", id, name:"StructuredOutput", input}]}},
  stream({type:"content_block_stop", index:1}),
  stream({type:"message_stop"}), receipt(id, error),
];
const consume = (observer, events) => events.forEach(event => observer.consume(event, "session"));

test("the acknowledged raw candidate wins over unrelated generic result JSON", () => {
  const raw = '{ "memory": "0123456789abcdef" }';
  const observer = new StructuredOutput();
  consume(observer, candidate("final", raw));
  assert.deepEqual(observer.complete(result(raw)), {type:"output_message", message:{id:"final", status:"completed", phase:"final_answer", text:raw}});
  assert.throws(() => observer.complete(result(raw)));
});

test("failed retries never publish; raw integer digits survive native binary64 validation", () => {
  const raw = '{"number":9007199254740993}';
  // Native equality is binary64: this proves byte fidelity, not exact-number schema validation.
  assert.equal(JSON.parse(raw).number, 9007199254740992);
  const observer = new StructuredOutput();
  consume(observer, candidate("retry", '{"number":0}', true));
  consume(observer, candidate("final", raw).slice(0, -1));
  assert.throws(() => observer.complete(result(raw)));
  observer.consume(receipt("final"), "session");
  assert.equal(observer.complete(result(raw)).message.text, raw);
});

test("streamed tool execution may acknowledge a snapshot before its block and message stop", () => {
  const raw = '{"memory":"value"}';
  const events = candidate("final", raw);
  const observer = new StructuredOutput();
  consume(observer, [...events.slice(0, 5), events[7]]);
  assert.throws(() => observer.complete(result(raw)));
  consume(observer, events.slice(5, 7));
  assert.equal(observer.complete(result(raw)).message.text, raw);
});

test("native malformed-input failure settles before a valid retry publishes", () => {
  const raw = '{"number":';
  const input = {__unparsedToolInput:{raw, len:raw.length}};
  const observer = new StructuredOutput();
  consume(observer, candidate("malformed", raw, true, input));
  consume(observer, candidate("final", '{"number":7}'));
  assert.equal(observer.complete(result('{"number":7}')).message.text, '{"number":7}');
  const invalid = new StructuredOutput();
  assert.throws(() => consume(invalid, candidate("malformed", raw, false, input)));
});

test("JSON transport normalizes negative zero without changing acknowledged raw text", () => {
  const raw = '{"number":-0,"nested":[-0,{"zero":-0}]}';
  const transported = JSON.parse(JSON.stringify(JSON.parse(raw)));
  const observer = new StructuredOutput();
  consume(observer, candidate("final", raw, false, transported));
  assert.equal(observer.complete(result(raw, {structured_output:transported})).message.text, raw);
  // JSON transport turns infinity into null; it must not be treated like zero.
  const overflowing = '{"number":1e999}';
  const invalid = new StructuredOutput();
  assert.throws(() => consume(invalid, candidate("overflow", overflowing, false, JSON.parse(JSON.stringify(JSON.parse(overflowing))))));
});

test("native connection retry abandons a closed partial before a confirmed replacement", () => {
  const raw = '{"number":7}';
  const events = candidate("abandoned", raw);
  // The native retry producer closes the partial without constructing a tool snapshot.
  const partial = [...events.slice(0, 3), ...events.slice(5, 7)];
  const observer = new StructuredOutput();
  consume(observer, partial);
  assert.throws(() => observer.complete(result(raw)));
  // Providers may reuse IDs on retry; the discarded prefix never became a native call.
  consume(observer, candidate("abandoned", raw));
  assert.equal(observer.complete(result(raw)).message.id, "abandoned");
  for (const late of [events[4], events[7]]) {
    const invalid = new StructuredOutput();
    consume(invalid, partial);
    assert.throws(() => invalid.consume(late, "session"));
  }
});

test("native retraction permits replacement raw input before its supersedes snapshot", () => {
  const raw = '{"number":7}';
  for (const lateReceipt of [false, true]) {
    const observer = new StructuredOutput();
    const old = candidate("retracted", raw);
    consume(observer, lateReceipt ? old.slice(0, -1) : old);
    const replacement = candidate("replacement", raw);
    replacement[4].supersedes = ["snapshot-retracted"];
    consume(observer, replacement);
    if (lateReceipt) observer.consume(old[7], "session");
    // The final banner repeats the earlier supersedes notice idempotently.
    observer.consume({type:"system", subtype:"model_refusal_fallback", session_id:"session", retracted_message_uuids:["snapshot-retracted","receipt-retracted"]}, "session");
    assert.equal(observer.complete(result(raw)).message.id, "replacement");
  }
});

test("a completed tool block survives native partial finalization without message_stop", () => {
  const raw = '{"number":7}';
  const events = candidate("final", raw);
  const observer = new StructuredOutput();
  consume(observer, [...events.slice(0, 6), {type:"assistant", session_id:"session", parent_tool_use_id:null,
    error:"server_error", message:{content:[{type:"text", text:"Connection lost mid-response."}]}}, events[7]]);
  assert.equal(observer.complete(result(raw)).message.text, raw);
});

test("retracted materialized tool identities cannot be reused or revived", () => {
  const raw = '{"number":7}';
  const observer = new StructuredOutput();
  consume(observer, candidate("retracted", raw));
  observer.consume({type:"system", subtype:"model_refusal_fallback", session_id:"session", retracted_message_uuids:["snapshot-retracted"]}, "session");
  assert.throws(() => observer.complete(result(raw)));
  assert.throws(() => consume(observer, candidate("retracted", raw)));
});

test("an assistant-only native fallback cannot supply original tool-input text", () => {
  const raw = '{"number":7}';
  const observer = new StructuredOutput();
  assert.throws(() => consume(observer, candidate("fallback", raw).slice(4)));
});

test("only live root events can confirm a candidate", () => {
  const raw = '{"memory":"value"}';
  for (const extra of [{isReplay:true}, {isSynthetic:true}, {parent_tool_use_id:"child"}, {session_id:"other"}]) {
    for (const index of [0, 1, 2, 4, 5, 7]) {
      const events = candidate("final", raw);
      events[index] = {...events[index], ...extra};
      const observer = new StructuredOutput();
      assert.throws(() => { consume(observer, events); observer.complete(result(raw)); });
    }
  }
});

test("incomplete, mismatched, duplicate and retracted native identities cannot publish", () => {
  const raw = '{"memory":"value"}';
  const mutations = [
    events => events.filter((_, i) => i !== 1),
    events => events.filter((_, i) => i !== 2),
    events => events.filter((_, i) => i !== 4),
    events => events.filter((_, i) => i !== 5),
    events => events.filter((_, i) => i !== 7),
    events => { events[2].event.index = 2; return events; },
    events => { events[4].message.id = "other"; return events; },
    events => { events[4].message.content[0].id = "other"; return events; },
    events => { events[4].message.content[0].input = {memory:"other"}; return events; },
    events => { events[4].aborted = true; return events; },
    events => { events[4].error = "server_error"; return events; },
    events => [...events.slice(0, 2), events[1], ...events.slice(2)],
    events => [...events.slice(0, 5), events[4], ...events.slice(5)],
    events => { events[3].uuid = events[2].uuid; return events; },
    events => [...events, events[7]],
    events => [...events, {...events[4], supersedes:["snapshot-final"]}],
    events => [...events, {type:"system", subtype:"model_refusal_fallback", session_id:"session", retracted_message_uuids:["receipt-final"]}],
  ];
  for (const mutate of mutations) {
    const observer = new StructuredOutput();
    assert.throws(() => { consume(observer, mutate(candidate("final", raw))); observer.complete(result(raw)); });
  }
});

test("failed, ambiguous and mismatched final results cannot publish", () => {
  const raw = '{"memory":"value"}';
  for (const extra of [{subtype:"error_max_structured_output_retries"}, {structured_output:undefined}, {structured_output:{memory:"other"}}, {is_error:true}, {session_id:"other"}]) {
    const observer = new StructuredOutput();
    consume(observer, candidate("final", raw));
    assert.throws(() => observer.complete(result(raw, extra)));
  }
  const ambiguous = new StructuredOutput();
  consume(ambiguous, [...candidate("first", raw).slice(0, -1), ...candidate("second", raw).slice(0, -1), receipt("first"), receipt("second")]);
  assert.throws(() => ambiguous.complete(result(raw)));
  const failed = new StructuredOutput();
  consume(failed, candidate("failed", raw, true));
  assert.throws(() => failed.complete(result(raw)));
  const observer = new StructuredOutput();
  assert.throws(() => { consume(observer, [...candidate("same", raw, true), ...candidate("same", raw)]); observer.complete(result(raw)); });
});

test('output configuration is a json_schema format',()=>{
 const r={type:'start',input: [{ content: [{ type: "input_text", text: 'hello' }] }],model:'model',system_prompt:'',cwd:'/tmp',output_format:{type:'json_schema',schema:{type:'object'}}};
 assert.deepEqual(parseStart(JSON.stringify(r)),r);
 for(const change of [{output_format:{type:'text',schema:{}}},{output_format:{type:'json_schema'}}]) assert.throws(()=>parseStart(JSON.stringify({...r,...change})));
});
