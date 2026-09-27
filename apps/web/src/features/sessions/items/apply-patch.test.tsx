import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { SessionItem } from "@agents-core-web/agents-client";

import { ApplyPatchDiffViewer } from "./ApplyPatchDiffViewer";
import { parseParsarApplyPatch } from "./apply-patch";
import { mergeFunctionSteps, ThreadItems, toolResult } from "./ItemRenderers";

const changes = [
  { path: "src/add.ts", kind: { type: "add", move_path: null }, diff: "--- /dev/null\n+++ b/src/add.ts\n@@ -0,0 +1,2 @@\n+one\n+two" },
  { path: "src/edit.ts", kind: { type: "update", move_path: null }, diff: "@@ -1 +1 @@\n-old\n+new" },
  { path: "src/delete.ts", kind: { type: "delete", move_path: null }, diff: "@@ -1 +0,0 @@\n-gone" },
];

function item(overrides: Partial<SessionItem> = {}): SessionItem {
  return { id: "patch", turn_id: "turn", type: "function_call", status: "completed", name: "apply_patch", call_id: "call", arguments: { changes }, ...overrides } as SessionItem;
}

describe("Parsar apply_patch parsing", () => {
  it("parses add, modify, delete, multiple files, and unified-diff counts", () => {
    const parsed = parseParsarApplyPatch({ changes });
    expect(parsed?.changes.map((change) => change.kind)).toEqual(["add", "modify", "delete"]);
    expect(parsed).toMatchObject({ additions: 3, deletions: 2 });
    expect(parsed?.changes[0]?.lines.map((line) => line.kind)).toContain("header");
    expect(parsed?.changes[0]?.lines.map((line) => line.kind)).toContain("hunk");
  });

  it.each([
    undefined,
    null,
    "{\"changes\":[]}",
    {},
    { changes: [] },
    { changes: [{ path: "x", diff: "+x" }] },
    { changes: [{ path: "x", kind: "add", diff: "+x" }] },
    { changes: [{ path: "x", kind: { type: "rename" }, diff: "+x" }] },
    { changes: [{ path: "x", kind: { type: "add" }, diff: "+x", alternate: true }] },
    { changes, provider: "another-core" },
  ])("fails closed for empty, malformed, string, missing, or alternate shapes", (value) => {
    expect(parseParsarApplyPatch(value)).toBeNull();
  });

  it("keeps XSS-like text and long lines as inert text", () => {
    const long = "x".repeat(20_000);
    const parsed = parseParsarApplyPatch({ changes: [{ path: "<img src=x onerror=alert(1)>", kind: { type: "update" }, diff: `+<script>alert(1)</script>${long}` }] });
    expect(parsed?.changes[0]?.path).toContain("<img");
    expect(parsed?.changes[0]?.diff).toContain(long);
    const html = renderToStaticMarkup(<ApplyPatchDiffViewer item={item({ arguments: { changes: parsed && [{ path: parsed.changes[0]?.path, kind: { type: "update" }, diff: parsed.changes[0]?.diff }] } })} patch={parsed!} result={{ ok: true }} />);
    expect(html).toContain("&lt;script&gt;");
    expect(html).not.toContain("<script>");
    expect(html).toContain("Raw arguments");
    expect(html).toContain("Raw result");
  });
});

describe("protocol-aware Item rendering", () => {
  it("merges function outputs and exposes running, success, and failure states", () => {
    const merged = mergeFunctionSteps([item({ status: "in_progress" }), item({ id: "out", type: "function_call_output", status: "completed", output: { applied: true }, duration_ms: 12 })]);
    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({ status: "completed", output: { applied: true }, duration_ms: 12 });
    expect(toolResult(merged[0]!)).toEqual({ output: { applied: true }, duration_ms: 12 });
    expect(renderToStaticMarkup(<ApplyPatchDiffViewer item={item({ status: "in_progress" })} patch={parseParsarApplyPatch({ changes })!} result={undefined} />)).toContain("In progress");
    expect(renderToStaticMarkup(<ApplyPatchDiffViewer item={item()} patch={parseParsarApplyPatch({ changes })!} result="ok" />)).toContain("Completed");
    expect(renderToStaticMarkup(<ApplyPatchDiffViewer item={item({ status: "failed" })} patch={parseParsarApplyPatch({ changes })!} result={{ error: "no" }} />)).toContain("Failed");
  });

  it("preserves message, command, MCP, function output, web search, and unknown fallbacks", () => {
    const values: SessionItem[] = [
      { id: "m", turn_id: "m", type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: "hello" }] },
      { id: "c", turn_id: "tools", type: "command_execution", status: "in_progress", command: "pwd", output: "/work" },
      { id: "f", turn_id: "tools", type: "function_call", status: "completed", name: "other", arguments: { value: 1 } },
      { id: "mcp", turn_id: "tools", type: "mcp_call", status: "completed", server_label: "docs", name: "read", arguments: { id: "x" } },
      { id: "out", turn_id: "tools", type: "function_call_output", status: "completed", output: "result" },
      { id: "web", turn_id: "tools", type: "web_search_call", status: "completed", action: { type: "search", query: "query" } },
      { id: "u", turn_id: "tools", type: "future_item" as SessionItem["type"], status: "completed", arguments: { raw: true } },
    ];
    const html = renderToStaticMarkup(<ThreadItems items={values} agentName="Agent" />);
    for (const text of ["hello", "pwd", "other", "docs read", "Function result", "query", "Unsupported", "future_item Item"]) expect(html).toContain(text);
    expect(html).not.toContain("raw");
    expect(html).not.toContain("OpenAgentCore apply patch diff");
  });

  it("uses generic JSON for another Core's same-name payload", () => {
    const html = renderToStaticMarkup(<ThreadItems items={[item({ status: "in_progress", arguments: { patch: "*** Begin Patch" } })]} agentName="Agent" />);
    expect(html).toContain("Begin Patch");
    expect(html).not.toContain("OpenAgentCore apply patch diff");
  });
});
