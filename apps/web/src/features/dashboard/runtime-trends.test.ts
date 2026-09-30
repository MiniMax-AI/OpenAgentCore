import { describe, expect, it } from "vitest";

import type { AgentSession, RuntimeObservation } from "@oac/agents-client";

import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";
import {
  appendRuntimeTrendSample,
  RUNTIME_TREND_MAX_TARGETS,
  runtimeTrendRange,
  runtimeTrendSample,
  tokenThroughput,
  type RuntimeTrendSample,
} from "./runtime-trends";

function snapshot(at: number, options: {
  input?: number;
  output?: number;
  cpuRatio?: number | null;
  cpuUsageCores?: number | null;
  cpuUsageSecondsTotal?: number;
  cpuCapacity?: number;
  memory?: number;
  startedAt?: number | null;
  allocationId?: string;
  observedAt?: number;
} = {}): RuntimeDashboardSnapshot {
  const sessionId = "11111111-1111-4111-8111-111111111111";
  const observedAt = options.observedAt ?? Math.floor(at / 1_000);
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
    object: "agent.runtime_observation",
    session_id: sessionId,
    environment_id: "22222222-2222-4222-8222-222222222222",
    mode: "openai_hosted",
    status: "observed",
    reason: null,
    provider_type: "docker",
    instance: {
      kind: "managed_allocation",
      allocation_id: options.allocationId ?? "33333333-3333-4333-8333-333333333333",
      device_id: null,
      connection_generation: null,
    },
    lifecycle_state: "active",
    allocation_created_at: observedAt - 180,
    resolved_at: observedAt,
    observed_at: observedAt,
    started_at: options.startedAt === undefined ? observedAt - 120 : options.startedAt,
    cpu: {
      utilization_ratio: Object.hasOwn(options, "cpuRatio") ? options.cpuRatio ?? null : .25,
      usage_cores: Object.hasOwn(options, "cpuUsageCores") ? options.cpuUsageCores ?? null : .5,
      capacity_cores: options.cpuCapacity ?? 2,
      usage_seconds_total: options.cpuUsageSecondsTotal ?? 30,
    },
    memory: { usage_bytes: options.memory ?? 512, limit_bytes: 1_024 },
  } as RuntimeObservation;
  return { sessions: [session], observations: [observation], loadedAt: at };
}

describe("Runtime live-window trends", () => {
  const cpuTarget = (sample: RuntimeTrendSample | undefined) => sample?.targets[0];

  it("projects only honest point-in-time and cumulative Session values", () => {
    const sample = runtimeTrendSample(snapshot(120_000));
    expect(sample).toMatchObject({
      sampledAt: 120_000,
      activeSandboxCount: 1,
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
    })]);
  });

  it("projects a managed Session without an allocation as inactive", () => {
    const pending = snapshot(120_000);
    pending.observations = [{
      ...pending.observations[0]!,
      instance: { kind: "managed_allocation", allocation_id: null, device_id: null, connection_generation: null },
      lifecycle_state: "pending",
      status: "unavailable",
      reason: "allocation_pending",
      allocation_created_at: null,
      observed_at: null,
      started_at: null,
      cpu: null,
      memory: null,
    } as RuntimeObservation];

    expect(runtimeTrendSample(pending)).toMatchObject({ activeSandboxCount: 0, targets: [] });
  });

  it("deduplicates live aggregate count and memory by Runtime allocation identity", () => {
    const duplicate = snapshot(120_000);
    const secondSession = {
      ...duplicate.sessions[0]!,
      id: "44444444-4444-4444-8444-444444444444",
    } as AgentSession;
    const secondObservation = {
      ...duplicate.observations[0]!,
      id: secondSession.id,
      session_id: secondSession.id,
      observed_at: (duplicate.observations[0]!.observed_at ?? 0) + 1,
      memory: { usage_bytes: 128, limit_bytes: 256 },
    } as RuntimeObservation;
    duplicate.sessions.push(secondSession);
    duplicate.observations.push(secondObservation);

    expect(runtimeTrendSample(duplicate)).toMatchObject({
      activeSandboxCount: 1,
      memoryUsageBytes: 128,
      memoryLimitBytes: 256,
    });
  });

  it("keeps lifecycle-active allocation count when resource metrics are unavailable", () => {
    const unavailable = snapshot(180_000);
    unavailable.observations[0] = {
      ...unavailable.observations[0]!,
      status: "unavailable",
      reason: "runtime_not_running",
      observed_at: null,
      started_at: null,
      cpu: null,
      memory: null,
    } as RuntimeObservation;

    expect(runtimeTrendSample(unavailable)).toMatchObject({
      activeSandboxCount: 1,
      memoryUsageBytes: null,
      memoryLimitBytes: null,
      targets: [],
    });
  });

  it("deduplicates refreshes and bounds the rolling window", () => {
    let samples = appendRuntimeTrendSample([], snapshot(60_000), 120_000, 2);
    samples = appendRuntimeTrendSample(samples, snapshot(120_000));
    samples = appendRuntimeTrendSample(samples, snapshot(120_000, { memory: 768 }), 120_000, 2);
    samples = appendRuntimeTrendSample(samples, snapshot(180_000), 120_000, 2);
    expect(samples.map((sample) => sample.sampledAt)).toEqual([120_000, 180_000]);
    expect(samples[0]?.memoryUsageBytes).toBe(768);
  });

  it("selects a live range relative to the newest complete snapshot", () => {
    const samples = [
      runtimeTrendSample(snapshot(0)),
      runtimeTrendSample(snapshot(10 * 60_000)),
      runtimeTrendSample(snapshot(30 * 60_000)),
      runtimeTrendSample(snapshot(60 * 60_000)),
    ];
    expect(runtimeTrendRange(samples, 15 * 60_000).map((sample) => sample.sampledAt))
      .toEqual([60 * 60_000]);
    expect(runtimeTrendRange(samples, 60 * 60_000).map((sample) => sample.sampledAt))
      .toEqual([0, 10 * 60_000, 30 * 60_000, 60 * 60_000]);
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

  it("derives real CPU utilization from cumulative samples within one allocation", () => {
    const cumulative = (at: number, usage: number) => snapshot(at, {
      cpuRatio: null,
      cpuUsageCores: null,
      cpuUsageSecondsTotal: usage,
      cpuCapacity: 2,
      startedAt: 0,
    });
    let samples = appendRuntimeTrendSample([], cumulative(60_000, 10));
    samples = appendRuntimeTrendSample(samples, cumulative(120_000, 70));
    samples = appendRuntimeTrendSample(samples, cumulative(180_000, 130));
    expect(samples.map((sample) => cpuTarget(sample)?.cpuRatio ?? null)).toEqual([null, .5, .5]);
  });

  it("uses allocation identity for Live chart series while ignoring start-time jitter", () => {
    const first = runtimeTrendSample(snapshot(60_000, { cpuRatio: .25, startedAt: 0 }));
    const jittered = runtimeTrendSample(snapshot(120_000, { cpuRatio: .5, startedAt: 1 }));
    const replaced = runtimeTrendSample(snapshot(180_000, {
      cpuRatio: .5,
      startedAt: 1,
      allocationId: "44444444-4444-4444-8444-444444444444",
    }));
    expect(cpuTarget(first)?.seriesId).toBe(cpuTarget(jittered)?.seriesId);
    expect(cpuTarget(replaced)?.seriesId).not.toBe(cpuTarget(first)?.seriesId);
  });

  it("resets cumulative CPU on start changes, allocation changes or counter regressions", () => {
    const base = snapshot(60_000, {
      cpuRatio: null, cpuUsageCores: null, cpuUsageSecondsTotal: 100, startedAt: 0,
    });
    const continued = appendRuntimeTrendSample(appendRuntimeTrendSample([], base), snapshot(120_000, {
      cpuRatio: null, cpuUsageCores: null, cpuUsageSecondsTotal: 160, startedAt: 1,
    }));
    expect(cpuTarget(continued.at(-1))?.cpuRatio ?? null).toBeNull();
    for (const next of [
      snapshot(120_000, { cpuRatio: null, cpuUsageCores: null, cpuUsageSecondsTotal: 160, startedAt: 0, allocationId: "44444444-4444-4444-8444-444444444444" }),
      snapshot(120_000, { cpuRatio: null, cpuUsageCores: null, cpuUsageSecondsTotal: 10, startedAt: 0 }),
    ]) {
      const samples = appendRuntimeTrendSample(appendRuntimeTrendSample([], base), next);
      expect(cpuTarget(samples.at(-1))?.cpuRatio ?? null).toBeNull();
    }
  });

  it("starts a fresh CPU baseline after a same-allocation restart even when its counter is higher", () => {
    const cumulative = (at: number, usage: number, startedAt: number | null) => snapshot(at, {
      cpuRatio: null, cpuUsageCores: null, cpuUsageSecondsTotal: usage, startedAt,
    });
    let samples = appendRuntimeTrendSample([], cumulative(60_000, 1, 0));
    samples = appendRuntimeTrendSample(samples, cumulative(90_000, 20, 65));
    samples = appendRuntimeTrendSample(samples, cumulative(120_000, 50, 65));
    expect(samples.map((sample) => cpuTarget(sample)?.cpuRatio ?? null)).toEqual([null, null, .5]);
    for (const [priorStart, nextStart] of [[null, 0], [0, null], [null, null]] as const) {
      const missingFence = appendRuntimeTrendSample(
        appendRuntimeTrendSample([], cumulative(60_000, 1, priorStart)),
        cumulative(90_000, 20, nextStart),
      );
      expect(cpuTarget(missingFence.at(-1))?.cpuRatio ?? null).toBeNull();
    }
  });

  it("keeps directly reported CPU continuous across start changes but not stale observations", () => {
    const base = snapshot(60_000, { cpuRatio: .25, startedAt: 0 });
    const continued = appendRuntimeTrendSample(appendRuntimeTrendSample([], base), snapshot(120_000, { cpuRatio: .5, startedAt: 1 }));
    expect(cpuTarget(continued.at(-1))?.cpuRatio ?? null).toBe(.5);
    const stale = appendRuntimeTrendSample(
      appendRuntimeTrendSample([], base),
      snapshot(120_000, { cpuRatio: .5, startedAt: 0, observedAt: 60 }),
    );
    expect(cpuTarget(stale.at(-1))?.cpuRatio ?? null).toBeNull();
  });

  it("rejects non-finite CPU ratios produced by finite provider inputs", () => {
    const direct = runtimeTrendSample(snapshot(60_000, {
      cpuRatio: null,
      cpuUsageCores: Number.MAX_VALUE,
      cpuCapacity: Number.MIN_VALUE,
    }));
    expect(cpuTarget(direct)?.cpuRatio ?? null).toBeNull();

    const cumulative = (at: number, usage: number) => snapshot(at, {
      cpuRatio: null,
      cpuUsageCores: null,
      cpuUsageSecondsTotal: usage,
      cpuCapacity: Number.MIN_VALUE,
      startedAt: 0,
    });
    const samples = appendRuntimeTrendSample(
      appendRuntimeTrendSample([], cumulative(60_000, 0)),
      cumulative(120_000, Number.MAX_VALUE),
    );
    expect(cpuTarget(samples.at(-1))?.cpuRatio ?? null).toBeNull();
  });

  it("leaves a gap while a Session's public usage is null and spreads the next report", () => {
    const pending = (at: number) => {
      const value = snapshot(at);
      value.sessions[0] = { ...value.sessions[0]!, status: "in_progress", usage: null };
      return value;
    };
    let samples = appendRuntimeTrendSample([], snapshot(60_000, { input: 100, output: 20 }));
    samples = appendRuntimeTrendSample(samples, pending(120_000));
    // The held total keeps its report time and is never a zero-rate measurement.
    expect(samples.at(-1)?.tokenTotals).toEqual([{
      sessionId: "11111111-1111-4111-8111-111111111111", sampledAt: 60_000, inputTokens: 100, outputTokens: 20, held: true,
    }]);
    samples = appendRuntimeTrendSample(samples, pending(180_000));
    samples = appendRuntimeTrendSample(samples, snapshot(240_000, { input: 400, output: 80 }));
    samples = appendRuntimeTrendSample(samples, snapshot(300_000, { input: 460, output: 90 }));
    expect(tokenThroughput(samples)).toEqual([
      { sampledAt: 60_000, inputPerMinute: null, outputPerMinute: null },
      { sampledAt: 120_000, inputPerMinute: null, outputPerMinute: null },
      { sampledAt: 180_000, inputPerMinute: null, outputPerMinute: null },
      // 300 input and 60 output tokens over the three minutes since the last report.
      { sampledAt: 240_000, inputPerMinute: 100, outputPerMinute: 20 },
      { sampledAt: 300_000, inputPerMinute: 60, outputPerMinute: 10 },
    ]);
  });

  it("keeps Session-set churn as a gap instead of publishing partial throughput", () => {
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
      { sampledAt: 120_000, inputPerMinute: null, outputPerMinute: null },
      { sampledAt: 180_000, inputPerMinute: null, outputPerMinute: null },
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
      instance: { ...many.observations[0]!.instance, allocation_id: `allocation-${index}` },
      cpu: { ...many.observations[0]!.cpu!, utilization_ratio: index / 10 },
      started_at: 60 - index,
    } as RuntimeObservation));
    const later = { ...many, loadedAt: 120_000 };
    let samples = appendRuntimeTrendSample([], many);
    samples = appendRuntimeTrendSample(samples, later);
    expect(samples.every((sample) => sample.targets.length <= RUNTIME_TREND_MAX_TARGETS)).toBe(true);
    expect(samples[0]?.cpuCandidates).toEqual([]);
    expect(samples[1]?.cpuCandidates).toHaveLength(20);
    expect(samples[0]?.tokenTotals).toEqual([]);
    expect(samples[1]?.tokenTotals).toHaveLength(20);
  });
});
