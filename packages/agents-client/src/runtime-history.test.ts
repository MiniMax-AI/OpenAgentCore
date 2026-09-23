import { describe, expect, it } from "vitest";

import { OpenAIAgentsClient } from "./client";

const sessionId = "11111111-1111-4111-8111-111111111111";
const environmentId = "22222222-2222-4222-8222-222222222222";
const allocationId = "33333333-3333-4333-8333-333333333333";

interface FetchCall {
  input: RequestInfo | URL;
  init?: RequestInit;
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

function clientFor(body: unknown, calls: FetchCall[] = []): OpenAIAgentsClient {
  return new OpenAIAgentsClient({
    baseUrl: "https://core.example/v1",
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init });
      return jsonResponse(body);
    }) as typeof fetch,
  });
}

function capabilities(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    object: "agent.runtime_history_capabilities",
    available: true,
    reason: null,
    collection_mode: "periodic",
    sample_interval_seconds: 30,
    retention_seconds: 604800,
    minimum_step_seconds: 30,
    maximum_range_seconds: 86400,
    maximum_points: 1000,
    metrics: ["cpu", "memory", "tokens"],
    ...overrides,
  };
}

function history(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  const first = {
    start: 1000,
    end: 1060,
    first_observed_at: 1010,
    last_observed_at: 1010,
    observation_count: 1,
    observed_count: 1,
    unavailable_count: 0,
  };
  const second = {
    start: 1060,
    end: 1120,
    first_observed_at: 1070,
    last_observed_at: 1070,
    observation_count: 1,
    observed_count: 0,
    unavailable_count: 1,
  };
  return {
    object: "agent.runtime_history",
    source: "durable",
    session_id: sessionId,
    requested_range: { start: 1000, end: 1120 },
    resolution_seconds: 60,
    generated_at: 1120,
    coverage: {
      retained_start: 1000,
      first_sample_at: 1010,
      last_sample_at: 1070,
      sample_count: 2,
      expected_sample_count: 4,
      buckets: [first, second],
    },
    series: [{
      environment_id: environmentId,
      allocation_id: allocationId,
      started_at: { seconds: 900, nanoseconds: 0 },
      provider_type: "docker",
      points: [{
        ...first,
        cpu: { contributor_count: 1, utilization_ratio: 0, capacity_cores: 2 },
        memory: { contributor_count: 1, usage_bytes: 0, limit_bytes: 2048 },
      }, { ...second, cpu: null, memory: null }],
    }],
    token_usage: [
      { start: 1000, end: 1060, sampled_at: 1010, input_tokens: 100, output_tokens: 20 },
      { start: 1060, end: 1120, sampled_at: 1070, input_tokens: 160, output_tokens: 50 },
    ],
    ...overrides,
  };
}

describe("Runtime history client", () => {
  it("retrieves strict capabilities without exposing backend details", async () => {
    const calls: FetchCall[] = [];
    await expect(clientFor(capabilities(), calls).getRuntimeHistoryCapabilities()).resolves.toEqual(capabilities());
    expect(String(calls[0]?.input)).toBe("https://core.example/v1/agents/runtime-history/capabilities");
  });

  it("retrieves bounded Session history and preserves observed zeroes", async () => {
    const calls: FetchCall[] = [];
    const controller = new AbortController();
    const response = history();
    const value = await clientFor(response, calls).retrieveRuntimeHistory(sessionId.toUpperCase(), {
      start: 1000,
      end: 1120,
      maxPoints: 60,
      signal: controller.signal,
    });
    expect(String(calls[0]?.input)).toBe(
      `https://core.example/v1/agents/sessions/${sessionId}/runtime-history?start=1000&end=1120&max_points=60`,
    );
    expect(calls[0]?.init?.signal).toBe(controller.signal);
    expect(value.series[0]?.points[0]?.cpu?.utilization_ratio).toBe(0);
    expect(value.series[0]?.points[0]?.memory?.usage_bytes).toBe(0);
    expect(value.series.map((series) => series.started_at)).toEqual([{ seconds: 900, nanoseconds: 0 }]);
    expect(value.coverage.sample_count).toBe(2);
    expect(value.token_usage[1]).toMatchObject({ input_tokens: 160, output_tokens: 50 });
  });

  it("rejects invalid requests before fetch", async () => {
    const calls: FetchCall[] = [];
    const client = clientFor(history(), calls);
    for (const [id, query] of [
      ["not-a-session", { start: 1000, end: 1120 }],
      [sessionId, { start: 1120, end: 1000 }],
      [sessionId, { start: -1, end: 1000 }],
      [sessionId, { start: 1000, end: 1120, maxPoints: 1 }],
      [sessionId, { start: 1000, end: 1120, maxPoints: 10001 }],
    ] as const) {
      await expect(client.retrieveRuntimeHistory(id, query)).rejects.toThrow(TypeError);
    }
    expect(calls).toHaveLength(0);
  });

  for (const [name, body] of [
    ["unknown field", capabilities({ backend: "postgres" })],
    ["inconsistent availability", capabilities({ available: false })],
    ["duplicate metrics", capabilities({ metrics: ["cpu", "cpu"] })],
    ["invalid retention", capabilities({ retention_seconds: 60, maximum_range_seconds: 120 })],
    ["unsafe maximum", capabilities({ maximum_points: 10001 })],
    ["unqualified periodic", capabilities({ sample_interval_seconds: null })],
  ] as const) {
    it(`rejects ${name} in capabilities`, async () => {
      await expect(clientFor(body).getRuntimeHistoryCapabilities()).rejects.toMatchObject({
        status: 502,
        code: "invalid_runtime_history_capabilities",
      });
    });
  }

  for (const [name, mutate] of [
    ["unknown field", (value: Record<string, unknown>) => { value.tokens = 10; }],
    ["foreign Session", (value: Record<string, unknown>) => { value.session_id = "55555555-5555-4555-8555-555555555555"; }],
    ["mismatched range", (value: Record<string, unknown>) => { value.requested_range = { start: 999, end: 1120 }; }],
    ["empty result generated before the requested range", (value: Record<string, unknown>) => {
      value.generated_at = 999;
      value.coverage = {
        retained_start: 1000, first_sample_at: null, last_sample_at: null,
        sample_count: 0, expected_sample_count: 0, buckets: [],
      };
      value.series = [];
    }],
    ["empty result retained after generation", (value: Record<string, unknown>) => {
      value.generated_at = 1050;
      value.coverage = {
        retained_start: 1060, first_sample_at: null, last_sample_at: null,
        sample_count: 0, expected_sample_count: 0, buckets: [],
      };
      value.series = [];
    }],
    ["empty incarnation newer than generation", (value: Record<string, unknown>) => {
      value.generated_at = 1050;
      value.coverage = {
        retained_start: 1000, first_sample_at: null, last_sample_at: null,
        sample_count: 0, expected_sample_count: 0, buckets: [],
      };
      const series = (value.series as Array<Record<string, unknown>>)[0]!;
      series.started_at = { seconds: 1051, nanoseconds: 0 };
      series.points = [];
    }],
    ["lossy incarnation fence", (value: Record<string, unknown>) => {
      (value.series as Array<Record<string, unknown>>)[0]!.started_at = 900;
    }],
    ["invalid incarnation nanoseconds", (value: Record<string, unknown>) => {
      (value.series as Array<Record<string, unknown>>)[0]!.started_at = { seconds: 900, nanoseconds: 1_000_000_000 };
    }],
    ["bucket ends at incarnation", (value: Record<string, unknown>) => {
      const series = (value.series as Array<{ started_at: unknown; points: Array<Record<string, unknown>> }>)[0]!;
      series.started_at = { seconds: 1060, nanoseconds: 0 };
      series.points[0] = {
        start: 1000, end: 1060, first_observed_at: null, last_observed_at: null,
        observation_count: 0, observed_count: 0, unavailable_count: 0, cpu: null, memory: null,
      };
    }],
    ["noncanonical allocation", (value: Record<string, unknown>) => {
      (value.series as Array<Record<string, unknown>>)[0]!.allocation_id = `urn:uuid:${allocationId}`;
    }],
    ["incorrect coverage total", (value: Record<string, unknown>) => { (value.coverage as Record<string, unknown>).sample_count = 3; }],
    ["series predates retention", (value: Record<string, unknown>) => {
      const coverage = value.coverage as { retained_start: number; first_sample_at: number; last_sample_at: number; sample_count: number; buckets: Array<Record<string, unknown>> };
      coverage.retained_start = 1020;
      coverage.first_sample_at = 1070;
      coverage.last_sample_at = 1070;
      coverage.sample_count = 1;
      coverage.buckets = [coverage.buckets[1]!];
    }],
    ["token sample predates retention", (value: Record<string, unknown>) => {
      const coverage = value.coverage as { retained_start: number; first_sample_at: number; last_sample_at: number; sample_count: number; buckets: Array<Record<string, unknown>> };
      coverage.retained_start = 1020;
      coverage.first_sample_at = 1070;
      coverage.last_sample_at = 1070;
      coverage.sample_count = 1;
      coverage.buckets = [coverage.buckets[1]!];
      const series = (value.series as Array<{ points: Array<Record<string, unknown>> }>)[0]!;
      series.points = series.points.slice(1);
      (value.token_usage as Array<Record<string, unknown>>)[0]!.sampled_at = 1010;
    }],
    ["overlapping buckets", (value: Record<string, unknown>) => {
      const buckets = (value.coverage as { buckets: Array<Record<string, unknown>> }).buckets;
      buckets[1]!.start = 1050;
    }],
    ["zero CPU capacity", (value: Record<string, unknown>) => {
      const point = (value.series as Array<{ points: Array<Record<string, unknown>> }>)[0]!.points[0]!;
      point.cpu = { contributor_count: 1, utilization_ratio: null, capacity_cores: 0 };
    }],
    ["unsafe memory", (value: Record<string, unknown>) => {
      const point = (value.series as Array<{ points: Array<Record<string, unknown>> }>)[0]!.points[0]!;
      point.memory = { contributor_count: 1, usage_bytes: Number.MAX_SAFE_INTEGER + 1, limit_bytes: 2048 };
    }],
    ["duplicate allocation with another start estimate", (value: Record<string, unknown>) => {
      const series = value.series as Array<Record<string, unknown>>;
      const duplicate = structuredClone(series[0]!);
      duplicate.started_at = { seconds: 901, nanoseconds: 0 };
      series.push(duplicate);
    }],
    ["mixed Environment scope", (value: Record<string, unknown>) => {
      const series = value.series as Array<Record<string, unknown>>;
      const foreign = structuredClone(series[0]!);
      foreign.environment_id = "44444444-4444-4444-8444-444444444444";
      foreign.allocation_id = "55555555-5555-4555-8555-555555555555";
      series.push(foreign);
    }],
    ["unsafe token usage", (value: Record<string, unknown>) => {
      (value.token_usage as Array<Record<string, unknown>>)[0]!.input_tokens = Number.MAX_SAFE_INTEGER + 1;
    }],
    ["token sample outside bucket", (value: Record<string, unknown>) => {
      (value.token_usage as Array<Record<string, unknown>>)[0]!.sampled_at = 1060;
    }],
    ["overlapping token buckets", (value: Record<string, unknown>) => {
      (value.token_usage as Array<Record<string, unknown>>)[1]!.start = 1050;
    }],
  ] as const) {
    it(`rejects ${name} in history`, async () => {
      const value = history();
      mutate(value);
      await expect(clientFor(value).retrieveRuntimeHistory(sessionId, {
        start: 1000,
        end: 1120,
        maxPoints: 60,
      })).rejects.toMatchObject({ status: 502, code: "invalid_runtime_history" });
    });
  }
});
