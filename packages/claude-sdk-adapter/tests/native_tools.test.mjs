import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";
import { sdkSource, startupFrom } from "./executor_run.mjs";

registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return { url: "data:text/javascript," + encodeURIComponent(sdkSource), shortCircuit: true };
  return next(specifier, context);
}});
const { execute } = await import("../dist/adapter.js");
const { ExecutorTurns } = await import("../dist/executor_protocol.js");

const prepare = patch => ({ type: "executor_prepare", model: "model", system_prompt: "", cwd: process.cwd(), ...patch });
const selected = async patch => {
  const abort = new AbortController();
  globalThis.startupFixture = startupFrom(({ options }) => { globalThis.options = options; return { async *[Symbol.asyncIterator]() {} }; });
  await execute(prepare(patch), async () => {}, abort, new ExecutorTurns(async () => {}, abort));
  return globalThis.options;
};

test("environment none adds WebSearch, never WebFetch, when web search is live", async () => {
  for (const search of ["live", "disabled", undefined]) {
    const options = await selected({ web_search: search });
    assert.deepEqual(options.tools, search === "live" ? ["WebSearch"] : []);
    assert.deepEqual(options.allowedTools, options.tools);
  }
});
