import type {
  AgentSession,
  AgentTurn,
  ListPage,
  OpenAIAgentsClient,
  SessionItem,
} from "@agents-core-web/agents-client";

/**
 * Read-only Session history for the administrator. The Web API offers no event
 * stream, so the page polls the history endpoints while the Session has work in
 * flight and stops once it is idle or failed.
 */

export const SESSION_POLL_MS = 5_000;
const SESSION_POLL_MAX_MS = 60_000;
export const HISTORY_LIMIT = 10_000;
const HISTORY_PAGE_SIZE = 100;

/** A Session with queued, running or waiting work can still change. */
export function isSessionActive(status: string | undefined): boolean {
  return status === "in_progress" || status === "requires_action";
}

/**
 * Delay before the next poll, or null to stop. Polling continues only while
 * the last known Session is active; consecutive failures back off (5 s, 10 s,
 * 20 s … up to a minute) so an unreachable Core is not hammered.
 */
export function nextPollDelay(status: string | undefined, consecutiveFailures = 0): number | null {
  if (!isSessionActive(status)) return null;
  return Math.min(SESSION_POLL_MS * 2 ** Math.max(0, consecutiveFailures), SESSION_POLL_MAX_MS);
}

/** Item entries stop changing once they leave `in_progress`. */
export function isItemSettled(item: SessionItem): boolean {
  return item.status !== "in_progress";
}

export function isTurnSettled(turn: AgentTurn): boolean {
  return turn.status === "completed" || turn.status === "failed" || turn.status === "cancelled";
}

/** Length of the leading run of entries that can no longer change. */
export function settledPrefixLength<T>(values: readonly T[], settled: (value: T) => boolean): number {
  const index = values.findIndex((value) => !settled(value));
  return index < 0 ? values.length : index;
}

/**
 * Reads an ascending history list incrementally: entries up to the last
 * settled one are kept, everything after it is read again. With no known
 * entries it reads the whole list. Bounded at `limit` entries.
 */
export async function readHistoryTail<T extends { id: string }>(
  page: (after: string | undefined) => Promise<ListPage<T>>,
  known: readonly T[],
  settled: (value: T) => boolean,
  limit = HISTORY_LIMIT,
): Promise<T[]> {
  const keep = settledPrefixLength(known, settled);
  const values: T[] = known.slice(0, keep);
  let after = values.at(-1)?.id;
  while (values.length < limit) {
    const result = await page(after);
    values.push(...result.data);
    const last = result.last_id ?? result.data.at(-1)?.id;
    if (!result.has_more || !last || last === after) break;
    after = last;
  }
  return values.slice(0, limit);
}

export interface SessionHistory {
  session: AgentSession | null;
  items: SessionItem[];
  turns: AgentTurn[];
  /** Why the Session itself could not be read; earlier data stays shown. */
  sessionError: unknown;
  itemsError: string | null;
  turnsError: string | null;
  loadedAt: number;
}

type HistoryReader = Pick<OpenAIAgentsClient, "retrieveSession" | "listItems" | "listTurns">;

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Reads the Session, its Items and its Turns in parallel. With `incremental`
 * only the part of each list after its settled prefix is read again; each part
 * keeps its previous value when its read fails.
 */
export async function loadSessionHistory(
  client: HistoryReader,
  sessionId: string,
  previous: Pick<SessionHistory, "session" | "items" | "turns"> | null,
  options: { incremental?: boolean; signal?: AbortSignal } = {},
): Promise<SessionHistory> {
  const { incremental = false, signal } = options;
  const [session, items, turns] = await Promise.allSettled([
    client.retrieveSession(sessionId, { signal }),
    readHistoryTail((after) => client.listItems(sessionId, { after, limit: HISTORY_PAGE_SIZE, order: "asc", signal }), incremental ? previous?.items ?? [] : [], isItemSettled),
    readHistoryTail((after) => client.listTurns(sessionId, { after, limit: HISTORY_PAGE_SIZE, order: "asc", signal }), incremental ? previous?.turns ?? [] : [], isTurnSettled),
  ]);
  signal?.throwIfAborted();
  return {
    session: session.status === "fulfilled" ? session.value : previous?.session ?? null,
    items: items.status === "fulfilled" ? items.value : previous?.items ?? [],
    turns: turns.status === "fulfilled" ? turns.value : previous?.turns ?? [],
    sessionError: session.status === "rejected" ? session.reason : null,
    itemsError: items.status === "rejected" ? message(items.reason) : null,
    turnsError: turns.status === "rejected" ? message(turns.reason) : null,
    loadedAt: Date.now(),
  };
}

/** Number of Items that belong to each Turn, and how many reference no known Turn. */
export function itemsPerTurn(turns: readonly AgentTurn[], items: readonly SessionItem[]): { counts: Map<string, number>; unassociated: number } {
  const known = new Set(turns.map((turn) => turn.id));
  const counts = new Map<string, number>();
  let unassociated = 0;
  for (const item of items) {
    if (known.has(item.turn_id)) counts.set(item.turn_id, (counts.get(item.turn_id) ?? 0) + 1);
    else unassociated += 1;
  }
  return { counts, unassociated };
}

/** Wall-clock seconds of a Turn; a running Turn counts up to `now`. Null when Core did not report the bounds. */
export function turnDurationSeconds(turn: AgentTurn, nowSeconds: number): number | null {
  if (turn.started_at === null) return null;
  const end = turn.completed_at ?? (turn.status === "in_progress" || turn.status === "waiting" ? nowSeconds : null);
  return end === null || end < turn.started_at ? null : end - turn.started_at;
}

export interface TranscriptGroup {
  /** The Turn these Items belong to; null for Items that reference no known Turn. */
  turn: AgentTurn | null;
  /** 1-based position of the Turn in the Session. */
  number: number | null;
  items: SessionItem[];
}

/**
 * The conversation grouped by Turn in the order Core ran them, keeping each
 * Turn's Items in their durable order. Turns without Items still appear (a
 * failed or queued Turn has something to say); Items whose Turn is unknown
 * come last.
 */
export function transcriptGroups(turns: readonly AgentTurn[], items: readonly SessionItem[]): TranscriptGroup[] {
  const byTurn = new Map<string, SessionItem[]>(turns.map((turn) => [turn.id, []]));
  const orphans: SessionItem[] = [];
  for (const item of items) (byTurn.get(item.turn_id) ?? orphans).push(item);
  const groups: TranscriptGroup[] = turns.map((turn, index) => ({ turn, number: index + 1, items: byTurn.get(turn.id) ?? [] }));
  if (orphans.length) groups.push({ turn: null, number: null, items: orphans });
  return groups;
}
