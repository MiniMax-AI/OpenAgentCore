import type {
  AgentCore,
  AgentTurn,
  SessionEvent,
  SessionItem,
} from "@agents-core-web/agents-client";

const turnStatuses = new Set<AgentTurn["status"]>([
  "queued",
  "in_progress",
  "waiting",
  "completed",
  "failed",
  "cancelled",
]);

const lifecycleEventStatus = new Map<string, AgentTurn["status"]>([
  ["agent.session.turn.created", "queued"],
  ["agent.session.turn.in_progress", "in_progress"],
  ["agent.session.turn.waiting", "waiting"],
  ["agent.session.turn.completed", "completed"],
  ["agent.session.turn.failed", "failed"],
  ["agent.session.turn.cancelled", "cancelled"],
]);

export interface TurnReadScope {
  coreGeneration: number;
  request: number;
  sessionId: string;
}

export interface CurrentTurnReadScope extends TurnReadScope {
  selectedSessionId: string | null;
}

export function turnReadIsCurrent(read: TurnReadScope, current: CurrentTurnReadScope): boolean {
  return read.coreGeneration === current.coreGeneration &&
    read.request === current.request &&
    read.sessionId === current.sessionId &&
    current.selectedSessionId === read.sessionId;
}

function isTurnForSession(value: unknown, sessionId: string): value is AgentTurn {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const turn = value as Record<string, unknown>;
  return typeof turn.id === "string" && Boolean(turn.id) &&
    turn.session_id === sessionId &&
    turn.object === "agent.session.turn" &&
    typeof turn.status === "string" && turnStatuses.has(turn.status as AgentTurn["status"]);
}

/**
 * Session timelines show root work. Subagent Turns belong to the Subagent routes;
 * Core no longer returns or streams them, or their Items, for a Session, but
 * earlier releases did.
 */
function isRootTurn(turn: AgentTurn): boolean {
  return turn.subagent_id === undefined || turn.subagent_id === null;
}

/**
 * Reads the complete durable root Turn collection in server creation order.
 * Subagent Turn IDs listed by an earlier Core are added to childTurnIds.
 */
export async function listAllTurns(
  core: AgentCore,
  sessionId: string,
  signal?: AbortSignal,
  childTurnIds?: Set<string>,
): Promise<AgentTurn[]> {
  const turns: AgentTurn[] = [];
  const indexes = new Map<string, number>();
  const cursors = new Set<string>();
  let after: string | undefined;

  while (true) {
    const page = await core.listTurns(sessionId, { after, limit: 100, order: "asc", signal });
    if (!page || !Array.isArray(page.data) || typeof page.has_more !== "boolean") {
      throw new Error("The Agent core returned an invalid Turns page.");
    }
    for (const value of page.data) {
      if (!isTurnForSession(value, sessionId)) {
        throw new Error("The Agent core returned a Turn outside the selected Session.");
      }
      if (!isRootTurn(value)) {
        childTurnIds?.add(value.id);
        continue;
      }
      const index = indexes.get(value.id);
      if (index === undefined) {
        indexes.set(value.id, turns.length);
        turns.push(value);
      } else {
        turns[index] = preferTurn(turns[index] as AgentTurn, value);
      }
    }
    if (!page.has_more) return turns;

    const nextAfter = page.last_id ?? page.data[page.data.length - 1]?.id;
    if (!nextAfter || cursors.has(nextAfter)) {
      throw new Error("The Agent core returned an invalid Turns pagination cursor.");
    }
    cursors.add(nextAfter);
    after = nextAfter;
  }
}

function isTerminal(status: AgentTurn["status"]): boolean {
  return status === "completed" || status === "failed" || status === "cancelled";
}

function preferTurn(current: AgentTurn, incoming: AgentTurn): AgentTurn {
  if (isTerminal(current.status) && !isTerminal(incoming.status)) return current;
  return incoming;
}

export function upsertTurn(current: AgentTurn[], incoming: AgentTurn): AgentTurn[] {
  const index = current.findIndex((turn) => turn.id === incoming.id);
  if (index < 0) return [...current, incoming];
  const preferred = preferTurn(current[index] as AgentTurn, incoming);
  if (preferred === current[index]) return current;
  return current.map((turn, candidate) => candidate === index ? preferred : turn);
}

/** Durable order stays authoritative while a newer live snapshot wins safely. */
export function mergeDurableAndLiveTurns(durable: AgentTurn[], live: AgentTurn[]): AgentTurn[] {
  return live.reduce(upsertTurn, durable);
}

/** Accepts only a scoped, known root Turn snapshot carried by a Turn event. */
export function matchingTurnSnapshot(event: SessionEvent, sessionId: string): AgentTurn | null {
  const type = typeof event.type === "string" ? event.type : "";
  const expectedStatus = lifecycleEventStatus.get(type);
  if (!expectedStatus || !isTurnForSession(event.turn, sessionId) || event.turn.status !== expectedStatus) return null;
  if (!isRootTurn(event.turn)) return null;
  if (event.session_id && event.session_id !== sessionId) return null;
  if (event.turn_id && event.turn_id !== event.turn.id) return null;
  return event.turn;
}

/**
 * Returns the Turn ID of a scoped Subagent Turn lifecycle event from an earlier
 * Core. Its first snapshot can already be terminal, so the status is not checked.
 */
export function childTurnSnapshotId(event: SessionEvent, sessionId: string): string | null {
  const type = typeof event.type === "string" ? event.type : "";
  if (!lifecycleEventStatus.has(type) || !isTurnForSession(event.turn, sessionId) || isRootTurn(event.turn)) return null;
  if (event.session_id && event.session_id !== sessionId) return null;
  if (event.turn_id && event.turn_id !== event.turn.id) return null;
  return event.turn.id;
}

/** Records Subagent Turn IDs per Session, keeping the same map when nothing is new. */
export function addChildTurnIds(
  current: ReadonlyMap<string, ReadonlySet<string>>,
  sessionId: string,
  ids: Iterable<string>,
): ReadonlyMap<string, ReadonlySet<string>> {
  const known = current.get(sessionId);
  const added = [...ids].filter((id) => !known?.has(id));
  if (!added.length) return current;
  const next = new Map(current);
  next.set(sessionId, new Set([...(known ?? []), ...added]));
  return next;
}

/**
 * Hides Items of known Subagent Turns, including live Item and text events an
 * earlier Core streamed for them, so the Session timeline stays root-only.
 */
export function rootSessionItems(items: SessionItem[], childTurnIds: ReadonlySet<string> | undefined): SessionItem[] {
  if (!childTurnIds?.size) return items;
  const visible = items.filter((item) => !childTurnIds.has(item.turn_id));
  return visible.length === items.length ? items : visible;
}
