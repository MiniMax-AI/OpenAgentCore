import { describe, expect, it } from "vitest";

import { AgentCoreError, type AgentSession, type RuntimeHistory, type RuntimeObservation } from "@oac/agents-client";

import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";
import {
  loadRuntimeDurableSnapshot,
  runtimeDurableTrendSamples,
  RuntimeDurableHistoryIncompleteError,
  type RuntimeHistoryReader,
} from "./runtime-history";

const sessionID = "11111111-1111-4111-8111-111111111111";
const environmentID = "22222222-2222-4222-8222-222222222222";
const allocationID = "33333333-3333-4333-8333-333333333333";

const session = {
  id: sessionID,
  object: "agent.session",
  metadata: { title: "Durable worker" },
  agent: {
    id: "agent-1",
    name: "Worker",
    model: "fixture/model",
    instructions: null,
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [],
  },
  environment: { type: "none" },
  status: "idle",
  error: null,
  required_actions: [],
  vault_ids: [],
  usage: null,
  created_at: 1,
  last_active_at: 1,
} as AgentSession;

const observation = {
  id: sessionID,
  session_id: sessionID,
  environment_id: environmentID,
  mode: "openai_hosted",
  status: "observed",
} as RuntimeObservation;

function history(overrides: Partial<RuntimeHistory> = {}): RuntimeHistory {
  return {
    object: "agent.runtime_history",
    source: "durable",
    session_id: sessionID,
    requested_range: { start: 100, end: 160 },
    resolution_seconds: 30,
    generated_at: 161,
    coverage: {
      retained_start: 100,
      first_sample_at: 110,
      last_sample_at: 140,
      sample_count: 2,
      expected_sample_count: 2,
      buckets: [{
        start: 100, end: 130, first_observed_at: 110, last_observed_at: 110,
        observation_count: 1, observed_count: 1, unavailable_count: 0,
      }, {
        start: 130, end: 160, first_observed_at: 140, last_observed_at: 140,
        observation_count: 1, observed_count: 1, unavailable_count: 0,
      }],
    },
    series: [{
      environment_id: environmentID,
      allocation_id: allocationID,
      started_at: { seconds: 10, nanoseconds: 500_000_000 },
      provider_type: "docker",
      points: [{
        start: 100, end: 130, first_observed_at: 110, last_observed_at: 110,
        observation_count: 1, observed_count: 1, unavailable_count: 0,
        cpu: { contributor_count: 1, utilization_ratio: .25, capacity_cores: 2 },
        memory: { contributor_count: 1, usage_bytes: 512, limit_bytes: 1_024 },
      }, {
        start: 130, end: 160, first_observed_at: 140, last_observed_at: 140,
        observation_count: 1, observed_count: 1, unavailable_count: 0,
        cpu: { contributor_count: 1, utilization_ratio: .5, capacity_cores: 2 },
        memory: { contributor_count: 1, usage_bytes: 768, limit_bytes: 1_024 },
      }],
    }],
    token_usage: [
      { start: 100, end: 130, sampled_at: 110, input_tokens: 10, output_tokens: 5 },
      { start: 130, end: 160, sampled_at: 140, input_tokens: 40, output_tokens: 15 },
    ],
    ...overrides,
  };
}

describe("Runtime Durable Dashboard history", () => {
  it("projects persisted Runtime and canonical token history", () => {
    const samples = runtimeDurableTrendSamples([session], [history()]);
    expect(samples).toHaveLength(2);
    expect(samples[0]).toMatchObject({
      sampledAt: 130_000,
      activeSandboxCount: 1,
      memoryUsageBytes: 512,
      memoryLimitBytes: 1_024,
      inputTokensPerMinute: null,
      outputTokensPerMinute: null,
    });
    expect(samples[0]?.targets).toEqual([
      expect.objectContaining({ label: "Durable worker", cpuRatio: .25 }),
    ]);
    expect(samples[1]).toMatchObject({ inputTokensPerMinute: 60, outputTokensPerMinute: 20 });
  });

  it("counts and aggregates distinct Runtime allocations within one Session", () => {
    const source = history();
    const secondSeries = {
      ...source.series[0]!,
      allocation_id: "55555555-5555-4555-8555-555555555555",
      points: source.series[0]!.points.map((point) => ({
        ...point,
        memory: point.memory ? { ...point.memory, usage_bytes: 128, limit_bytes: 256 } : null,
      })),
    };
    const samples = runtimeDurableTrendSamples([session], [{
      ...source,
      series: [source.series[0]!, secondSeries],
    }]);

    expect(samples[0]).toMatchObject({
      activeSandboxCount: 2,
      memoryUsageBytes: 640,
      memoryLimitBytes: 1_280,
    });
  });

  it("deduplicates one Runtime allocation repeated across Session histories", () => {
    const second = { ...session, id: "44444444-4444-4444-8444-444444444444" } as AgentSession;
    const repeated = history({
      session_id: second.id,
      series: [{
        ...history().series[0]!,
        points: history().series[0]!.points.map((point) => ({
          ...point,
          memory: point.memory ? { ...point.memory, usage_bytes: 128, limit_bytes: 256 } : null,
        })),
      }],
    });
    const samples = runtimeDurableTrendSamples([session, second], [history(), repeated]);

    expect(samples[0]).toMatchObject({
      activeSandboxCount: 1,
      memoryUsageBytes: 128,
      memoryLimitBytes: 256,
    });
  });

  it("projects an unavailable retained observation as zero active Sandboxes", () => {
    const source = history();
    source.series[0]!.points[1] = {
      ...source.series[0]!.points[1]!, observed_count: 0, unavailable_count: 1, cpu: null, memory: null,
    };
    source.coverage.buckets[1] = {
      ...source.coverage.buckets[1]!, observed_count: 0, unavailable_count: 1,
    };
    const samples = runtimeDurableTrendSamples([session], [source]);
    expect(samples[1]?.activeSandboxCount).toBe(0);
  });

  it("aggregates observed memory without letting an unavailable target erase it", () => {
    const second = { ...session, id: "44444444-4444-4444-8444-444444444444" } as AgentSession;
    const secondHistory = history({
      session_id: second.id,
      coverage: { ...history().coverage, sample_count: 0, first_sample_at: null, last_sample_at: null },
      series: [],
    });
    const samples = runtimeDurableTrendSamples([session, second], [history(), secondHistory]);
    expect(samples.every((sample) => sample.memoryUsageBytes !== null && sample.memoryLimitBytes !== null)).toBe(true);
    expect(samples[0]).toMatchObject({ memoryUsageBytes: 512, memoryLimitBytes: 1_024, activeSandboxCount: 1 });
  });

  it("keeps omitted buckets between distant observations as gaps", () => {
    const source = history();
    const secondPoint = {
      ...source.series[0]!.points[1]!,
      start: 400, end: 430, first_observed_at: 410, last_observed_at: 410,
    };
    const sparse = history({
      requested_range: { start: 100, end: 430 },
      generated_at: 431,
      coverage: {
        ...source.coverage,
        last_sample_at: 410,
        expected_sample_count: 11,
        buckets: [source.coverage.buckets[0]!, {
          ...source.coverage.buckets[1]!,
          start: 400, end: 430, first_observed_at: 410, last_observed_at: 410,
        }],
      },
      series: [{ ...source.series[0]!, points: [source.series[0]!.points[0]!, secondPoint] }],
      token_usage: [
        source.token_usage[0]!,
        { ...source.token_usage[1]!, start: 400, end: 430, sampled_at: 410 },
      ],
    });
    const samples = runtimeDurableTrendSamples([session], [sparse]);
    expect(samples.map((sample) => sample.sampledAt)).toEqual(
      Array.from({ length: 11 }, (_, index) => (130 + index * 30) * 1_000),
    );
    expect(samples[0]?.targets.find((target) => target.cpuRatio !== null)?.cpuRatio).toBe(.25);
    expect(samples[10]?.targets.find((target) => target.cpuRatio !== null)?.cpuRatio).toBe(.5);
    expect(samples[10]?.inputTokensPerMinute).toBeNull();
    expect(samples[10]?.outputTokensPerMinute).toBeNull();
    for (const sample of samples.slice(1, -1)) {
      expect(sample).toMatchObject({
        activeSandboxCount: null, targets: [], memoryUsageBytes: null, memoryLimitBytes: null,
        inputTokensPerMinute: null, outputTokensPerMinute: null,
      });
    }
  });

  it("includes leading and trailing gaps with a shortened final bucket", () => {
    const samples = runtimeDurableTrendSamples([session], [history({
      requested_range: { start: 70, end: 205 },
      generated_at: 206,
    })]);
    expect(samples.map((sample) => sample.sampledAt)).toEqual([100_000, 130_000, 160_000, 190_000, 205_000]);
    expect(samples.map((sample) => sample.memoryUsageBytes)).toEqual([null, 512, 768, null, null]);
    expect(samples.map((sample) => sample.targets.length)).toEqual([0, 1, 1, 0, 0]);
    expect(samples.map((sample) => sample.activeSandboxCount)).toEqual([null, 1, 1, null, null]);
  });

  it("represents an entirely missing range without fabricating zero measurements", () => {
    const samples = runtimeDurableTrendSamples([session], [history({
      requested_range: { start: 100, end: 175 },
      generated_at: 176,
      coverage: {
        retained_start: 100, first_sample_at: null, last_sample_at: null,
        sample_count: 0, expected_sample_count: 3, buckets: [],
      },
      series: [],
      token_usage: [],
    })]);
    expect(samples.map((sample) => sample.sampledAt)).toEqual([130_000, 160_000, 175_000]);
    for (const sample of samples) {
      expect(sample).toMatchObject({
        activeSandboxCount: null, targets: [], memoryUsageBytes: null, memoryLimitBytes: null,
        inputTokensPerMinute: null, outputTokensPerMinute: null,
      });
    }
  });

  it("keeps aggregate token throughput absent when any queried Session lacks usage", () => {
    const second = { ...session, id: "44444444-4444-4444-8444-444444444444" } as AgentSession;
    const secondHistory = history({ session_id: second.id, token_usage: [] });
    const samples = runtimeDurableTrendSamples([session, second], [history(), secondHistory]);
    expect(samples.every((sample) => sample.inputTokensPerMinute === null && sample.outputTokensPerMinute === null)).toBe(true);
  });

  it("derives each Session token rate from its actual sample interval", () => {
    const samples = runtimeDurableTrendSamples([session], [history({
      token_usage: [
        { start: 100, end: 130, sampled_at: 105, input_tokens: 10, output_tokens: 5 },
        { start: 130, end: 160, sampled_at: 150, input_tokens: 40, output_tokens: 20 },
      ],
    })]);
    expect(samples[1]).toMatchObject({ inputTokensPerMinute: 40, outputTokensPerMinute: 20 });
  });

  it("keeps a token counter regression as a gap instead of inventing throughput", () => {
    const samples = runtimeDurableTrendSamples([session], [history({
      token_usage: [
        { start: 100, end: 130, sampled_at: 110, input_tokens: 100, output_tokens: 20 },
        { start: 130, end: 160, sampled_at: 140, input_tokens: 90, output_tokens: 30 },
      ],
    })]);
    expect(samples[1]).toMatchObject({ inputTokensPerMinute: null, outputTokensPerMinute: 20 });
  });

  it("loads each hosted Session's history through its project with a bounded common range", async () => {
    const calls: Array<{ sessionID: string; start: number; end: number; maxPoints?: number }> = [];
    const reader: RuntimeHistoryReader = {
      retrieveRuntimeHistory: async (id, query) => {
        calls.push({ sessionID: id, start: query.start, end: query.end, maxPoints: query.maxPoints });
        return history({ requested_range: { start: query.start, end: query.end } });
      },
    };
    const readers: string[] = [];
    const snapshot: RuntimeDashboardSnapshot = { sessions: [session], observations: [observation], owners: new Map([[sessionID, "proj_a"]]), loadedAt: 1 };
    const result = await loadRuntimeDurableSnapshot((id) => { readers.push(snapshot.owners?.get(id) ?? ""); return reader; }, snapshot, 60 * 60_000, undefined, () => 7_200_000);
    expect(readers).toEqual(["proj_a"]);
    expect(calls).toEqual([{ sessionID, start: 3_600, end: 7_200, maxPoints: 120 }]);
    expect(result).toMatchObject({ rangeStart: 3_600_000, rangeEnd: 7_200_000, targetCount: 1, sampleCount: 2 });
  });

  it("queries each managed Session only once when observations contain duplicates", async () => {
    const queried: string[] = [];
    const reader: RuntimeHistoryReader = {
      retrieveRuntimeHistory: async (id) => {
        queried.push(id);
        return history();
      },
    };
    const snapshot: RuntimeDashboardSnapshot = {
      sessions: [session],
      observations: [observation, { ...observation }],
      loadedAt: 1,
    };
    await loadRuntimeDurableSnapshot(() => reader, snapshot, 60 * 60_000);
    expect(queried).toEqual([sessionID]);
  });

  it("reports history as unavailable when Core keeps none for any Session", async () => {
    const reader: RuntimeHistoryReader = {
      retrieveRuntimeHistory: async () => { throw new AgentCoreError("No history.", 404); },
    };
    const snapshot: RuntimeDashboardSnapshot = { sessions: [session], observations: [observation], loadedAt: 1 };
    await expect(loadRuntimeDurableSnapshot(() => reader, snapshot, 60 * 60_000)).resolves.toBeNull();
  });

  it("leaves out a Session without history and keeps the others", async () => {
    const otherID = "44444444-4444-4444-8444-444444444444";
    const other = { ...session, id: otherID } as AgentSession;
    const reader: RuntimeHistoryReader = {
      retrieveRuntimeHistory: async (id) => {
        if (id === otherID) throw new AgentCoreError("No history.", 404);
        return history();
      },
    };
    const snapshot: RuntimeDashboardSnapshot = {
      sessions: [session, other],
      observations: [observation, { ...observation, id: otherID, session_id: otherID }],
      loadedAt: 1,
    };
    await expect(loadRuntimeDurableSnapshot(() => reader, snapshot, 60 * 60_000)).resolves.toMatchObject({ targetCount: 1 });
  });

  it("propagates other history failures", async () => {
    const reader: RuntimeHistoryReader = {
      retrieveRuntimeHistory: async () => { throw new AgentCoreError("Unavailable.", 503); },
    };
    const snapshot: RuntimeDashboardSnapshot = { sessions: [session], observations: [observation], loadedAt: 1 };
    await expect(loadRuntimeDurableSnapshot(() => reader, snapshot, 60 * 60_000)).rejects.toMatchObject({ status: 503 });
  });

  it("fails closed instead of publishing a partial oversized Dashboard", async () => {
    const reader: RuntimeHistoryReader = { retrieveRuntimeHistory: async () => history() };
    const snapshot: RuntimeDashboardSnapshot = { sessions: [session], observations: [observation], loadedAt: 1 };
    await expect(loadRuntimeDurableSnapshot(() => reader, snapshot, 60 * 60_000, undefined, Date.now, 0))
      .rejects.toBeInstanceOf(RuntimeDurableHistoryIncompleteError);
  });
});
