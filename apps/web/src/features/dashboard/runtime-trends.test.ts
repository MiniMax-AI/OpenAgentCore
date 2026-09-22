import { describe, expect, it } from "vitest";

import type { AgentSession, RuntimeObservation } from "@agents-core-web/agents-client";

import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";
import {
  appendRuntimeTrendSample,
  RUNTIME_TREND_MAX_TARGETS,
  runtimeTrendSample,
  tokenThroughput,
} from "./runtime-trends";

function snapshot(at: number, options: {
  input?: number;
  output?: number;
  cpuRatio?: number | null;
  memory?: number;
  startedAt?: number;
} = {}): RuntimeDashboardSnapshot {
  const sessionId = "11111111-1111-4111-8111-111111111111";
  const observedAt = Math.floor(at / 1_000);
  const input = options.input ?? 100;
  const output = options.output ?? 20;
  const session = {
    id: sessionId,
    object: "agent.session",
    metadata: { title: "Runtime worker" },
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
    usage: {
      input_tokens: input,
      output_tokens: output,
      total_tokens: input + output,
      input_tokens_details: { cached_tokens: 0 },
      output_tokens_details: { reasoning_tokens: 0 },
    },
    created_at: observedAt - 180,
    last_active_at: observedAt,
  } as AgentSession;
  const observation = {
    id: sessionId,
    session_id: sessionId,
    status: "observed",
    provider_type: "docker",
    observed_at: observedAt,
    started_at: options.startedAt ?? observedAt - 120,
    cpu: { utilization_ratio: options.cpuRatio ?? .25, usage_cores: .5, capacity_cores: 2, usage_seconds_total: 30 },
    memory: { usage_bytes: options.memory ?? 512, limit_bytes: 1_024 },
  } as RuntimeObservation;
  return { sessions: [session], observations: [observation], loadedAt: at };
}

describe("Runtime live-window trends", () => {
  it("projects only honest point-in-time and cumulative Session values", () => {
    const sample = runtimeTrendSample(snapshot(120_000));
    expect(sample).toMatchObject({
      sampledAt: 120_000,
      tokenTotals: [{
        sessionId: "11111111-1111-4111-8111-111111111111",
        inputTokens: 100,
        outputTokens: 20,
      }],
      memoryUsageBytes: 512,
      memoryLimitBytes: 1_024,
    });
    expect(sample.targets).toEqual([expect.objectContaining({
      label: "Runtime worker",
      cpuRatio: .25,
      uptimeSeconds: 120,
    })]);
  });

  it("deduplicates refreshes and bounds the rolling window", () => {
    let samples = appendRuntimeTrendSample([], snapshot(60_000), 120_000, 2);
    samples = appendRuntimeTrendSample(samples, snapshot(120_000));
    samples = appendRuntimeTrendSample(samples, snapshot(120_000, { memory: 768 }), 120_000, 2);
    samples = appendRuntimeTrendSample(samples, snapshot(180_000), 120_000, 2);
    expect(samples.map((sample) => sample.sampledAt)).toEqual([120_000, 180_000]);
    expect(samples[0]?.memoryUsageBytes).toBe(768);
  });

  it("derives throughput only between monotonic cumulative samples", () => {
    let samples = appendRuntimeTrendSample([], snapshot(60_000, { input: 100, output: 20 }));
    samples = appendRuntimeTrendSample(samples, snapshot(120_000, { input: 220, output: 50 }));
    samples = appendRuntimeTrendSample(samples, snapshot(180_000, { input: 10, output: 5 }));
    expect(tokenThroughput(samples)).toEqual([
      { sampledAt: 60_000, inputPerMinute: null, outputPerMinute: null },
      { sampledAt: 120_000, inputPerMinute: 120, outputPerMinute: 30 },
      { sampledAt: 180_000, inputPerMinute: null, outputPerMinute: null },
    ]);
  });

  it("matches token counters by Session without creating churn spikes", () => {
    const first = snapshot(60_000, { input: 100, output: 20 });
    const second = snapshot(120_000, { input: 220, output: 50 });
    second.sessions.push({
      ...second.sessions[0]!,
      id: "22222222-2222-4222-8222-222222222222",
      usage: { ...second.sessions[0]!.usage!, input_tokens: 5_000, output_tokens: 800, total_tokens: 5_800 },
    });
    const third = snapshot(180_000, { input: 250, output: 10 });
    let samples = appendRuntimeTrendSample([], first);
    samples = appendRuntimeTrendSample(samples, second);
    samples = appendRuntimeTrendSample(samples, third);
    expect(tokenThroughput(samples)).toEqual([
      { sampledAt: 60_000, inputPerMinute: null, outputPerMinute: null },
      { sampledAt: 120_000, inputPerMinute: 120, outputPerMinute: 30 },
      { sampledAt: 180_000, inputPerMinute: 30, outputPerMinute: null },
    ]);
  });

  it("bounds retained target detail and keeps raw token counters only for the latest sample", () => {
    const many = snapshot(60_000);
    many.sessions = Array.from({ length: 20 }, (_, index) => ({
      ...many.sessions[0]!,
      id: `session-${index}`,
      metadata: { title: `Runtime ${index}` },
    }));
    many.observations = many.sessions.map((session, index) => ({
      ...many.observations[0]!,
      id: session.id,
      session_id: session.id,
      cpu: { ...many.observations[0]!.cpu!, utilization_ratio: index / 10 },
      started_at: 60 - index,
    } as RuntimeObservation));
    const later = { ...many, loadedAt: 120_000 };
    let samples = appendRuntimeTrendSample([], many);
    samples = appendRuntimeTrendSample(samples, later);
    expect(samples.every((sample) => sample.targets.length <= RUNTIME_TREND_MAX_TARGETS)).toBe(true);
    expect(samples[0]?.tokenTotals).toEqual([]);
    expect(samples[1]?.tokenTotals).toHaveLength(20);
  });
});
