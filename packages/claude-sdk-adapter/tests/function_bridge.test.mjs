import assert from "node:assert/strict";
import test from "node:test";
import { FunctionBridge } from "../dist/function_bridge.js";

const call = id => ({ id, name: "lookup", arguments: { ids: ["same"] } });
const result = (id, success = true) => ({ type: "function_result", call_id: id, delivery_id: `delivery-${id}`, success,
  content: [{ type: "input_text", text: `first-${id}` }, { type: "input_text", text: `second-${id}` }] });
const native = (value, content) => ({ type: "user", parent_tool_use_id: null, session_id: "native",
  message: { role: "user", content: [{ type: "tool_result", tool_use_id: value.call_id,
    is_error: !value.success, content: content ?? (value.success ? value.content.map(part => ({ type: "text", text: part.text })) : value.content.map(part => part.text).join("\n")) }] } });

test("identical calls accept reversed results but require matching live native responses", { timeout: 5000 }, async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const first = bridge.invoke(call("a"), new AbortController().signal);
  const second = bridge.invoke(call("b"), new AbortController().signal);
  for (const value of [result("b", false), result("a")]) bridge.submit(JSON.stringify(value));
  const [a, b] = await Promise.all([first, second]);
  assert.deepEqual(a.content.map(part => part.text), ["first-a", "second-a"]);
  assert.equal(b.isError, true);
  assert.deepEqual(events.map(event => event.type), ["function_call", "function_call"]);
  assert.throws(() => bridge.assertComplete(), /unconfirmed/);
  await bridge.consume({ ...native(result("a")), isReplay: true }, "native");
  await bridge.consume(native(result("a")), "other-session");
  await bridge.consume({ ...native(result("a")), parent_tool_use_id: "child" }, "native");
  assert.equal(events.length, 2);
  await assert.rejects(bridge.consume(native(result("a"), [{ type: "text", text: "wrong" }]), "native"), /differs/);
  await bridge.consume(native(result("b", false)), "native");
  await bridge.consume(native(result("a")), "native");
  assert.deepEqual(events.slice(2), [
    { type: "function_applied", call_id: "b", delivery_id: "delivery-b" },
    { type: "function_applied", call_id: "a", delivery_id: "delivery-a" },
  ]);
  bridge.assertComplete();
  await assert.rejects(bridge.invoke(call("a"), new AbortController().signal), /Repeated/);
});

test("invalid results preserve pending calls, cancellation and close never acknowledge application", { timeout: 5000 }, async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const abort = new AbortController();
  const cancelled = bridge.invoke(call("a"), abort.signal);
  const stopped = bridge.invoke(call("b"), new AbortController().signal);
  const cancelCheck = assert.rejects(cancelled, /aborted/);
  const stopCheck = assert.rejects(stopped, /ended/);
  for (const bad of [null, { ...result("a"), success: null }, { ...result("a"), content: null },
    { ...result("a"), content: [{ type: "input_image", image_url: "https://example.invalid/image" }] }, result("unknown")]) {
    assert.throws(() => bridge.submit(JSON.stringify(bad)));
  }
  abort.abort();
  bridge.close();
  await Promise.all([cancelCheck, stopCheck]);
  await bridge.consume(native(result("a")), "native");
  assert.deepEqual(events.map(event => event.type), ["function_call", "function_call"]);
});

test("an aborted submitted result never becomes a native application receipt", { timeout: 5000 }, async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const abort = new AbortController();
  const waiting = bridge.invoke(call("a"), abort.signal);
  bridge.submit(JSON.stringify(result("a", false)));
  await waiting;
  abort.abort();
  bridge.cancelUnanswered();
  assert.throws(() => bridge.assertComplete(), /unconfirmed/);
  await bridge.consume(native(result("a", false)), "native", true);
  assert.throws(() => bridge.assertComplete(), /unconfirmed/);
  assert.deepEqual(events.map(event => event.type), ["function_call"]);
});


test("successful image results confirm native preprocessing with unchanged identity and block order", async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const value = { ...result("visual"), content: [
    { type: "input_text", text: "before" },
    { type: "input_image", image_url: "data:image/png;base64,AQID" },
    { type: "input_text", text: "after" },
  ] };
  const waiting = bridge.invoke(call("visual"), new AbortController().signal);
  bridge.submit(JSON.stringify(value));
  assert.deepEqual((await waiting).content, [
    { type: "text", text: "before" }, { type: "image", mimeType: "image/png", data: "AQID" },
    { type: "text", text: "after" },
  ]);
  const converted = [
    { type: "text", text: "before" },
    { type: "image", source: { type: "base64", media_type: "image/jpeg", data: "BAUG" } },
    { type: "text", text: "after" },
  ];
  for (const bad of [converted.slice(1), [...converted, converted[0]], [converted[2], converted[1], converted[0]]]) {
    await assert.rejects(bridge.consume(native(value, bad), "native"), /differs/);
  }
  await bridge.consume({ ...native(value, converted), isSynthetic: true }, "native");
  await bridge.consume({ ...native(value, converted), isReplay: true }, "native");
  await bridge.consume(native(value, converted), "other");
  assert.equal(events.length, 1);
  await bridge.consume(native(value, converted), "native");
  assert.deepEqual(events[1], { type: "function_applied", call_id: "visual", delivery_id: "delivery-visual" });
  bridge.assertComplete();
});

test("native error-image rejection leaves the pending call available for a supported result", async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const waiting = bridge.invoke(call("error"), new AbortController().signal);
  const value = { ...result("error", false), content: [{ type: "input_image", image_url: "data:image/png;base64,AQID" }] };
  assert.throws(() => bridge.submit(JSON.stringify(value)), /cannot retain images/);
  bridge.submit(JSON.stringify(result("error", false)));
  assert.equal((await waiting).isError, true);
  await bridge.consume(native(result("error", false)), "native");
  bridge.assertComplete();
});

for (const terminal of ["tool_result", "result"]) test(`native cancellation ${terminal} settles unanswered callbacks without application`, async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const waiting = bridge.invoke(call("a"), new AbortController().signal);
  const rejected = assert.rejects(waiting, /cancelled/);
  if (terminal === "tool_result") {
    const cancellation = native(result("a", false), "Interrupted");
    await assert.rejects(bridge.consume(cancellation, "native"), /preceded/);
    await bridge.consume(cancellation, "native", true);
  } else bridge.cancelUnanswered();
  await rejected;
  bridge.assertComplete();
  assert.deepEqual(events.map(event => event.type), ["function_call"]);
});

test("native cancellation preserves a submitted result's missing receipt", async () => {
  const bridge = new FunctionBridge(async () => {});
  const waiting = bridge.invoke(call("a"), new AbortController().signal);
  bridge.submit(JSON.stringify(result("a")));
  await waiting;
  bridge.cancelUnanswered();
  assert.throws(() => bridge.assertComplete(), /unconfirmed/);
  bridge.close();
});

test("a Subagent call carries its child, is confirmed by the child's own result and is withdrawn when native abandons it unanswered", { timeout: 5000 }, async () => {
  const events = [], child = { native_id: "child", turn_id: "child-turn" };
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const abort = new AbortController();
  const kept = bridge.invoke({ ...call("a"), child }, new AbortController().signal);
  const dropped = bridge.invoke({ ...call("b"), child }, abort.signal);
  const droppedCheck = assert.rejects(dropped, /aborted/);
  assert.deepEqual(events[0], { type: "function_call", call: { call_id: "a", name: "lookup", arguments: { ids: ["same"] }, ...child } });
  bridge.submit(JSON.stringify(result("a")));
  await bridge.consume(native(result("a")), "native");
  assert.equal(events.length, 2);
  await bridge.consume({ ...native(result("a")), parent_tool_use_id: "spawn" }, "native");
  assert.equal(events.length, 3);
  assert.deepEqual(events[2], { type: "function_applied", call_id: "a", delivery_id: "delivery-a" });
  await kept;
  abort.abort();
  await droppedCheck;
  assert.deepEqual(events[3], { type: "function_withdrawn", withdrawn: { call_id: "b", ...child } });
  bridge.submit(JSON.stringify(result("b")));
  bridge.assertComplete();
  const late = new AbortController(), delivered = bridge.invoke({ ...call("c"), child }, late.signal);
  bridge.submit(JSON.stringify(result("c")));
  await delivered;
  late.abort();
  assert.throws(() => bridge.assertComplete(), /unconfirmed/);
});
