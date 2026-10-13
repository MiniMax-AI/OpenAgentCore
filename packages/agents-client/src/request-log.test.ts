import { describe, expect, it, vi } from "vitest";
import { AdminClient } from "./admin-client";
import { projectRequestLog } from "./request-log";

const entry = { request_id: `req_${"1".repeat(32)}`, trace_id: "2".repeat(32), started_at: "2026-10-13T10:00:00Z", method: "GET", route: "/v1/agents/{agent_id}", state: "active", status: null, duration_ms: 1.25, response_bytes: 0, error_code: null };
const log = { object: "core.request_log.list", instance_id: "instance-1", instance_started_at: "2026-10-13T09:00:00Z", surface: "public", capacity: 1000, retained_count: 1, total_matching: 1, has_more: false, data: [entry] };

describe("request log", () => {
  it("uses only the administration boundary and forwards filters and cancellation", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(log)));
    const signal = new AbortController().signal;
    await new AdminClient({ fetch }).listRequestLogs({ request_id: entry.request_id, surface: "public", outcome: "active", method: "GET", limit: 200, signal });
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe(`/core/v1/request-logs?limit=200&request_id=${entry.request_id}&outcome=active&surface=public&method=GET`);
    expect(init).toMatchObject({ signal, credentials: "same-origin", redirect: "error" });
    expect(new Headers(init?.headers).has("Authorization")).toBe(false);
    expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
  });

  it("preserves active/aborted HTTP 200 responses and null status without claiming Turn success", () => {
    expect(projectRequestLog(log).data[0]).toEqual(entry);
    for (const state of ["active", "aborted"]) {
      expect(projectRequestLog({ ...log, data: [{ ...entry, state, status: 200 }] }).data[0]).toMatchObject({ state, status: 200 });
    }
  });

  it.each([
    { state: "succeeded" }, { status: "200" }, { state: "completed", status: null }, { duration_ms: -1 },
    { response_bytes: -1 }, { request_id: "unsafe id" }, { trace_id: "unknown" }, { started_at: "not a timestamp" }, { body: "secret" },
  ])("rejects invalid or undeclared entry fields %j", (patch) => {
    expect(() => projectRequestLog({ ...log, data: [{ ...entry, ...patch }] })).toThrow();
  });

  it.each([
    { surface: "machine" }, { instance_started_at: "invalid" }, { retained_count: 1001 },
    { total_matching: 2 }, { has_more: true }, { data: [entry, entry], total_matching: 2, retained_count: 2 },
  ])("rejects inconsistent snapshots %j", (patch) => {
    expect(() => projectRequestLog({ ...log, ...patch })).toThrow();
  });
});
