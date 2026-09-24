import type { AgentSession, ListPage, PageOptions, SessionStatus } from "@agents-core-web/agents-client";

import { canonicalUsage } from "../dashboard/dashboard-model";

/**
 * Per-Agent usage statistics built from the public Session list.
 *
 * Usage on a Session is Core's cumulative total for that Session, so it is
 * attributed whole to the range the Session was created in. A Session whose
 * usage is missing or invalid counts toward the coverage denominator but never
 * toward token sums; missing is not zero.
 */

export type AgentUsageRange = "all" | "7d" | "30d";
export const AGENT_USAGE_RANGES: readonly AgentUsageRange[] = ["all", "7d", "30d"];

/** Core accepts at most 100 Sessions per list page. */
export const USAGE_PAGE_LIMIT = 100;
/** Beyond this many Sessions the console suggests a shorter range. */
export const LARGE_SESSION_COUNT = 10_000;

const DAY_SECONDS = 86_400;
const RANGE_DAYS: Record<Exclude<AgentUsageRange, "all">, number> = { "7d": 7, "30d": 30 };

export function usageRangeStart(range: AgentUsageRange, nowSeconds: number): number | null {
  return range === "all" ? null : nowSeconds - RANGE_DAYS[range] * DAY_SECONDS;
}

export type UsageSessionStatus = SessionStatus | "unknown";

export interface UsageTokens {
  input: number;
  output: number;
  total: number;
  cached: number;
  reasoning: number;
}

/** The only Session fields the statistics keep in memory. */
export interface UsageSessionRecord {
  id: string;
  /** Raw `agent.id`; null only if Core returned none. */
  agentId: string | null;
  status: UsageSessionStatus;
  createdAt: number | null;
  lastActiveAt: number | null;
  usage: UsageTokens | null;
}

const sessionStatuses = new Set<string>(["idle", "in_progress", "requires_action", "failed"]);

function nonEmptyString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function timestamp(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
}

export function usageRecord(session: AgentSession): UsageSessionRecord {
  const agent: unknown = session.agent;
  const agentId = agent !== null && typeof agent === "object" ? nonEmptyString((agent as { id?: unknown }).id) : null;
  const usage = canonicalUsage(session.usage);
  return {
    id: session.id,
    agentId,
    status: sessionStatuses.has(session.status) ? session.status : "unknown",
    createdAt: timestamp(session.created_at),
    lastActiveAt: timestamp(session.last_active_at),
    usage: usage
      ? {
          input: usage.input_tokens,
          output: usage.output_tokens,
          total: usage.total_tokens,
          cached: usage.input_tokens_details.cached_tokens,
          reasoning: usage.output_tokens_details.reasoning_tokens,
        }
      : null,
  };
}

/** A token sum; null when no Session reported usage or the sum left the safe integer range. */
export type UsageTokenTotals = { [Kind in keyof UsageTokens]: number | null };

export interface AgentUsageTotals {
  sessions: number;
  statuses: Record<UsageSessionStatus, number>;
  /** Sessions with valid reported usage: the coverage numerator. */
  reported: number;
  tokens: UsageTokenTotals;
  lastActiveAt: number | null;
}

export interface OtherAgentUsage {
  agentId: string | null;
  totals: AgentUsageTotals;
}

export interface AgentUsageReport {
  rangeStart: number | null;
  /** Every loaded saved Agent, including those without Sessions in the range. */
  byAgent: ReadonlyMap<string, AgentUsageTotals>;
  /** Sessions whose Agent is not a loaded saved Agent, grouped by raw Agent ID. */
  other: OtherAgentUsage[];
  otherTotals: AgentUsageTotals;
  total: AgentUsageTotals;
}

const TOKEN_KINDS = ["input", "output", "total", "cached", "reasoning"] as const;

interface MutableTotals {
  sessions: number;
  statuses: Record<UsageSessionStatus, number>;
  reported: number;
  sums: UsageTokens;
  safe: Record<keyof UsageTokens, boolean>;
  lastActiveAt: number | null;
}

function emptyTotals(): MutableTotals {
  return {
    sessions: 0,
    statuses: { idle: 0, in_progress: 0, requires_action: 0, failed: 0, unknown: 0 },
    reported: 0,
    sums: { input: 0, output: 0, total: 0, cached: 0, reasoning: 0 },
    safe: { input: true, output: true, total: true, cached: true, reasoning: true },
    lastActiveAt: null,
  };
}

function addRecord(totals: MutableTotals, record: UsageSessionRecord): void {
  totals.sessions += 1;
  totals.statuses[record.status] += 1;
  if (record.lastActiveAt !== null && (totals.lastActiveAt === null || record.lastActiveAt > totals.lastActiveAt)) {
    totals.lastActiveAt = record.lastActiveAt;
  }
  if (!record.usage) return;
  totals.reported += 1;
  for (const kind of TOKEN_KINDS) {
    const next = totals.sums[kind] + record.usage[kind];
    if (Number.isSafeInteger(next)) totals.sums[kind] = next;
    else totals.safe[kind] = false;
  }
}

function freeze(totals: MutableTotals): AgentUsageTotals {
  const tokens = {} as UsageTokenTotals;
  for (const kind of TOKEN_KINDS) {
    tokens[kind] = totals.reported > 0 && totals.safe[kind] ? totals.sums[kind] : null;
  }
  return {
    sessions: totals.sessions,
    statuses: { ...totals.statuses },
    reported: totals.reported,
    tokens,
    lastActiveAt: totals.lastActiveAt,
  };
}

export function inUsageRange(record: UsageSessionRecord, rangeStart: number | null): boolean {
  // A Session without a valid creation time cannot be placed in a bounded range.
  return rangeStart === null || (record.createdAt !== null && record.createdAt >= rangeStart);
}

export function aggregateAgentUsage(
  records: readonly UsageSessionRecord[],
  savedAgentIds: Iterable<string>,
  rangeStart: number | null,
): AgentUsageReport {
  const saved = new Map<string, MutableTotals>();
  for (const id of savedAgentIds) saved.set(id, emptyTotals());
  const other = new Map<string | null, MutableTotals>();
  const otherTotals = emptyTotals();
  const total = emptyTotals();

  for (const record of records) {
    if (!inUsageRange(record, rangeStart)) continue;
    addRecord(total, record);
    const savedTotals = record.agentId === null ? undefined : saved.get(record.agentId);
    if (savedTotals) {
      addRecord(savedTotals, record);
      continue;
    }
    let group = other.get(record.agentId);
    if (!group) {
      group = emptyTotals();
      other.set(record.agentId, group);
    }
    addRecord(group, record);
    addRecord(otherTotals, record);
  }

  return {
    rangeStart,
    byAgent: new Map([...saved].map(([id, totals]) => [id, freeze(totals)])),
    other: [...other]
      .map(([agentId, totals]) => ({ agentId, totals: freeze(totals) }))
      .sort((left, right) => right.totals.sessions - left.totals.sessions
        || (right.totals.lastActiveAt ?? -1) - (left.totals.lastActiveAt ?? -1)
        || (left.agentId ?? "").localeCompare(right.agentId ?? "")),
    otherTotals: freeze(otherTotals),
    total: freeze(total),
  };
}

/** Reported Sessions over all Sessions; null when there are no Sessions. */
export function usageCoverage(totals: Pick<AgentUsageTotals, "sessions" | "reported">): number | null {
  return totals.sessions > 0 ? totals.reported / totals.sessions : null;
}

// Loading ------------------------------------------------------------------

export interface AgentUsageSource {
  listSessions(options?: PageOptions & { agentId?: string }): Promise<ListPage<AgentSession>>;
}

/**
 * Everything read so far, newest first. The cursor is independent of the
 * selected range: a range is only a stop condition and a filter, so a narrower
 * range reuses what was read and a wider one continues from `after`.
 */
export interface UsageLoadCursor {
  records: UsageSessionRecord[];
  ids: Set<string>;
  after: string | undefined;
  pages: number;
  /** Core reported no further pages. */
  exhausted: boolean;
  /** Oldest valid creation time read; once it precedes a range start, that range is complete. */
  oldestCreatedAt: number | null;
}

export interface UsageLoadProgress {
  pages: number;
  sessions: number;
}

export function createUsageLoadCursor(): UsageLoadCursor {
  return { records: [], ids: new Set(), after: undefined, pages: 0, exhausted: false, oldestCreatedAt: null };
}

/** Whether the cursor already holds every Session created at or after the range start. */
export function usageCursorCovers(cursor: UsageLoadCursor, rangeStart: number | null): boolean {
  if (cursor.exhausted) return true;
  return rangeStart !== null && cursor.oldestCreatedAt !== null && cursor.oldestCreatedAt < rangeStart;
}

/**
 * Reads newest-first Session pages into the cursor until the range is covered.
 * Each page is committed only after it is fully validated, so after a failure
 * or cancellation the cursor still ends on a page boundary and a later call
 * resumes without re-reading earlier pages.
 */
export async function continueUsageLoad(
  source: AgentUsageSource,
  cursor: UsageLoadCursor,
  rangeStart: number | null,
  { signal, onProgress }: { signal?: AbortSignal; onProgress?: (progress: UsageLoadProgress) => void } = {},
): Promise<UsageLoadCursor> {
  while (!usageCursorCovers(cursor, rangeStart)) {
    signal?.throwIfAborted();
    const page = await source.listSessions({ after: cursor.after, limit: USAGE_PAGE_LIMIT, order: "desc", signal });
    signal?.throwIfAborted();
    if (!page || !Array.isArray(page.data) || typeof page.has_more !== "boolean") {
      throw new Error("Agent Core returned an invalid Session page.");
    }
    const pageIds = new Set<string>();
    const records: UsageSessionRecord[] = [];
    let oldest = cursor.oldestCreatedAt;
    for (const session of page.data) {
      const id = session && typeof session.id === "string" && session.id.length > 0 ? session.id : null;
      if (id === null || cursor.ids.has(id) || pageIds.has(id)) {
        throw new Error("Agent Core returned duplicate or invalid Session identities.");
      }
      pageIds.add(id);
      const record = usageRecord(session);
      if (record.createdAt !== null && (oldest === null || record.createdAt < oldest)) oldest = record.createdAt;
      records.push(record);
    }
    const nextAfter = page.has_more ? page.last_id ?? page.data.at(-1)?.id : undefined;
    if (page.has_more && (!nextAfter || nextAfter === cursor.after)) {
      throw new Error("Agent Core returned an invalid Session pagination cursor.");
    }

    cursor.records.push(...records);
    for (const id of pageIds) cursor.ids.add(id);
    cursor.pages += 1;
    cursor.oldestCreatedAt = oldest;
    cursor.after = nextAfter;
    cursor.exhausted = !page.has_more;
    onProgress?.({ pages: cursor.pages, sessions: cursor.records.length });
  }
  return cursor;
}
