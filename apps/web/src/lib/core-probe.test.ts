import { describe, expect, it } from "vitest";

import { isValidDirectCoreBaseUrl } from "./connection";
import { coreProbeUrl, probeCore } from "./core-probe";

interface FetchCall {
  input: RequestInfo | URL;
  init?: RequestInit;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function recordingFetch(response: Response, calls: FetchCall[]): typeof fetch {
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ input, init });
    return response;
  }) as typeof fetch;
}

function stalledBodyResponse(status: number, signal: AbortSignal): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      const failOnAbort = () => controller.error(signal.reason ?? new DOMException("Aborted", "AbortError"));
      if (signal.aborted) failOnAbort();
      else signal.addEventListener("abort", failOnAbort, { once: true });
    },
  });
  return new Response(body, { status, headers: { "Content-Type": "application/json" } });
}

const canonicalAgent = {
  id: "agent_probe",
  object: "agent",
  model: "fixture/model",
  name: "Probe Agent",
  instructions: "Read-only probe fixture",
  metadata: { fixture: "safe" },
  multi_agent: { enabled: false, max_concurrent_subagents: null } as {
    enabled: boolean;
    max_concurrent_subagents: number | null;
  },
  reasoning: {},
  service_tier: "auto",
  text: { format: { type: "text" }, verbosity: "medium" },
  tools: [] as unknown[],
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
};

function canonicalPage(data: Array<typeof canonicalAgent> = []) {
  return {
    object: "list",
    data,
    has_more: false,
    first_id: data[0]?.id ?? null,
    last_id: data.at(-1)?.id ?? null,
  };
}

describe("Core connection probe", () => {
  it("uses one read-only Agents API request with the required beta header", async () => {
    const calls: FetchCall[] = [];

    const result = await probeCore({
      baseUrl: "/v1/",
      fetch: recordingFetch(jsonResponse(canonicalPage()), calls),
    });

    expect(result).toEqual({ kind: "authenticated", executionReadiness: "unknown", httpStatus: 200 });
    expect(calls).toHaveLength(1);
    expect(String(calls[0]?.input)).toBe("/v1/agents?limit=1");
    expect(calls[0]?.init?.method).toBe("GET");
    expect(calls[0]?.init?.body).toBeUndefined();
    expect(calls[0]?.init?.cache).toBe("no-store");
    const headers = new Headers(calls[0]?.init?.headers);
    expect(headers.get("Accept")).toBe("application/json");
    expect(headers.get("OpenAI-Beta")).toBe("agents=v1");
    expect(headers.has("Authorization")).toBe(false);
  });

  it("uses a direct-mode bearer only in the authorization header", async () => {
    const calls: FetchCall[] = [];
    const token = "current-tab-secret";

    const result = await probeCore({
      baseUrl: "https://core.example/v1/",
      token,
      fetch: recordingFetch(jsonResponse(canonicalPage()), calls),
    });

    expect(result.kind).toBe("authenticated");
    expect(String(calls[0]?.input)).toBe("https://core.example/v1/agents?limit=1");
    expect(new Headers(calls[0]?.init?.headers).get("Authorization")).toBe(`Bearer ${token}`);
    expect(JSON.stringify(result)).not.toContain(token);
  });

  it("ignores a historical browser bearer on the local proxy path", async () => {
    const calls: FetchCall[] = [];

    const result = await probeCore({
      baseUrl: "/v1",
      token: "stale-browser-secret",
      fetch: recordingFetch(jsonResponse(canonicalPage()), calls),
    });

    expect(result.kind).toBe("authenticated");
    expect(new Headers(calls[0]?.init?.headers).has("Authorization")).toBe(false);
  });

  it("classifies 401 without reflecting an upstream secret-bearing message", async () => {
    const token = "do-not-reflect";
    const result = await probeCore({
      baseUrl: "https://core.example/v1",
      token,
      fetch: recordingFetch(
        jsonResponse({ error: { code: "invalid_api_key", message: `bad ${token}` } }, 401),
        [],
      ),
    });

    expect(result).toEqual({ kind: "unauthorized", executionReadiness: "unknown", httpStatus: 401 });
    expect(JSON.stringify(result)).not.toContain(token);
  });

  it.each([
    jsonResponse({ error: { code: "gateway_auth_required" } }, 401),
    new Response("proxy login required", { status: 401 }),
  ])("does not claim invalid_api_key for a non-canonical 401", async (response) => {
    const result = await probeCore({ baseUrl: "/v1", fetch: recordingFetch(response, []) });

    expect(result).toEqual({ kind: "http_error", executionReadiness: "unknown", httpStatus: 401 });
  });

  it("accepts a canonical non-empty Agents page", async () => {
    const result = await probeCore({
      baseUrl: "/v1",
      fetch: recordingFetch(jsonResponse(canonicalPage([canonicalAgent])), []),
    });

    expect(result).toEqual({ kind: "authenticated", executionReadiness: "unknown", httpStatus: 200 });
  });

  it("accepts a canonical SavedAgent whose required model field is an empty string", async () => {
    const result = await probeCore({
      baseUrl: "/v1",
      fetch: recordingFetch(jsonResponse(canonicalPage([{ ...canonicalAgent, model: "" }])), []),
    });

    expect(result).toEqual({ kind: "authenticated", executionReadiness: "unknown", httpStatus: 200 });
  });

  it("accepts MCP and unknown non-empty tool types for a basic-read probe", async () => {
    const tools = [
      { type: "mcp", server_label: "fixture-server" },
      { type: "future_tool_type", future_field: true },
    ];
    const result = await probeCore({
      baseUrl: "/v1",
      fetch: recordingFetch(jsonResponse(canonicalPage([{ ...canonicalAgent, tools }])), []),
    });

    expect(result).toEqual({ kind: "authenticated", executionReadiness: "unknown", httpStatus: 200 });
  });

  it.each([
    null,
    {},
    { type: "" },
  ])("rejects a malformed SavedAgent tool descriptor: %j", async (tool) => {
    const result = await probeCore({
      baseUrl: "/v1",
      fetch: recordingFetch(jsonResponse(canonicalPage([{ ...canonicalAgent, tools: [tool] }])), []),
    });

    expect(result).toEqual({ kind: "protocol_mismatch", executionReadiness: "unknown", httpStatus: 200 });
  });

  it.each([
    { enabled: true, max_concurrent_subagents: 1 },
    { enabled: true, max_concurrent_subagents: 4_294_967_295 },
    { enabled: false, max_concurrent_subagents: null },
  ])("accepts a canonical multi_agent relationship: %j", async (multiAgent) => {
    const result = await probeCore({
      baseUrl: "/v1",
      fetch: recordingFetch(jsonResponse(canonicalPage([{ ...canonicalAgent, multi_agent: multiAgent }])), []),
    });

    expect(result).toEqual({ kind: "authenticated", executionReadiness: "unknown", httpStatus: 200 });
  });

  it.each([
    { enabled: true, max_concurrent_subagents: null },
    { enabled: false, max_concurrent_subagents: 4 },
    { enabled: true, max_concurrent_subagents: 0 },
    { enabled: true, max_concurrent_subagents: 4_294_967_296 },
  ])("rejects an inconsistent or out-of-range multi_agent relationship: %j", async (multiAgent) => {
    const result = await probeCore({
      baseUrl: "/v1",
      fetch: recordingFetch(jsonResponse(canonicalPage([{ ...canonicalAgent, multi_agent: multiAgent }])), []),
    });

    expect(result).toEqual({ kind: "protocol_mismatch", executionReadiness: "unknown", httpStatus: 200 });
  });

  it.each([
    [jsonResponse({ error: { code: "invalid_beta" } }, 400), 400],
    [jsonResponse({ error: { code: "not_found" } }, 404), 404],
    [jsonResponse({ error: { code: "method_not_allowed" } }, 405), 405],
    [jsonResponse(canonicalPage(), 201), 201],
    [jsonResponse(canonicalPage(), 202), 202],
    [jsonResponse({ ok: true }), 200],
    [new Response("not json", { status: 200 }), 200],
  ])("classifies beta, route, and response-shape mismatches", async (response, status) => {
    const result = await probeCore({ baseUrl: "/v1", fetch: recordingFetch(response, []) });

    expect(result).toEqual({ kind: "protocol_mismatch", executionReadiness: "unknown", httpStatus: status });
  });

  it.each([
    { ...canonicalPage(), object: "collection" },
    { ...canonicalPage(), first_id: "wrong" },
    { ...canonicalPage(), last_id: 42 },
    { ...canonicalPage(), has_more: true },
    { ...canonicalPage(), data: [null], first_id: null, last_id: null },
    { ...canonicalPage([canonicalAgent]), data: [{ ...canonicalAgent, model: undefined }] },
    { ...canonicalPage([canonicalAgent]), data: [{ ...canonicalAgent, updated_at: undefined }] },
  ])("rejects a malformed Agents list envelope or item", async (payload) => {
    const result = await probeCore({ baseUrl: "/v1", fetch: recordingFetch(jsonResponse(payload), []) });

    expect(result).toEqual({ kind: "protocol_mismatch", executionReadiness: "unknown", httpStatus: 200 });
  });

  it("separates other HTTP failures from authentication and protocol failures", async () => {
    const result = await probeCore({
      baseUrl: "/v1",
      fetch: recordingFetch(jsonResponse({ error: { code: "unavailable" } }, 503), []),
    });

    expect(result).toEqual({ kind: "http_error", executionReadiness: "unknown", httpStatus: 503 });
  });

  it("classifies a network or CORS rejection without retrying", async () => {
    let calls = 0;
    const result = await probeCore({
      baseUrl: "https://core.example/v1",
      fetch: (async () => {
        calls += 1;
        throw new TypeError("Failed to fetch a private URL");
      }) as typeof fetch,
    });

    expect(result).toEqual({ kind: "unreachable", executionReadiness: "unknown" });
    expect(calls).toBe(1);
  });

  it.each([
    "http://core.example/v1",
    "https://core.example/v1?",
    "https://core.example/v1#",
    "https://@core.example/v1",
  ])("blocks an unsafe direct Core before sending a bearer or request: %s", async (baseUrl) => {
    let calls = 0;
    const result = await probeCore({
      baseUrl,
      token: "must-not-leave-browser",
      fetch: (async () => {
        calls += 1;
        throw new Error("must not request an unsafe URL");
      }) as typeof fetch,
    });

    expect(result).toEqual({ kind: "invalid_configuration", executionReadiness: "unknown" });
    expect(calls).toBe(0);
  });

  it("times out one stalled request without retrying", async () => {
    let calls = 0;
    const result = await probeCore({
      baseUrl: "/v1",
      timeoutMs: 10,
      fetch: (async (_input, init) => {
        calls += 1;
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () => reject(init.signal?.reason), { once: true });
        });
      }) as typeof fetch,
    });

    expect(result).toEqual({ kind: "unreachable", executionReadiness: "unknown" });
    expect(calls).toBe(1);
  });

  it.each([200, 503])("times out a stalled response body after HTTP %s headers without retrying", async (status) => {
    let calls = 0;
    const result = await probeCore({
      baseUrl: "/v1",
      timeoutMs: 10,
      fetch: (async (_input, init) => {
        calls += 1;
        const signal = init?.signal;
        if (!signal) throw new Error("probe signal missing");
        return stalledBodyResponse(status, signal);
      }) as typeof fetch,
    });

    expect(result).toEqual({ kind: "unreachable", executionReadiness: "unknown" });
    expect(calls).toBe(1);
  });

  it("passes through a caller abort while a response body is stalled", async () => {
    const controller = new AbortController();
    let calls = 0;
    const pending = probeCore({
      baseUrl: "/v1",
      signal: controller.signal,
      timeoutMs: 1_000,
      fetch: (async (_input, init) => {
        calls += 1;
        const signal = init?.signal;
        if (!signal) throw new Error("probe signal missing");
        return stalledBodyResponse(200, signal);
      }) as typeof fetch,
    });
    const abortTimer = globalThis.setTimeout(() => controller.abort(), 0);

    try {
      await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    } finally {
      globalThis.clearTimeout(abortTimer);
    }
    expect(calls).toBe(1);
  });

  it("passes through aborts so stale probes can be fenced", async () => {
    const controller = new AbortController();
    controller.abort();

    await expect(
      probeCore({
        baseUrl: "/v1",
        signal: controller.signal,
        fetch: (async (_input, init) => {
          expect(init?.signal?.aborted).toBe(true);
          throw init?.signal?.reason ?? new DOMException("Aborted", "AbortError");
        }) as typeof fetch,
      }),
    ).rejects.toMatchObject({ name: "AbortError" });
  });
});

describe("Core probe URL validation", () => {
  it("normalizes the probe path without putting credentials in the URL", () => {
    expect(coreProbeUrl("https://core.example/v1///")).toBe("https://core.example/v1/agents?limit=1");
  });

  it.each([
    "https://core.example/v1",
    "http://127.0.0.1:8091/v1/",
    "https://core.example/v1%3Ftenant%3Dsafe",
    "https://core.example/v1%23section",
  ])("accepts an explicit HTTP(S) direct Core URL", (value) => {
    expect(isValidDirectCoreBaseUrl(value)).toBe(true);
  });

  it.each([
    "",
    "/v1",
    "ftp://core.example/v1",
    "https://user:secret@core.example/v1",
    "https://@core.example/v1",
    "https://core.example/v1?",
    "https://core.example/v1#",
    "https://core.example/v1?token=secret",
    "https://core.example/v1#secret",
  ])("rejects an unsafe or non-direct Core URL", (value) => {
    expect(isValidDirectCoreBaseUrl(value)).toBe(false);
  });
});
