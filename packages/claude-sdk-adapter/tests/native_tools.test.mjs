import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

const sdk = 'export async function getSessionInfo(){return {sessionId:"native"};} export function query(call){globalThis.options=call.options;return {close(){},async *[Symbol.asyncIterator](){}};} export function startup(){throw new Error("Unexpected preparation");}';
registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return { url: "data:text/javascript," + encodeURIComponent(sdk), shortCircuit: true };
  return next(specifier, context);
}});
const { execute } = await import("../dist/adapter.js");

const start = patch => ({ type: "start", input: [{ content: [{ type: "input_text", text: "hello" }] }], model: "model", system_prompt: "", cwd: process.cwd(), ...patch });
const selected = async patch => { await execute(start(patch), async () => {}, new AbortController()); return globalThis.options; };

test("environment none adds WebSearch, never WebFetch, when web search is live", async () => {
  for (const search of ["live", "disabled", undefined]) {
    const options = await selected({ web_search: search });
    assert.deepEqual(options.tools, search === "live" ? ["WebSearch"] : []);
    assert.deepEqual(options.allowedTools, options.tools);
  }
});
