import type { AgentSession, AgentTurn, SessionItem } from "@agents-core-web/agents-client";

/**
 * Agent metrics derived in the browser from each project's Session, Turn and
 * Item lists (read through the Web API). A Turn is one Agent run request.
 * Core has no Turn aggregate endpoint yet, so every total states which
 * Sessions it covers.
 */

export type AgentMetricsRange = "1h" | "6h" | "24h" | "7d";

export const AGENT_METRICS_RANGES: Readonly<Record<AgentMetricsRange, { seconds: number; bucketSeconds: number }>> = {
  "1h": { seconds: 3_600, bucketSeconds: 120 },
  "6h": { seconds: 21_600, bucketSeconds: 600 },
  "24h": { seconds: 86_400, bucketSeconds: 1_800 },
  "7d": { seconds: 604_800, bucketSeconds: 21_600 },
};

export interface MetricsWindow {
  range: AgentMetricsRange;
  /** Inclusive start, epoch seconds (the first bucket start). */
  start: number;
  end: number;
  bucketSeconds: number;
  buckets: number[];
}

export function metricsWindow(range: AgentMetricsRange, nowSeconds: number): MetricsWindow {
  const { seconds, bucketSeconds } = AGENT_METRICS_RANGES[range];
  const count = Math.round(seconds / bucketSeconds);
  const lastBucket = Math.floor(nowSeconds / bucketSeconds) * bucketSeconds;
  const first = lastBucket - (count - 1) * bucketSeconds;
  return {
    range,
    start: first,
    end: nowSeconds,
    bucketSeconds,
    buckets: Array.from({ length: count }, (_, index) => first + index * bucketSeconds),
  };
}

/** Core timestamps may run slightly ahead of the browser clock; count those in the newest bucket. */
export const CLOCK_SKEW_TOLERANCE_SECONDS = 900;

export function bucketIndex(window: MetricsWindow, seconds: number): number | null {
  if (seconds < window.start || seconds > window.end + CLOCK_SKEW_TOLERANCE_SECONDS) return null;
  const index = Math.floor((seconds - window.start) / window.bucketSeconds);
  return Math.min(Math.max(index, 0), window.buckets.length - 1);
}

export interface SessionActivity {
  /** The project that owns the Session, when several projects are aggregated. */
  projectId?: string;
  session: AgentSession;
  /** Turns created inside the window. */
  turns: AgentTurn[];
  /** Items of those Turns, or null when Items were not read. */
  items: SessionItem[] | null;
  /** Older Turns or Items exist beyond the bounded read. */
  truncated: boolean;
}

export interface MetricsCoverage {
  /** Sessions active in the window according to the Session list. */
  candidateSessions: number;
  /** Sessions whose Turns were read successfully. */
  loadedSessions: number;
  /** Sessions skipped because of the per-load Session cap. */
  skippedSessions: number;
  /** Sessions whose reads failed. */
  failedSessions: number;
  /** Sessions whose Turn or Item history exceeded the page cap. */
  truncatedSessions: number;
  /** Sessions whose Turns loaded but whose Item read failed; tool figures exclude them. */
  itemFailedSessions: number;
}

export type ToolKind = "function" | "mcp" | "command" | "web_search" | "subagent";

export interface ToolIdentity {
  id: string;
  kind: ToolKind;
  name: string | null;
}

export interface Breakdown {
  id: string;
  label: string;
  requests: number;
  failed: number;
  /** Turns that reported token usage; tokens are unknown when zero. */
  reportedTurns: number;
  tokens: number;
  inputTokens: number;
  outputTokens: number;
}

export interface AgentBreakdown extends Breakdown {
  /** The Agent's own ID (or the inline key); `id` also carries the project. */
  agentId: string;
  projectId: string | null;
  sessions: number;
  /** Completed, failed or cancelled Turns. */
  finished: number;
  averageLatencySeconds: number | null;
}

export interface ToolBreakdown {
  id: string;
  kind: ToolKind;
  name: string | null;
  calls: number;
  failed: number;
}

export interface NamedSeries {
  id: string;
  values: number[];
}

export interface AgentMetrics {
  window: MetricsWindow;
  coverage: MetricsCoverage;
  totals: {
    requests: number;
    completed: number;
    failed: number;
    cancelled: number;
    unfinished: number;
    /** Failed share of finished Turns; null when none finished. */
    errorRate: number | null;
    averageLatencySeconds: number | null;
    p95LatencySeconds: number | null;
    averageQueueSeconds: number | null;
    tokens: { input: number; output: number; cached: number; reasoning: number; total: number; reportedTurns: number };
    toolCalls: number | null;
    toolFailures: number | null;
  };
  series: {
    requests: number[];
    failed: number[];
    averageLatency: Array<number | null>;
    p95Latency: Array<number | null>;
    tokensByModel: NamedSeries[];
    requestsByModel: NamedSeries[];
    callsByTool: NamedSeries[];
  };
  byModel: Breakdown[];
  byAgent: AgentBreakdown[];
  byTool: ToolBreakdown[] | null;
}

export const SERIES_LIMIT = 5;
export const OTHER_SERIES_ID = "__other__";

export function toolIdentity(item: SessionItem): ToolIdentity | null {
  switch (item.type) {
    case "function_call":
      return item.name ? { id: `function:${item.name}`, kind: "function", name: item.name } : null;
    case "mcp_call": {
      const name = [item.server_label, item.name].filter(Boolean).join(" · ") || null;
      return { id: `mcp:${name ?? "unknown"}`, kind: "mcp", name };
    }
    case "command_execution":
      return { id: "command", kind: "command", name: null };
    case "web_search_call":
      return { id: "web_search", kind: "web_search", name: null };
    case "create_subagent_call":
    case "send_subagent_input_call":
    case "resume_subagent_call":
    case "wait_for_subagents_call":
    case "interrupt_subagent_call":
    case "close_subagent_call":
      return { id: "subagent", kind: "subagent", name: null };
    default:
      return null;
  }
}

function toolFailed(item: SessionItem): boolean {
  if (item.status === "failed") return true;
  return item.type === "command_execution" && typeof item.exit_code === "number" && item.exit_code !== 0;
}

export function percentile(sorted: readonly number[], fraction: number): number | null {
  if (!sorted.length) return null;
  const rank = Math.min(sorted.length - 1, Math.max(0, Math.ceil(fraction * sorted.length) - 1));
  return sorted[rank] ?? null;
}

function average(values: readonly number[]): number | null {
  return values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : null;
}

function modelOf(session: AgentSession): string {
  const model = session.agent?.model;
  return typeof model === "string" && model.trim() ? model : "unknown";
}

export const INLINE_AGENT_ID = "inline";

function agentOf(session: AgentSession): { id: string; label: string } {
  const name = typeof session.agent?.name === "string" && session.agent.name.trim() ? session.agent.name : null;
  if (typeof session.agent?.id === "string" && session.agent.id) return { id: session.agent.id, label: name ?? session.agent.id };
  // Inline Agents have no ID; keep differently named ones apart.
  return { id: name ? `${INLINE_AGENT_ID}:${name}` : INLINE_AGENT_ID, label: name ?? INLINE_AGENT_ID };
}

function emptyBreakdown(id: string, label: string): Breakdown {
  return { id, label, requests: 0, failed: 0, reportedTurns: 0, tokens: 0, inputTokens: 0, outputTokens: 0 };
}

/** Fold the long tail into one "other" series so the chart never needs a generated hue. */
export function topSeries(totals: ReadonlyMap<string, number>, perBucket: ReadonlyMap<string, number[]>, length: number): NamedSeries[] {
  const ranked = [...totals.entries()].filter(([, total]) => total > 0).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  const kept = ranked.slice(0, SERIES_LIMIT);
  const rest = ranked.slice(SERIES_LIMIT);
  const series = kept.map(([id]) => ({ id, values: [...(perBucket.get(id) ?? Array<number>(length).fill(0))] }));
  if (rest.length) {
    const values = Array<number>(length).fill(0);
    for (const [id] of rest) (perBucket.get(id) ?? []).forEach((value, index) => { values[index] = (values[index] ?? 0) + value; });
    series.push({ id: OTHER_SERIES_ID, values });
  }
  return series;
}

/** Series for a fixed, already ranked set of entities, plus "other" for the rest. */
export function seriesFor(kept: readonly string[], perBucket: ReadonlyMap<string, number[]>, length: number): NamedSeries[] {
  const keptSet = new Set(kept);
  const series = kept.map((id) => ({ id, values: [...(perBucket.get(id) ?? Array<number>(length).fill(0))] }));
  const values = Array<number>(length).fill(0);
  let hasOther = false;
  for (const [id, bucketValues] of perBucket) {
    if (keptSet.has(id)) continue;
    bucketValues.forEach((value, index) => {
      values[index] = (values[index] ?? 0) + value;
      if (value) hasOther = true;
    });
  }
  if (hasOther) series.push({ id: OTHER_SERIES_ID, values });
  return series;
}

function addTo(map: Map<string, number[]>, id: string, index: number, value: number, length: number) {
  let values = map.get(id);
  if (!values) {
    values = Array<number>(length).fill(0);
    map.set(id, values);
  }
  values[index] = (values[index] ?? 0) + value;
}

export function aggregateAgentMetrics(
  window: MetricsWindow,
  activities: readonly SessionActivity[],
  coverage: MetricsCoverage,
): AgentMetrics {
  const length = window.buckets.length;
  const requests = Array<number>(length).fill(0);
  const failedSeries = Array<number>(length).fill(0);
  const latencyByBucket: number[][] = Array.from({ length }, () => []);
  const latencies: number[] = [];
  const queues: number[] = [];
  const tokenTotals = { input: 0, output: 0, cached: 0, reasoning: 0, total: 0, reportedTurns: 0 };
  let completed = 0;
  let failed = 0;
  let cancelled = 0;
  let unfinished = 0;

  const models = new Map<string, Breakdown>();
  const agents = new Map<string, AgentBreakdown & { latencies: number[]; sessionIds: Set<string> }>();
  const tokensByModel = new Map<string, number[]>();
  const requestsByModel = new Map<string, number[]>();
  const modelTokenTotals = new Map<string, number>();

  const tools = new Map<string, ToolBreakdown>();
  const callsByTool = new Map<string, number[]>();
  const toolTotals = new Map<string, number>();
  let itemsRead = false;

  for (const activity of activities) {
    const model = modelOf(activity.session);
    const agent = agentOf(activity.session);
    const projectId = activity.projectId ?? null;
    // Agent IDs belong to one project; keep the same Agent name in two projects apart.
    const agentKey = projectId ? `${projectId}/${agent.id}` : agent.id;
    const modelEntry = models.get(model) ?? emptyBreakdown(model, model);
    models.set(model, modelEntry);
    const agentEntry = agents.get(agentKey) ?? { ...emptyBreakdown(agentKey, agent.label), agentId: agent.id, projectId, sessions: 0, finished: 0, averageLatencySeconds: null, latencies: [], sessionIds: new Set<string>() };
    agents.set(agentKey, agentEntry);
    const turnBucket = new Map<string, number>();

    for (const turn of activity.turns) {
      const index = bucketIndex(window, turn.created_at);
      if (index === null) continue;
      turnBucket.set(turn.id, index);
      requests[index] = (requests[index] ?? 0) + 1;
      modelEntry.requests += 1;
      agentEntry.requests += 1;
      agentEntry.sessionIds.add(activity.session.id);
      addTo(requestsByModel, model, index, 1, length);

      if (turn.status === "completed") completed += 1;
      else if (turn.status === "failed") {
        failed += 1;
        failedSeries[index] = (failedSeries[index] ?? 0) + 1;
        modelEntry.failed += 1;
        agentEntry.failed += 1;
      } else if (turn.status === "cancelled") cancelled += 1;
      else unfinished += 1;

      const finished = turn.status === "completed" || turn.status === "failed" || turn.status === "cancelled";
      if (finished) agentEntry.finished += 1;
      if (finished && turn.started_at !== null && turn.completed_at !== null && turn.completed_at >= turn.started_at) {
        const duration = turn.completed_at - turn.started_at;
        latencies.push(duration);
        latencyByBucket[index]?.push(duration);
        agentEntry.latencies.push(duration);
      }
      if (turn.started_at !== null && turn.started_at >= turn.created_at) queues.push(turn.started_at - turn.created_at);

      if (turn.usage) {
        const total = turn.usage.total_tokens;
        tokenTotals.input += turn.usage.input_tokens;
        tokenTotals.output += turn.usage.output_tokens;
        tokenTotals.cached += turn.usage.input_tokens_details?.cached_tokens ?? 0;
        tokenTotals.reasoning += turn.usage.output_tokens_details?.reasoning_tokens ?? 0;
        tokenTotals.total += total;
        tokenTotals.reportedTurns += 1;
        modelEntry.reportedTurns += 1;
        agentEntry.reportedTurns += 1;
        modelEntry.tokens += total;
        modelEntry.inputTokens += turn.usage.input_tokens;
        modelEntry.outputTokens += turn.usage.output_tokens;
        agentEntry.tokens += total;
        agentEntry.inputTokens += turn.usage.input_tokens;
        agentEntry.outputTokens += turn.usage.output_tokens;
        addTo(tokensByModel, model, index, total, length);
        modelTokenTotals.set(model, (modelTokenTotals.get(model) ?? 0) + total);
      }
    }

    if (activity.items) {
      itemsRead = true;
      for (const item of activity.items) {
        const index = turnBucket.get(item.turn_id);
        if (index === undefined) continue;
        const identity = toolIdentity(item);
        if (!identity) continue;
        const entry = tools.get(identity.id) ?? { id: identity.id, kind: identity.kind, name: identity.name, calls: 0, failed: 0 };
        tools.set(identity.id, entry);
        entry.calls += 1;
        if (toolFailed(item)) entry.failed += 1;
        addTo(callsByTool, identity.id, index, 1, length);
        toolTotals.set(identity.id, (toolTotals.get(identity.id) ?? 0) + 1);
      }
    }
  }

  const sortedLatencies = [...latencies].sort((a, b) => a - b);
  // One model ranking drives both model charts, so a model keeps one colour.
  const rankedModels = [...models.values()]
    .filter((entry) => entry.requests > 0)
    .sort((a, b) => b.tokens - a.tokens || b.requests - a.requests || a.id.localeCompare(b.id));
  const keptModels = rankedModels.slice(0, SERIES_LIMIT).map((entry) => entry.id);
  const finishedCount = completed + failed + cancelled;
  const byTool = itemsRead ? [...tools.values()].sort((a, b) => b.calls - a.calls || a.id.localeCompare(b.id)) : null;

  return {
    window,
    coverage,
    totals: {
      requests: requests.reduce((sum, value) => sum + value, 0),
      completed,
      failed,
      cancelled,
      unfinished,
      errorRate: finishedCount ? failed / finishedCount : null,
      averageLatencySeconds: average(latencies),
      p95LatencySeconds: percentile(sortedLatencies, 0.95),
      averageQueueSeconds: average(queues),
      tokens: tokenTotals,
      toolCalls: byTool ? byTool.reduce((sum, tool) => sum + tool.calls, 0) : null,
      toolFailures: byTool ? byTool.reduce((sum, tool) => sum + tool.failed, 0) : null,
    },
    series: {
      requests,
      failed: failedSeries,
      averageLatency: latencyByBucket.map((values) => average(values)),
      p95Latency: latencyByBucket.map((values) => percentile([...values].sort((a, b) => a - b), 0.95)),
      tokensByModel: seriesFor(keptModels.filter((id) => (modelTokenTotals.get(id) ?? 0) > 0), tokensByModel, length),
      requestsByModel: seriesFor(keptModels, requestsByModel, length),
      callsByTool: topSeries(toolTotals, callsByTool, length),
    },
    byModel: rankedModels,
    byAgent: [...agents.values()]
      .filter((entry) => entry.requests > 0)
      .map(({ latencies: agentLatencies, sessionIds, ...entry }) => ({ ...entry, sessions: sessionIds.size, averageLatencySeconds: average(agentLatencies) }))
      .sort((a, b) => b.requests - a.requests || b.tokens - a.tokens),
    byTool,
  };
}

export type AgentMetricsOutcome = "failed" | "empty" | "ready";

/**
 * What the page may claim. "No runs" is claimed only when every read
 * succeeded: with no requests counted and any Session list or Turn read
 * failed, it would be a false statement during an incident, so the load is
 * reported as failed.
 */
export function agentMetricsOutcome(metrics: AgentMetrics, failedLists = 0): AgentMetricsOutcome {
  if (metrics.totals.requests) return "ready";
  return metrics.coverage.failedSessions > 0 || failedLists > 0 ? "failed" : "empty";
}
