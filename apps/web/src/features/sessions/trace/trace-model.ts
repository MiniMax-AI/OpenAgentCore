import type {
  AgentSnapshot,
  AgentTurn,
  ItemStatus,
  SessionItem,
} from "@agents-core-web/agents-client";

export type TraceValueState = "available" | "unavailable" | "unknown";

export interface TraceValue<T> {
  state: TraceValueState;
  value: T | null;
}

export type TraceLane = "configuration" | "input" | "model" | "tools" | "unknown";
export type TraceRowKind =
  | "configured_instructions"
  | "user_message"
  | "assistant_message"
  | "tool_call"
  | "tool_result"
  | "unknown_item";
export type TraceRowLabel = "SYSTEM" | "USER" | "ASSISTANT" | "TOOL" | "ITEM";

export interface TraceConfiguredFunctionTool {
  type: "function";
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  defer_loading?: boolean;
}

export interface TraceToolDetails {
  type: string;
  name: string | null;
  payload: TraceValue<unknown>;
  result: TraceValue<unknown>;
  configuredFunction: TraceValue<TraceConfiguredFunctionTool>;
}

/**
 * An allow-listed projection for the Raw tab. Durable identifiers used to join
 * resources stay on sourceItems and are deliberately absent here. Values of
 * public protocol fields remain verbatim; this is not recursive redaction.
 */
export type TraceSafeRaw = Readonly<Record<string, unknown>>;

export interface TraceRow {
  id: string;
  groupId: string;
  kind: TraceRowKind;
  lane: TraceLane;
  label: TraceRowLabel;
  title: string;
  text: TraceValue<string>;
  status: ItemStatus | null;
  durationMs: TraceValue<number>;
  tool: TraceToolDetails | null;
  safeRaw: readonly TraceSafeRaw[];
  /** Original public Item snapshots, retained for specialised safe renderers. */
  sourceItems: readonly SessionItem[];
  searchText: string;
}

export type TraceGroupKind = "configuration" | "turn" | "orphan";

export interface TraceGroup {
  id: string;
  kind: TraceGroupKind;
  title: string;
  turnIndex: number | null;
  turn: AgentTurn | null;
  turnWallClockDurationMs: TraceValue<number>;
  rows: readonly TraceRow[];
  searchText: string;
}

export interface TraceSummary {
  turnCount: number;
  toolCallCount: number;
  knownTurnWallClockDurationMs: number;
  turnsWithKnownWallClock: number;
  turnsWithUnknownWallClock: number;
}

export interface TraceModel {
  groups: readonly TraceGroup[];
  rows: readonly TraceRow[];
  summary: TraceSummary;
}

export type TraceAgentSnapshot = Pick<AgentSnapshot, "instructions" | "tools">;

export interface BuildTraceModelInput {
  turns: readonly AgentTurn[];
  items: readonly SessionItem[];
  agent: TraceAgentSnapshot | null;
}

const SAFE_ITEM_FIELDS = [
  "type",
  "status",
  "role",
  "phase",
  "content",
  "command",
  "cwd",
  "duration_ms",
  "exit_code",
  "name",
  "server_label",
  "arguments",
  "output",
  "error",
  "action",
] as const;

const TOOL_CALL_TYPES = new Set<SessionItem["type"]>([
  "command_execution",
  "mcp_call",
  "function_call",
  "web_search_call",
]);
const ITEM_STATUSES = new Set<ItemStatus>(["in_progress", "completed", "failed", "incomplete"]);
const MESSAGE_ROLES = new Set(["user", "assistant"]);
const MESSAGE_PHASES = new Set(["commentary", "final_answer"]);
const CONTENT_TYPES = new Set(["input_text", "output_text", "input_image"]);
const WEB_SEARCH_ACTION_TYPES = new Set(["search", "open_page", "find_in_page", "other"]);
const SEARCH_TEXT_LIMIT = 16_384;
const SEARCH_NODE_LIMIT = 2_048;

function available<T>(value: T): TraceValue<T> {
  return { state: "available", value };
}

function unavailable<T>(): TraceValue<T> {
  return { state: "unavailable", value: null };
}

function unknown<T>(): TraceValue<T> {
  return { state: "unknown", value: null };
}

function hasOwn(value: object, key: PropertyKey): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

function optionalNullableString(raw: Record<string, unknown>, key: string): boolean {
  return !hasOwn(raw, key) || raw[key] === undefined || raw[key] === null || typeof raw[key] === "string";
}

function optionalSafeInteger(raw: Record<string, unknown>, key: string, nonNegative: boolean): boolean {
  if (!hasOwn(raw, key) || raw[key] === undefined || raw[key] === null) return true;
  const value = raw[key];
  return typeof value === "number" && Number.isSafeInteger(value) && (!nonNegative || value >= 0);
}

function validContent(value: unknown): boolean {
  if (!Array.isArray(value)) return false;
  return value.every((candidate) => {
    if (!isRecord(candidate) || !CONTENT_TYPES.has(candidate.type as string)) return false;
    return optionalNullableString(candidate, "text")
      && (!hasOwn(candidate, "image_url") || candidate.image_url === undefined || typeof candidate.image_url === "string");
  });
}

function validWebSearchAction(value: unknown): boolean {
  if (!isRecord(value) || !WEB_SEARCH_ACTION_TYPES.has(value.type as string)) return false;
  if (
    !optionalNullableString(value, "query")
    || !optionalNullableString(value, "url")
    || !optionalNullableString(value, "pattern")
  ) return false;
  return !hasOwn(value, "queries")
    || value.queries === undefined
    || (Array.isArray(value.queries) && value.queries.every((query) => typeof query === "string"));
}

function validatedItemStatus(item: SessionItem): ItemStatus | null {
  const status = (item as unknown as Record<string, unknown>).status;
  return typeof status === "string" && ITEM_STATUSES.has(status as ItemStatus) ? status as ItemStatus : null;
}

function validatedItemType(item: SessionItem): SessionItem["type"] | null {
  const raw = item as unknown as Record<string, unknown>;
  if (
    !isNonEmptyString(raw.id)
    || !isNonEmptyString(raw.turn_id)
    || validatedItemStatus(item) === null
  ) return null;

  if (raw.type === "message") {
    return MESSAGE_ROLES.has(raw.role as string)
      && validContent(raw.content)
      && (!hasOwn(raw, "phase") || raw.phase === undefined || raw.phase === null || MESSAGE_PHASES.has(raw.phase as string))
      ? raw.type
      : null;
  }
  if (raw.type === "command_execution") {
    return isNonEmptyString(raw.command)
      && optionalNullableString(raw, "cwd")
      && optionalSafeInteger(raw, "duration_ms", true)
      && optionalSafeInteger(raw, "exit_code", false)
      ? raw.type
      : null;
  }
  if (raw.type === "mcp_call") {
    return isNonEmptyString(raw.server_label)
      && isNonEmptyString(raw.name)
      && hasOwn(raw, "arguments")
      && optionalSafeInteger(raw, "duration_ms", true)
      ? raw.type
      : null;
  }
  if (raw.type === "function_call") {
    return isNonEmptyString(raw.name)
      && isNonEmptyString(raw.call_id)
      && hasOwn(raw, "arguments")
      && optionalSafeInteger(raw, "duration_ms", true)
      ? raw.type
      : null;
  }
  if (raw.type === "function_call_output") {
    return isNonEmptyString(raw.call_id)
      && (hasOwn(raw, "output") || hasOwn(raw, "error"))
      && optionalNullableString(raw, "name")
      && optionalSafeInteger(raw, "duration_ms", true)
      ? raw.type
      : null;
  }
  if (raw.type === "web_search_call") {
    return validWebSearchAction(raw.action)
      && optionalSafeInteger(raw, "duration_ms", true)
      ? raw.type
      : null;
  }
  return null;
}

function safeInteger(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
}

function turnDuration(turn: AgentTurn): TraceValue<number> {
  const startedAt = safeInteger(turn.started_at);
  const completedAt = safeInteger(turn.completed_at);
  if (startedAt === null || completedAt === null || completedAt < startedAt) return unknown();
  return available((completedAt - startedAt) * 1_000);
}

function safeItemProjection(item: SessionItem): TraceSafeRaw {
  const raw = item as unknown as Record<string, unknown>;
  const projected: Record<string, unknown> = {};
  for (const field of SAFE_ITEM_FIELDS) {
    if (hasOwn(raw, field)) projected[field] = raw[field];
  }
  return projected;
}

function makeSearchText(...values: unknown[]): string {
  const parts: string[] = [];
  let remaining = SEARCH_TEXT_LIMIT;
  const seen = new WeakSet<object>();
  const queue = [...values];
  for (let cursor = 0; cursor < queue.length && cursor < SEARCH_NODE_LIMIT && remaining > 0; cursor += 1) {
    const value = queue[cursor];
    if (value === null || value === undefined) continue;
    if (typeof value === "object") {
      if (seen.has(value)) continue;
      seen.add(value);
      if (Array.isArray(value)) {
        for (let index = 0; index < value.length && queue.length + 2 <= SEARCH_NODE_LIMIT; index += 1) {
          queue.push(String(index), value[index]);
        }
      } else {
        const record = value as Record<string, unknown>;
        for (const key in record) {
          if (!hasOwn(record, key)) continue;
          if (queue.length + 2 > SEARCH_NODE_LIMIT) break;
          queue.push(key, record[key]);
        }
      }
      continue;
    }
    const rendered = typeof value === "string" ? value : String(value);
    if (!rendered) continue;
    const bounded = rendered.slice(0, remaining);
    parts.push(bounded);
    remaining -= bounded.length;
  }
  return parts.join("\n").toLocaleLowerCase();
}

function messageText(item: SessionItem): TraceValue<string> {
  if (!Array.isArray(item.content)) return unavailable();
  const text = item.content
    .map((part) => typeof part?.text === "string" ? part.text : null)
    .filter((part): part is string => part !== null)
    .join("\n");
  return text || item.content.some((part) => typeof part?.text === "string")
    ? available(text)
    : unavailable();
}

function hasOnlyKeys(value: Record<string, unknown>, allowed: ReadonlySet<string>): boolean {
  return Object.keys(value).every((key) => allowed.has(key));
}

const FUNCTION_TOOL_FIELDS = new Set(["type", "name", "description", "parameters", "defer_loading"]);

function canonicalFunctionTool(value: unknown): TraceConfiguredFunctionTool | null {
  if (!isRecord(value) || !hasOnlyKeys(value, FUNCTION_TOOL_FIELDS)) return null;
  if (
    value.type !== "function"
    || typeof value.name !== "string"
    || !value.name.trim()
    || typeof value.description !== "string"
    || !isRecord(value.parameters)
    || (value.defer_loading !== undefined && typeof value.defer_loading !== "boolean")
  ) return null;
  return {
    type: "function",
    name: value.name,
    description: value.description,
    parameters: value.parameters,
    ...(value.defer_loading === undefined ? {} : { defer_loading: value.defer_loading }),
  };
}

function configuredFunctions(agent: TraceAgentSnapshot | null): Map<string, TraceConfiguredFunctionTool | null> | null {
  if (!agent || !Array.isArray(agent.tools)) return null;
  const functions = new Map<string, TraceConfiguredFunctionTool | null>();
  for (const candidate of agent.tools) {
    const tool = canonicalFunctionTool(candidate);
    if (!tool) continue;
    functions.set(tool.name, functions.has(tool.name) ? null : tool);
  }
  return functions;
}

function configuredFunction(
  functions: Map<string, TraceConfiguredFunctionTool | null> | null,
  name: string | undefined,
): TraceValue<TraceConfiguredFunctionTool> {
  if (functions === null) return unknown();
  if (!name || !functions.has(name)) return unavailable();
  const match = functions.get(name);
  return match ? available(match) : unavailable();
}

function durationFromItems(primary: SessionItem, results: readonly SessionItem[]): TraceValue<number> {
  const candidates = results.length ? [...results].reverse() : [primary];
  for (const candidate of candidates) {
    if (!hasOwn(candidate, "duration_ms") || candidate.duration_ms === null || candidate.duration_ms === undefined) continue;
    const duration = safeInteger(candidate.duration_ms);
    return duration === null ? unknown() : available(duration);
  }
  return unavailable();
}

function payloadFor(item: SessionItem): TraceValue<unknown> {
  if (item.type === "command_execution") {
    const payload: Record<string, unknown> = {};
    if (hasOwn(item, "command") && item.command !== undefined) payload.command = item.command;
    if (hasOwn(item, "cwd") && item.cwd !== undefined) payload.cwd = item.cwd;
    return Object.keys(payload).length ? available(payload) : unavailable();
  }
  if (item.type === "web_search_call") {
    return hasOwn(item, "action") && item.action !== undefined ? available(item.action) : unavailable();
  }
  if (item.type === "function_call" || item.type === "mcp_call") {
    return hasOwn(item, "arguments") && item.arguments !== undefined ? available(item.arguments) : unavailable();
  }
  return unavailable();
}

function resultPart(item: SessionItem): TraceValue<unknown> {
  const result: Record<string, unknown> = {};
  if (hasOwn(item, "output") && item.output !== undefined) result.output = item.output;
  if (hasOwn(item, "error") && item.error !== undefined && item.error !== null) result.error = item.error;
  if (hasOwn(item, "exit_code") && item.exit_code !== undefined && item.exit_code !== null) result.exit_code = item.exit_code;
  if (hasOwn(item, "duration_ms") && item.duration_ms !== undefined && item.duration_ms !== null) result.duration_ms = item.duration_ms;
  const entries = Object.entries(result);
  if (!entries.length) return unavailable();
  return entries.length === 1 && entries[0]?.[0] === "output"
    ? available(entries[0][1])
    : available(result);
}

function resultFor(primary: SessionItem, results: readonly SessionItem[]): TraceValue<unknown> {
  const sources = results.length ? results : [primary];
  const values = sources
    .map(resultPart)
    .filter((part): part is TraceValue<unknown> & { state: "available" } => part.state === "available");
  const first = values[0];
  if (!first) return unavailable();
  return values.length === 1 ? available(first.value) : available(values.map((part) => part.value));
}

function toolTitle(item: SessionItem): string {
  if (item.type === "command_execution") return item.command || "Command execution";
  if (item.type === "web_search_call") {
    return item.action?.query
      || item.action?.queries?.join(", ")
      || item.action?.url
      || item.action?.pattern
      || "Web search";
  }
  if (item.type === "mcp_call") return item.name || item.server_label || "MCP call";
  if (item.type === "function_call_output") return item.name || "Function result";
  return item.name || "Function call";
}

function traceItemRow(
  item: SessionItem,
  results: readonly SessionItem[],
  groupId: string,
  functions: Map<string, TraceConfiguredFunctionTool | null> | null,
  rowId: string,
  sourceOrder: readonly SessionItem[] = [item, ...results],
): TraceRow {
  const sourceItems = [...sourceOrder];
  const safeRaw = sourceItems.map(safeItemProjection);
  const itemType = validatedItemType(item);
  const rawType = (item as unknown as Record<string, unknown>).type;
  let kind: TraceRowKind = "unknown_item";
  let lane: TraceLane = "unknown";
  let label: TraceRowLabel = "ITEM";
  let title = typeof rawType === "string" && rawType.length > 0
    ? `Unsupported ${rawType.slice(0, 80)} Item`
    : "Unsupported Item";
  let text = unavailable<string>();
  let tool: TraceToolDetails | null = null;
  let status = validatedItemStatus(item);
  let durationMs = unavailable<number>();

  if (itemType === "message" && item.role === "user") {
    kind = "user_message";
    lane = "input";
    label = "USER";
    title = "User message";
    text = messageText(item);
  } else if (itemType === "message" && item.role === "assistant") {
    kind = "assistant_message";
    lane = "model";
    label = "ASSISTANT";
    title = item.phase === "commentary"
      ? "Assistant commentary"
      : item.phase === "final_answer" ? "Assistant answer" : "Assistant message";
    text = messageText(item);
  } else if (itemType && (TOOL_CALL_TYPES.has(itemType) || itemType === "function_call_output")) {
    kind = itemType === "function_call_output" ? "tool_result" : "tool_call";
    lane = "tools";
    label = "TOOL";
    title = toolTitle(item);
    const resultItem = results.at(-1);
    if (resultItem) status = validatedItemStatus(resultItem);
    durationMs = durationFromItems(item, results);
    tool = {
      type: item.type,
      name: item.name ?? null,
      payload: payloadFor(item),
      result: resultFor(item, results),
      configuredFunction: itemType === "function_call"
        ? configuredFunction(functions, item.name)
        : unavailable(),
    };
  }

  const searchText = makeSearchText(
    label,
    title,
    text.state,
    text.value,
    status,
    durationMs.state,
    durationMs.value,
    tool?.configuredFunction,
    safeRaw,
  );
  return {
    id: rowId,
    groupId,
    kind,
    lane,
    label,
    title,
    text,
    status,
    durationMs,
    tool,
    safeRaw,
    sourceItems,
    searchText,
  };
}

function groupedRows(
  items: readonly SessionItem[],
  groupId: string,
  functions: Map<string, TraceConfiguredFunctionTool | null> | null,
): TraceRow[] {
  const itemIdCounts = new Map<string, number>();
  for (const item of items) {
    const rawId = (item as unknown as Record<string, unknown>).id;
    if (!isNonEmptyString(rawId)) continue;
    itemIdCounts.set(rawId, (itemIdCounts.get(rawId) ?? 0) + 1);
  }
  const rowId = (item: SessionItem, index: number) => {
    const rawId = (item as unknown as Record<string, unknown>).id;
    if (!isNonEmptyString(rawId)) return `${groupId}:position:${index}`;
    const occurrence = itemIdCounts.get(rawId) === 1 ? "" : `:position:${index}`;
    return `${groupId}:item:${encodeURIComponent(rawId)}${occurrence}`;
  };
  const callIndexes = new Map<string, number[]>();
  const resultIndexes = new Map<string, number[]>();
  items.forEach((item, index) => {
    const itemType = validatedItemType(item);
    if (typeof item.call_id !== "string" || item.call_id.length === 0) return;
    const target = itemType === "function_call"
      ? callIndexes
      : itemType === "function_call_output" ? resultIndexes : null;
    if (!target) return;
    const indexes = target.get(item.call_id) ?? [];
    indexes.push(index);
    target.set(item.call_id, indexes);
  });

  const aggregates = new Map<number, { primary: number; results: number[] }>();
  const consumed = new Set<number>();
  for (const [callId, calls] of callIndexes) {
    const results = resultIndexes.get(callId) ?? [];
    if (calls.length !== 1 || results.length !== 1) continue;
    const primary = calls[0];
    const result = results[0];
    if (primary === undefined || result === undefined || result <= primary) continue;
    aggregates.set(primary, { primary, results: [result] });
    consumed.add(primary);
    consumed.add(result);
  }

  const rows: TraceRow[] = [];
  items.forEach((item, index) => {
    const aggregate = aggregates.get(index);
    if (aggregate) {
      const primary = items[aggregate.primary];
      if (!primary) return;
      const results = aggregate.results
        .map((resultIndex) => items[resultIndex])
        .filter((result): result is SessionItem => result !== undefined);
      const originalOrder = [aggregate.primary, ...aggregate.results]
        .sort((left, right) => left - right)
        .map((memberIndex) => items[memberIndex])
        .filter((member): member is SessionItem => member !== undefined);
      rows.push(traceItemRow(
        primary,
        results,
        groupId,
        functions,
        rowId(primary, aggregate.primary),
        originalOrder,
      ));
      return;
    }
    if (!consumed.has(index)) rows.push(traceItemRow(item, [], groupId, functions, rowId(item, index)));
  });
  return rows;
}

function instructionsGroup(agent: TraceAgentSnapshot | null): TraceGroup {
  const instructions = agent === null
    ? unknown<string>()
    : typeof agent.instructions === "string" ? available(agent.instructions) : unavailable<string>();
  const safeRaw: TraceSafeRaw = {
    type: "configured_instructions",
    availability: instructions.state,
    ...(instructions.state === "available" ? { instructions: instructions.value } : {}),
  };
  const groupId = "configuration";
  const row: TraceRow = {
    id: "configuration:instructions",
    groupId,
    kind: "configured_instructions",
    lane: "configuration",
    label: "SYSTEM",
    title: "Configured instructions",
    text: instructions,
    status: null,
    durationMs: unavailable(),
    tool: null,
    safeRaw: [safeRaw],
    sourceItems: [],
    searchText: makeSearchText("SYSTEM", "Configured instructions", instructions.state, instructions.value, safeRaw),
  };
  return {
    id: groupId,
    kind: "configuration",
    title: "Agent configuration",
    turnIndex: null,
    turn: null,
    turnWallClockDurationMs: unavailable(),
    rows: [row],
    searchText: makeSearchText("Agent configuration", row.searchText),
  };
}

function turnGroup(
  turn: AgentTurn,
  index: number,
  items: readonly SessionItem[],
  functions: Map<string, TraceConfiguredFunctionTool | null> | null,
): TraceGroup {
  const turnId = typeof turn.id === "string" && turn.id.length > 0 ? turn.id : `position-${index}`;
  const groupId = `turn:${encodeURIComponent(turnId)}`;
  const rows = groupedRows(items, groupId, functions);
  const duration = turnDuration(turn);
  return {
    id: groupId,
    kind: "turn",
    title: `Turn ${index + 1}`,
    turnIndex: index,
    turn,
    turnWallClockDurationMs: duration,
    rows,
    searchText: makeSearchText(`Turn ${index + 1}`, turn.status, duration.state, duration.value),
  };
}

function orphanGroup(
  turnId: string | null,
  index: number,
  items: readonly SessionItem[],
  functions: Map<string, TraceConfiguredFunctionTool | null> | null,
): TraceGroup {
  const groupId = turnId === null ? "orphan:unknown-turn" : `orphan:${encodeURIComponent(turnId)}`;
  const rows = groupedRows(items, groupId, functions);
  return {
    id: groupId,
    kind: "orphan",
    title: `Unassociated Items ${index + 1}`,
    turnIndex: null,
    turn: null,
    turnWallClockDurationMs: unknown(),
    rows,
    searchText: makeSearchText("Unassociated Items", "Unknown Turn"),
  };
}

export function buildTraceModel({ turns, items, agent }: BuildTraceModelInput): TraceModel {
  const functions = configuredFunctions(agent);
  const knownTurnIds = new Set(turns.flatMap((turn) => (
    typeof turn.id === "string" && turn.id.length > 0 ? [turn.id] : []
  )));
  const itemsByTurn = new Map<string, SessionItem[]>();
  const orphanTurnIds: string[] = [];
  const unknownTurnItems: SessionItem[] = [];
  for (const item of items) {
    const turnId = typeof item.turn_id === "string" && item.turn_id.length > 0 ? item.turn_id : null;
    if (turnId === null) {
      unknownTurnItems.push(item);
      continue;
    }
    const groupItems = itemsByTurn.get(turnId);
    if (groupItems) groupItems.push(item);
    else {
      itemsByTurn.set(turnId, [item]);
      if (!knownTurnIds.has(turnId)) orphanTurnIds.push(turnId);
    }
  }

  // listTurns(order: "asc") is the ordering authority. Item arrival order must
  // not reorder complete Turn history.
  const turnGroups = turns.map((turn, index) => turnGroup(
    turn,
    index,
    itemsByTurn.get(turn.id) ?? [],
    functions,
  ));
  const orphanGroups = orphanTurnIds.map((turnId, index) => orphanGroup(
    turnId,
    index,
    itemsByTurn.get(turnId) ?? [],
    functions,
  ));
  if (unknownTurnItems.length) {
    orphanGroups.push(orphanGroup(null, orphanGroups.length, unknownTurnItems, functions));
  }
  const groups = [instructionsGroup(agent), ...turnGroups, ...orphanGroups];
  const rows = groups.flatMap((group) => group.rows);
  const durations = turnGroups.map((group) => group.turnWallClockDurationMs);
  const knownDurations = durations.filter(
    (duration): duration is TraceValue<number> & { state: "available"; value: number } => (
      duration.state === "available" && duration.value !== null
    ),
  );
  return {
    groups,
    rows,
    summary: {
      turnCount: turns.length,
      toolCallCount: items.filter((item) => {
        const itemType = validatedItemType(item);
        return itemType !== null && TOOL_CALL_TYPES.has(itemType);
      }).length,
      knownTurnWallClockDurationMs: knownDurations.reduce((total, duration) => total + duration.value, 0),
      turnsWithKnownWallClock: knownDurations.length,
      turnsWithUnknownWallClock: durations.length - knownDurations.length,
    },
  };
}

function includesEveryTerm(searchText: string, terms: readonly string[]): boolean {
  return terms.every((term) => searchText.includes(term));
}

/** Returns a filtered view without mutating or rebuilding the durable model. */
export function filterTraceModel(model: TraceModel, query: string): TraceModel {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean);
  if (!terms.length) return model;
  const groups = model.groups.flatMap((group): TraceGroup[] => {
    if (includesEveryTerm(group.searchText, terms)) return [{ ...group }];
    const rows = group.rows.filter((row) => includesEveryTerm(row.searchText, terms));
    return rows.length ? [{ ...group, rows }] : [];
  });
  return {
    groups,
    rows: groups.flatMap((group) => group.rows),
    summary: model.summary,
  };
}
