import assert from "node:assert/strict";
import test from "node:test";
import { Inputs } from "../dist/inputs.js";

const result = (ids, session_id = "native") => ({ type: "result", session_id, user_message_uuids: ids });
const steer = (input_id, text = "additional text") => ({ type: "steer", input_id, input: [{ content: [{ type: "input_text", text }] }] });

test("queued input survives the first result and completes only with its own native result", async () => {
  const inputs = new Inputs([{ content: [{ type: "input_text", text: "opening text" }] }]);
  const stream = inputs[Symbol.asyncIterator]();
  const first = (await stream.next()).value;
  assert.deepEqual(inputs.start("native"), [{ type: "input_ready", session_id: "native" }]);
  assert.deepEqual(inputs.submit(steer("second")), []);
  // This result raced the SDK's read of the extra local input: native queue count is zero.
  assert.deepEqual(inputs.consume({ ...result([first.uuid]), queued_turn_count: 0 }), []);
  assert.equal(inputs.complete, false);
  assert.deepEqual(inputs.start("native"), []);
  assert.throws(() => inputs.start("different"), /identity/);
  const second = (await stream.next()).value;
  assert.equal(second.session_id, "native");
  assert.equal(second.message.content[0].text, "additional text");
  assert.notEqual(second.uuid, first.uuid);
  assert.deepEqual(inputs.consume({ type: "assistant", parent_tool_use_id: null, session_id: "native", user_message_uuid: second.uuid }), [{ type: "input_applied", input_id: "second" }]);
  assert.equal(inputs.complete, false);
  assert.deepEqual(inputs.consume(result([second.uuid])), [{ type: "input_closed", session_id: "native" }]);
  assert.equal(inputs.complete, true);
  assert.equal((await stream.next()).done, true);
  assert.deepEqual(inputs.submit(steer("too-late")), [{ type: "input_rejected", input_id: "too-late" }]);
});

test("native folds confirm all consumed UUIDs, never queue/user echoes or unrelated frames", async () => {
  const inputs = new Inputs([{ content: [{ type: "input_text", text: "first" }] }]);
  const stream = inputs[Symbol.asyncIterator]();
  const first = (await stream.next()).value;
  inputs.start("native"); inputs.submit(steer("fold"));
  const second = (await stream.next()).value;
  for (const frame of [
    { type: "command_lifecycle", state: "queued", uuid: second.uuid },
    { type: "user", uuid: second.uuid, isReplay: true },
    { type: "assistant", parent_tool_use_id: "subagent", user_message_uuid: second.uuid },
    { type: "assistant", parent_tool_use_id: null, isSynthetic: true, user_message_uuid: second.uuid },
    { type: "stream_event", parent_tool_use_id: null, user_message_uuid: "unknown" },
  ]) assert.deepEqual(inputs.consume({ session_id: "native", ...frame }), []);
  assert.equal(inputs.complete, false);
  assert.throws(() => inputs.consume(result([second.uuid], "another-session")), /session/);
  assert.throws(() => inputs.consume(result(["unknown"])), /Unattributed/);
  assert.deepEqual(inputs.consume(result([first.uuid, second.uuid])), [
    { type: "input_closed", session_id: "native" }, { type: "input_applied", input_id: "fold" },
  ]);
  assert.equal((await stream.next()).done, true);
});

test("reject before readiness, repeated identities and native receipt capacity without enqueueing", async () => {
  const inputs = new Inputs([{ content: [{ type: "input_text", text: "first" }] }]);
  assert.deepEqual(inputs.submit(steer("early")), [{ type: "input_rejected", input_id: "early" }]);
  inputs.start("native");
  for (let i = 0; i < 63; i++) assert.deepEqual(inputs.submit(steer(String(i))), []);
  assert.deepEqual(inputs.submit(steer("0", "changed")), [{ type: "input_rejected", input_id: "0" }]);
  assert.deepEqual(inputs.submit(steer("overflow")), [{ type: "input_rejected", input_id: "overflow" }]);
  assert.throws(() => inputs.submit({ ...steer("bad"), environment: {} }), /Invalid/);
  inputs.close();
  let count = 0; for await (const _ of inputs) count++;
  assert.equal(count, 64);
  assert.equal(inputs.complete, false);
});

test("ordered image batches keep message boundaries and aggregate native consumption", async () => {
  const { textInput } = await import("../dist/message_input.js");
  const inputs = new Inputs(textInput("opening"));
  const stream = inputs[Symbol.asyncIterator]();
  const first = (await stream.next()).value;
  inputs.start("native");
  const input = [{ content: [
    { type: "input_text", text: " before " },
    { type: "input_image", image_url: "data:image/png;base64,aW1hZ2U=" },
    { type: "input_text", text: " after " },
  ] }, ...textInput("second message")];
  assert.deepEqual(inputs.submit({ type: "steer", input_id: "batch", input }), []);
  const a = (await stream.next()).value, b = (await stream.next()).value;
  assert.deepEqual(a.message.content, [
    { type: "text", text: " before " },
    { type: "image", source: { type: "base64", media_type: "image/png", data: "aW1hZ2U=" } },
    { type: "text", text: " after " },
  ]);
  assert.deepEqual(b.message.content, [{ type: "text", text: "second message" }]);
  assert.deepEqual(inputs.consume(result([first.uuid, a.uuid])), []);
  assert.equal(inputs.complete, false);
  assert.deepEqual(inputs.consume({ type: "assistant", parent_tool_use_id: null, session_id: "native", user_message_uuids: [a.uuid] }), []);
  assert.deepEqual(inputs.consume(result([b.uuid])), [
    { type: "input_closed", session_id: "native" }, { type: "input_applied", input_id: "batch" },
  ]);
});

test("native UUID capacity rejects a whole batch and partial consumption does not apply it", async () => {
  const { textInput } = await import("../dist/message_input.js");
  const inputs = new Inputs(Array.from({ length: 63 }, () => textInput("opening")[0]));
  inputs.start("native");
  const pair = [...textInput("a"), ...textInput("b")];
  assert.deepEqual(inputs.submit({ type: "steer", input_id: "overflow", input: pair }), [{ type: "input_rejected", input_id: "overflow" }]);
  assert.deepEqual(inputs.submit(steer("last")), []);
  inputs.close();
  const queued = []; for await (const message of inputs) queued.push(message);
  assert.equal(queued.length, 64);
  assert.throws(() => new Inputs([...pair, ...Array.from({ length: 63 }, () => textInput("x")[0])]), /Invalid/);

  const partial = new Inputs(textInput("opening"));
  const stream = partial[Symbol.asyncIterator]();
  await stream.next(); partial.start("native");
  partial.submit({ type: "steer", input_id: "partial", input: pair });
  const first = (await stream.next()).value;
  assert.deepEqual(partial.consume(result([first.uuid])), []);
  partial.close();
  assert.equal(partial.complete, false);
});
