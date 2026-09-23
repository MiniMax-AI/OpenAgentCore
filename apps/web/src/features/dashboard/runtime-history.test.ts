import { describe, expect, it } from "vitest";

import type {
  AgentCore,
  AgentSession,
  RuntimeHistory,
  RuntimeHistoryCapabilities,
  RuntimeObservation,
} from "@agents-core-web/agents-client";

import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";
import {
  loadRuntimeDurableSnapshot,
  runtimeDurableTrendSamples,
  runtimeTrendSourceAfterHistoryUnavailable,
  RuntimeDurableHistoryIncompleteError,
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

const capabilities: RuntimeHistoryCapabilities = {
  object: "agent.runtime_history_capabilities",
  available: true,
  reason: null,
  collection_mode: "periodic",
  sample_interval_seconds: 30,
  retention_seconds: 604_800,
  minimum_step_seconds: 30,
  maximum_range_seconds: 86_400,
  maximum_points: 1_000,
  metrics: ["cpu", "memory", "tokens"],
};

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
  it("returns an explicit History selection to Live when the capability disappears", () => {
    expect(runtimeTrendSourceAfterHistoryUnavailable("durable")).toBe("live");
    expect(runtimeTrendSourceAfterHistoryUnavailable("auto")).toBe("auto");
    expect(runtimeTrendSourceAfterHistoryUnavailable("live")).toBe("live");
  });

  it("projects persisted Runtime and canonical token history", () => {
    const samples = runtimeDurableTrendSamples([session], [history()]);
    expect(samples).toHaveLength(2);
    expect(samples[0]).toMatchObject({
      sampledAt: 130_000,
      memoryUsageBytes: 512,
      memoryLimitBytes: 1_024,
      inputTokensPerMinute: null,
      outputTokensPerMinute: null,
      targets: [{ label: "Durable worker", cpuRatio: .25, uptimeSeconds: null }],
    });
    expect(samples[1]?.targets[0]?.uptimeSeconds).toBeNull();
    expect(samples[1]).toMatchObject({ inputTokensPerMinute: 60, outputTokensPerMinute: 20 });
  });

  it("does not derive compute uptime from retained allocation starts or unavailable observations", () => {
    const source = history();
    source.series[0]!.points[1] = {
      ...source.series[0]!.points[1]!, observed_count: 0, unavailable_count: 1, cpu: null, memory: null,
    };
    const samples = runtimeDurableTrendSamples([session], [source]);
    expect(samples.flatMap((sample) => sample.targets.map((target) => target.uptimeSeconds))).toEqual([null, null]);
  });

  it("keeps aggregate memory absent when any queried target has no memory value", () => {
    const second = { ...session, id: "44444444-4444-4444-8444-444444444444" } as AgentSession;
    const secondHistory = history({
      session_id: second.id,
      coverage: { ...history().coverage, sample_count: 0, first_sample_at: null, last_sample_at: null },
      series: [],
    });
    const samples = runtimeDurableTrendSamples([session, second], [history(), secondHistory]);
    expect(samples.every((sample) => sample.memoryUsageBytes === null && sample.memoryLimitBytes === null)).toBe(true);
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
    expect(samples[0]?.targets[0]?.cpuRatio).toBe(.25);
    expect(samples[10]?.targets[0]?.cpuRatio).toBe(.5);
    expect(samples[10]?.inputTokensPerMinute).toBeNull();
    expect(samples[10]?.outputTokensPerMinute).toBeNull();
    for (const sample of samples.slice(1, -1)) {
      expect(sample).toMatchObject({
        targets: [], memoryUsageBytes: null, memoryLimitBytes: null,
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
        targets: [], memoryUsageBytes: null, memoryLimitBytes: null,
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

  it("loads capability-gated Session histories with a bounded common range", async () => {
    const calls: Array<{ sessionID: string; start: number; end: number; maxPoints?: number }> = [];
    const core: Pick<AgentCore, "getRuntimeHistoryCapabilities" | "retrieveRuntimeHistory"> = {
      getRuntimeHistoryCapabilities: async () => capabilities,
      retrieveRuntimeHistory: async (id, query) => {
        calls.push({ sessionID: id, start: query.start, end: query.end, maxPoints: query.maxPoints });
        return history({ requested_range: { start: query.start, end: query.end } });
      },
    };
    const snapshot: RuntimeDashboardSnapshot = { sessions: [session], observations: [observation], loadedAt: 1 };
    const result = await loadRuntimeDurableSnapshot(core, snapshot, 60 * 60_000, undefined, () => 7_200_000);
    expect(calls).toEqual([{ sessionID, start: 3_600, end: 7_200, maxPoints: 120 }]);
    expect(result).toMatchObject({ rangeStart: 3_600_000, rangeEnd: 7_200_000, targetCount: 1, sampleCount: 2 });
  });

  it("queries each managed Session only once when observations contain duplicates", async () => {
    const queried: string[] = [];
    const core: Pick<AgentCore, "getRuntimeHistoryCapabilities" | "retrieveRuntimeHistory"> = {
      getRuntimeHistoryCapabilities: async () => capabilities,
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
    await loadRuntimeDurableSnapshot(core, snapshot, 60 * 60_000);
    expect(queried).toEqual([sessionID]);
  });

  it("does not issue Session queries when Durable history is unavailable", async () => {
    let queried = false;
    const core: Pick<AgentCore, "getRuntimeHistoryCapabilities" | "retrieveRuntimeHistory"> = {
      getRuntimeHistoryCapabilities: async () => ({
        ...capabilities,
        available: false,
        reason: "not_configured",
        collection_mode: null,
        sample_interval_seconds: null,
        retention_seconds: null,
        minimum_step_seconds: null,
        maximum_range_seconds: null,
        maximum_points: null,
        metrics: [],
      }),
      retrieveRuntimeHistory: async () => { queried = true; return history(); },
    };
    const snapshot: RuntimeDashboardSnapshot = { sessions: [session], observations: [observation], loadedAt: 1 };
    await expect(loadRuntimeDurableSnapshot(core, snapshot, 60 * 60_000)).resolves.toBeNull();
    expect(queried).toBe(false);
  });

  it("fails closed instead of publishing a partial oversized tenant Dashboard", async () => {
    const core: Pick<AgentCore, "getRuntimeHistoryCapabilities" | "retrieveRuntimeHistory"> = {
      getRuntimeHistoryCapabilities: async () => capabilities,
      retrieveRuntimeHistory: async () => history(),
    };
    const snapshot: RuntimeDashboardSnapshot = { sessions: [session], observations: [observation], loadedAt: 1 };
    await expect(loadRuntimeDurableSnapshot(core, snapshot, 60 * 60_000, undefined, Date.now, 0))
      .rejects.toBeInstanceOf(RuntimeDurableHistoryIncompleteError);
  });
});
