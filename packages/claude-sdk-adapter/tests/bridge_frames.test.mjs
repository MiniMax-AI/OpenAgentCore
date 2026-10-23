import assert from "node:assert/strict";
import test from "node:test";
import { ExecutorTurns } from "../dist/executor_protocol.js";
import { FunctionBridge } from "../dist/function_bridge.js";
import { Inputs } from "../dist/inputs.js";
import { parseRequest } from "../dist/request.js";
import { assertFrame, frames } from "./frames.mjs";

test("the bridge accepts every frame Go sends", async () => {
  for (const frame of frames.to_bridge.filter(frame => frame.type === "executor_prepare")) parseRequest(JSON.stringify(frame));

  const events = [];
  const turns = new ExecutorTurns(async event => { events.push(event); }, new AbortController());
  const functions = new FunctionBridge(async event => { events.push(event); });
  let inputs;
  turns.configure({ interrupt: async () => ({ still_queued: [] }) }, input => (inputs = new Inputs(input)), async payload => {
    if (payload.type === "steer") events.push(...inputs.submit(payload));
    else functions.submit(JSON.stringify(payload));
  });
  const [start, cancel, steer, result] = ["turn_start", "turn_cancel", "steer", "function_result"].map(type => frames.to_bridge.find(frame => frame.type === type));
  await turns.start(start);
  events.push(...inputs.start("native-session"));
  const call = functions.invoke({ id: result.call_id, name: "lookup", arguments: {} }, new AbortController().signal);
  await turns.submit(steer);
  await turns.submit(result);
  await call;
  await turns.cancel(cancel);
  await turns.settled(true, false, "native_error");
  assert.deepEqual(events.map(event => event.type), ["turn_started", "input_ready", "function_call", "turn_settled"]);
  for (const event of events) assertFrame(event);
});
