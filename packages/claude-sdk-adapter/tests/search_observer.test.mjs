import assert from "node:assert/strict";
import test from "node:test";
import { SearchObserver } from "../dist/search_observer.js";

const assistant = (id, name, input) => ({ type: "assistant", session_id: "session", parent_tool_use_id: null,
  message: { content: [{ type: "tool_use", id, name, input }] } });
const user = (id, is_error = false) => ({ type: "user", session_id: "session", parent_tool_use_id: null,
  message: { content: [{ type: "tool_result", tool_use_id: id, content: "native result", is_error }] } });
const consume = (observer, message) => [...observer.consume(message, "session")];

test("WebSearch and WebFetch project as web search actions with a completion status", () => {
  const observer = new SearchObserver();
  assert.deepEqual(consume(observer, assistant("search", "WebSearch", { query: "agents api" })), [{ type: "search_observation", session_id: "session", id: "search", stage: "before",
    observation: { kind: "web_search", status: "in_progress", action: { type: "search", query: "agents api" } } }]);
  consume(observer, assistant("fetch", "WebFetch", { url: "https://example.com", prompt: "summarize" }));
  assert.throws(() => observer.assertComplete());
  assert.equal(consume(observer, user("search"))[0].observation.status, "completed");
  assert.deepEqual(consume(observer, user("fetch", true))[0].observation, { kind: "web_search", status: "failed", action: { type: "open_page", url: "https://example.com" } });
  observer.assertComplete();
  assert.deepEqual(observer.close("session"), []);
});

test("other tools, child work and replays are not observed; open calls close as incomplete", () => {
  const observer = new SearchObserver();
  for (const message of [assistant("bash", "Bash", { command: "true" }), { ...assistant("child", "WebSearch", { query: "q" }), parent_tool_use_id: "parent" },
    { ...assistant("replay", "WebSearch", { query: "q" }), isReplay: true }, assistant("blank", "WebSearch", {})]) {
    assert.deepEqual(consume(observer, message), []);
  }
  consume(observer, assistant("open", "WebSearch", { query: "q" }));
  assert.deepEqual(observer.close("session").map(event => [event.id, event.stage, event.observation.status]), [["open", "after", "incomplete"]]);
});
