import assert from "node:assert/strict";
import { assertFrame } from "./frames.mjs";

// SDK module source for fixtures that run execute() in process: startup() and getSessionInfo() delegate to globals.
export const sdkSource = 'export async function getSessionInfo(...args){return globalThis.historyFixture?globalThis.historyFixture(...args):{sessionId:"native"};} export function startup(args){return globalThis.startupFixture(args);} export function query(){throw new Error("Unexpected direct query");}';

// Turns a controlled SDK stream, which reads `prompt` like the native Query, into the warm startup the Executor requires.
// The native child exits with exitCode once the Query closes.
export function startupFrom(queryFixture, { exitCode = 0, args } = {}) {
  return async ({ options }) => {
    const child = options.spawnClaudeCodeProcess({ command: process.execPath, env: options.env, signal: options.abortController.signal,
      args: args ?? ["-e", `process.stdin.resume();process.stdin.on('end',()=>process.exit(${exitCode}));`] });
    const close = () => child.stdin.end();
    return { close, query: prompt => Object.assign(queryFixture({ prompt, options }), {
      close, initializationResult: async () => ({ hooks_applied: true }), mcpServerStatus: async () => [],
      interrupt: async () => ({ still_queued: [] }) }) };
  };
}

// Runs one Turn through execute(). Returns the frames in order, without the lifecycle frames of readiness and settlement.
// onFrame may call turns.submit() or globalThis.cancelTurn() while the Turn runs.
export async function runTurn(request, abort, input, onFrame = async () => {}) {
  const { execute } = await import("../dist/adapter.js");
  const { ExecutorTurns } = await import("../dist/executor_protocol.js");
  const frames = [];
  let settled;
  const turns = new ExecutorTurns(async frame => {
    assertFrame(frame);
    if (frame.type === "turn_settled") settled = frame;
    else if (frame.type !== "executor_ready" && frame.type !== "turn_started") { const { turn_id, ...rest } = frame; frames.push(rest); }
    await onFrame(frame, turns);
  }, abort);
  globalThis.cancelTurn = () => turns.cancel({ type: "turn_cancel", turn_id: "run" });
  let ready;
  const readied = new Promise(resolve => { ready = resolve; });
  let finished = false;
  const done = execute(request, async frame => { assertFrame(frame); if (frame.type === "executor_ready") ready(); else frames.push(frame); }, abort, turns).finally(() => { finished = true; });
  await Promise.race([readied, done]);
  if (finished) return { frames, settled };
  await turns.start({ type: "turn_start", turn_id: "run", input });
  await done;
  return { frames, settled };
}
