import type { AgentTurn, ItemContent, SessionItem, ListPage, PageOptions } from "./types";
import { exactFields, onlyFields, isRecord, hasOwn, isNonnegativeInteger, sameResourceId } from "./response-projection";
import { projectTokenUsage } from "./usage-projection";

const turnFields = new Set([
  "id", "agent_id", "session_id", "object", "status", "created_at", "started_at", "completed_at", "error", "usage", "subagent_id",
]);
const turnErrorFields = new Set(["code", "message"]);
const itemContentTextFields = new Set(["type", "text"]);
const itemContentImageFields = new Set(["type", "image_url"]);
const knownItemTypes = new Set([
  "message", "command_execution", "mcp_call", "function_call", "function_call_output", "web_search_call",
  "reasoning", "agent_message", "create_subagent_call", "send_subagent_input_call", "resume_subagent_call",
  "wait_for_subagents_call", "interrupt_subagent_call", "close_subagent_call",
]);
const itemStatuses = new Set(["in_progress", "completed", "failed", "incomplete"]);
const turnStatuses = new Set(["queued", "in_progress", "waiting", "completed", "failed", "cancelled"]);


export function projectItemContent(value: unknown, invalid: () => never): ItemContent {
  if (!isRecord(value) || typeof value.type !== "string") return invalid();
  if (value.type === "input_text" || value.type === "output_text") {
    if (!exactFields(value, itemContentTextFields) || typeof value.text !== "string") {
      return invalid();
    }
    return { type: value.type, text: value.text };
  }
  if (
    value.type !== "input_image" || !exactFields(value, itemContentImageFields) ||
    typeof value.image_url !== "string"
  ) return invalid();
  return { type: "input_image", image_url: value.image_url };
}

function validOptionalInteger(value: unknown): boolean {
  return value === undefined || value === null || isNonnegativeInteger(value);
}

function validOptionalSafeInteger(value: unknown): boolean {
  return value === undefined || value === null || Number.isSafeInteger(value);
}

function projectWebSearchAction(value: unknown, invalid: () => never): SessionItem["action"] {
  const fields = new Set(["type", "query", "queries", "url", "pattern"]);
  if (
    !isRecord(value) || !onlyFields(value, fields) ||
    (value.type !== "search" && value.type !== "open_page" && value.type !== "find_in_page" && value.type !== "other") ||
    !(value.query === undefined || value.query === null || typeof value.query === "string") ||
    !(value.url === undefined || value.url === null || typeof value.url === "string") ||
    !(value.pattern === undefined || value.pattern === null || typeof value.pattern === "string") ||
    !(value.queries === undefined || (Array.isArray(value.queries) && value.queries.every((entry) => typeof entry === "string")))
  ) return invalid();
  return { ...value } as unknown as SessionItem["action"];
}

function itemVariantFields(type: string): Set<string> {
  const common = ["id", "turn_id", "type", "status"];
  switch (type) {
    case "message": return new Set([...common, "role", "phase", "content"]);
    case "command_execution": return new Set([...common, "command", "cwd", "duration_ms", "exit_code", "output"]);
    case "mcp_call": return new Set([...common, "server_label", "name", "arguments", "output", "error"]);
    case "function_call": return new Set([...common, "name", "call_id", "arguments"]);
    case "function_call_output": return new Set([...common, "call_id", "output", "error", "duration_ms"]);
    case "web_search_call": return new Set([...common, "action"]);
    case "reasoning": return new Set([...common, "summary"]);
    case "agent_message": return new Set(["id", "turn_id", "type", "sender_agent_id", "recipient_agent_id", "content"]);
    case "create_subagent_call": return new Set([...common, "agent_id", "content", "model", "reasoning_effort"]);
    case "send_subagent_input_call": return new Set([...common, "sender_agent_id", "recipient_agent_id", "content"]);
    case "wait_for_subagents_call": return new Set([...common, "sender_agent_id", "recipient_agent_ids"]);
    case "resume_subagent_call":
    case "interrupt_subagent_call":
    case "close_subagent_call": return new Set([...common, "sender_agent_id", "recipient_agent_id"]);
    default: return new Set(common);
  }
}

export function projectSessionItem(value: unknown, invalid: () => never): SessionItem {
  if (
    !isRecord(value) ||
    typeof value.id !== "string" || value.id === "" ||
    typeof value.turn_id !== "string" || value.turn_id === "" ||
    typeof value.type !== "string" || !knownItemTypes.has(value.type) ||
    !onlyFields(value, itemVariantFields(value.type))
  ) return invalid();
  if (value.type !== "agent_message" && !(
    (value.type === "reasoning" && (value.status === undefined || value.status === null)) ||
    (typeof value.status === "string" && itemStatuses.has(value.status) && !(value.type === "reasoning" && value.status === "failed"))
  )) return invalid();

  const projected: Record<string, unknown> = {
    id: value.id,
    turn_id: value.turn_id,
    type: value.type,
    ...(hasOwn(value, "status") ? { status: value.status } : {}),
  };
  switch (value.type) {
    case "message":
      if (
        (value.role !== "user" && value.role !== "assistant") || !Array.isArray(value.content) ||
        !(value.phase === undefined || value.phase === null || value.phase === "commentary" || value.phase === "final_answer")
      ) return invalid();
      projected.role = value.role;
      projected.content = Array.from(value.content, (part) => projectItemContent(part, invalid));
      // Current Cores send null when there is no phase; older ones omit it.
      if (value.phase !== undefined) projected.phase = value.phase;
      break;
    case "command_execution":
      if (
        typeof value.command !== "string" ||
        !(value.cwd === undefined || value.cwd === null || typeof value.cwd === "string") ||
        !validOptionalInteger(value.duration_ms) || !validOptionalSafeInteger(value.exit_code)
      ) return invalid();
      projected.command = value.command;
      for (const field of ["cwd", "duration_ms", "exit_code", "output"] as const) {
        if (hasOwn(value, field)) projected[field] = value[field];
      }
      break;
    case "mcp_call":
      if (
        typeof value.server_label !== "string" || value.server_label === "" ||
        typeof value.name !== "string" || value.name === "" ||
        !hasOwn(value, "arguments") || !hasOwn(value, "output") || !hasOwn(value, "error")
      ) return invalid();
      projected.server_label = value.server_label;
      projected.name = value.name;
      projected.arguments = value.arguments;
      projected.output = value.output;
      projected.error = value.error;
      break;
    case "function_call":
      if (
        typeof value.call_id !== "string" || value.call_id === "" ||
        typeof value.name !== "string" || value.name === "" || !hasOwn(value, "arguments")
      ) return invalid();
      projected.call_id = value.call_id;
      projected.name = value.name;
      projected.arguments = value.arguments;
      break;
    case "function_call_output":
      if (typeof value.call_id !== "string" || value.call_id === "" || !validOptionalInteger(value.duration_ms)) {
        return invalid();
      }
      projected.call_id = value.call_id;
      for (const field of ["output", "error", "duration_ms"] as const) {
        if (hasOwn(value, field)) projected[field] = value[field];
      }
      break;
    case "web_search_call":
      // Parsar omits action when the runtime has not reported one yet.
      if (hasOwn(value, "action")) projected.action = projectWebSearchAction(value.action, invalid);
      break;
    case "reasoning":
      if (!Array.isArray(value.summary)) return invalid();
      projected.summary = value.summary.map((part) => {
        if (!isRecord(part) || !exactFields(part, new Set(["type", "text"])) || part.type !== "summary_text" || typeof part.text !== "string") return invalid();
        return { type: "summary_text", text: part.text };
      });
      break;
    case "create_subagent_call":
      if (!nonemptyString(value.agent_id) || !optionalString(value.model) || !optionalString(value.reasoning_effort)) return invalid();
      projected.agent_id = value.agent_id;
      projected.content = projectCoordinationContent(value.content, invalid);
      for (const field of ["model", "reasoning_effort"]) {
        if (hasOwn(value, field)) projected[field] = value[field];
      }
      break;
    case "agent_message":
    case "send_subagent_input_call":
    case "resume_subagent_call":
    case "interrupt_subagent_call":
    case "close_subagent_call":
      if (!nonemptyString(value.sender_agent_id) || !nonemptyString(value.recipient_agent_id)) return invalid();
      projected.sender_agent_id = value.sender_agent_id;
      projected.recipient_agent_id = value.recipient_agent_id;
      if (value.type === "agent_message" || value.type === "send_subagent_input_call") {
        projected.content = projectCoordinationContent(value.content, invalid);
      }
      break;
    case "wait_for_subagents_call":
      if (!nonemptyString(value.sender_agent_id) || !Array.isArray(value.recipient_agent_ids) || !value.recipient_agent_ids.every(nonemptyString)) return invalid();
      projected.sender_agent_id = value.sender_agent_id;
      projected.recipient_agent_ids = [...value.recipient_agent_ids];
      break;
  }
  return projected as unknown as SessionItem;
}

export function projectAgentTurn(value: unknown, expectedSessionId: string, invalid: () => never, expectedTurnId?: string): AgentTurn {
  if (
    !isRecord(value) || !onlyFields(value, turnFields) ||
    typeof value.id !== "string" || value.id === "" ||
    (expectedTurnId !== undefined && !sameResourceId(value.id, expectedTurnId)) ||
    typeof value.agent_id !== "string" || value.agent_id === "" ||
    // A child Turn carries the Session's Agent ID; subagent_id names the child.
    !(value.subagent_id === undefined || value.subagent_id === null || nonemptyString(value.subagent_id)) ||
    typeof value.session_id !== "string" || !sameResourceId(value.session_id, expectedSessionId) ||
    value.object !== "agent.session.turn" || typeof value.status !== "string" || !turnStatuses.has(value.status) ||
    !isNonnegativeInteger(value.created_at) ||
    !(value.started_at === null || isNonnegativeInteger(value.started_at)) ||
    !(value.completed_at === null || isNonnegativeInteger(value.completed_at))
  ) return invalid();
  let error: AgentTurn["error"] = null;
  if (value.error !== null) {
    if (
      !isRecord(value.error) || !exactFields(value.error, turnErrorFields) ||
      value.error.code !== "internal_error" || typeof value.error.message !== "string"
    ) return invalid();
    error = { code: "internal_error", message: value.error.message };
  }
  return {
    id: value.id,
    agent_id: value.agent_id,
    ...(hasOwn(value, "subagent_id") ? { subagent_id: value.subagent_id as string | null } : {}),
    session_id: value.session_id,
    object: "agent.session.turn",
    status: value.status as AgentTurn["status"],
    created_at: value.created_at,
    started_at: value.started_at,
    completed_at: value.completed_at,
    error,
    usage: projectTokenUsage(value.usage, invalid),
  };
}

function nonemptyString(value: unknown): value is string {
  return typeof value === "string" && value !== "";
}

function optionalString(value: unknown): boolean {
  return value === undefined || value === null || typeof value === "string";
}

function projectCoordinationContent(value: unknown, invalid: () => never): ItemContent[] {
  if (!Array.isArray(value)) return invalid();
  return value.map((part) => {
    if (!isRecord(part)) return invalid();
    if (part.type === "output_text") return projectItemContent(part, invalid);
    if (part.type !== "encrypted_content" || !exactFields(part, new Set(["type", "encrypted_content"])) || typeof part.encrypted_content !== "string") return invalid();
    return { type: "encrypted_content", encrypted_content: part.encrypted_content };
  });
}

export function validateHistoryPageOptions(options?: PageOptions): void {
  if (options?.limit !== undefined && (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100)) {
    throw new TypeError("History list limit must be an integer from 1 through 100.");
  }
  if (options?.order !== undefined && options.order !== "asc" && options.order !== "desc") {
    throw new TypeError("History list order must be asc or desc.");
  }
  if (options?.after !== undefined && !nonemptyString(options.after)) {
    throw new TypeError("History list cursor must be a nonempty resource ID.");
  }
}

export function projectHistoryPage<T extends { id: string }>(
  value: unknown,
  options: PageOptions | undefined,
  project: (entry: unknown) => T,
  invalid: () => never,
): ListPage<T> {
  if (!isRecord(value) || !onlyFields(value, new Set(["data", "has_more", "object", "first_id", "last_id"])) ||
    !Array.isArray(value.data) || typeof value.has_more !== "boolean" ||
    value.data.length > (options?.limit ?? 20) || (value.has_more && value.data.length === 0) ||
    (hasOwn(value, "object") && value.object !== "list")
  ) return invalid();
  const data = value.data.map(project);
  if (data.some((entry, index) =>
    (options?.after !== undefined && sameResourceId(entry.id, options.after)) ||
    data.slice(0, index).some((previous) => sameResourceId(previous.id, entry.id))
  )) return invalid();
  const first = data[0]?.id ?? null;
  const last = data[data.length - 1]?.id ?? null;
  if ((hasOwn(value, "first_id") && value.first_id !== first) || (hasOwn(value, "last_id") && value.last_id !== last)) return invalid();
  return {
    data, has_more: value.has_more,
    ...(hasOwn(value, "object") ? { object: "list" as const } : {}),
    ...(hasOwn(value, "first_id") ? { first_id: first } : {}),
    ...(hasOwn(value, "last_id") ? { last_id: last } : {}),
  };
}
