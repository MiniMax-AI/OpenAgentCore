import type { AgentTurn, ItemContent, SessionItem, ListPage, PageOptions, WebSearchAction } from "./types";
import { exactFields, isOneOf, isRecord, hasOwn, isNonnegativeInteger, sameResourceId, variantFields } from "./response-projection";
import { projectTokenUsage } from "./usage-projection";
import {
  functionCallStatusResourceValues, messageContentResourceFields, messagePhaseResourceValues, outputItemStatusResourceValues,
  sessionItemListResourceFields, sessionMessageRoleResourceValues, sessionTurnErrorCodeResourceValues, sessionTurnErrorResourceFields,
  sessionTurnItemResourceFields, summaryTextResourceFields, turnResourceFields, turnStatusResourceValues, webSearchActionResourceFields,
  encryptedContentResourceFields, type SessionTurnErrorCodeResource,
} from "./generated/public-api";

export function projectItemContent(value: unknown, invalid: () => never): ItemContent {
  const fields = isRecord(value) ? variantFields(messageContentResourceFields, value.type) : undefined;
  if (!isRecord(value) || fields === undefined || !exactFields(value, fields)) return invalid();
  if (value.type === "input_image") {
    if (typeof value.image_url !== "string") return invalid();
    return { type: "input_image", image_url: value.image_url };
  }
  if (typeof value.text !== "string") return invalid();
  return { type: value.type as "input_text" | "output_text", text: value.text };
}

function projectWebSearchAction(value: unknown, invalid: () => never): WebSearchAction {
  const fields = isRecord(value) ? variantFields(webSearchActionResourceFields, value.type) : undefined;
  if (
    !isRecord(value) || fields === undefined || !exactFields(value, fields) ||
    ["query", "url", "pattern"].some((field) => hasOwn(value, field) && !nullableString(value[field])) ||
    !(value.queries === undefined || value.queries === null ||
      (Array.isArray(value.queries) && value.queries.every((entry) => typeof entry === "string")))
  ) return invalid();
  return { ...value } as unknown as WebSearchAction;
}

// The status enum each Item schema uses; agent_message has no status.
function validItemStatus(type: unknown, status: unknown): boolean {
  switch (type) {
    case "agent_message": return status === undefined;
    case "reasoning": return status === null || isOneOf(outputItemStatusResourceValues, status);
    case "message":
    case "web_search_call": return isOneOf(outputItemStatusResourceValues, status);
    default: return isOneOf(functionCallStatusResourceValues, status);
  }
}

export function projectSessionItem(value: unknown, invalid: () => never): SessionItem {
  const fields = isRecord(value) ? variantFields(sessionTurnItemResourceFields, value.type) : undefined;
  if (
    !isRecord(value) || fields === undefined || !exactFields(value, fields) ||
    !nonemptyString(value.id) || !nonemptyString(value.turn_id) || !validItemStatus(value.type, value.status)
  ) return invalid();

  const item: Record<string, unknown> = { ...value };
  switch (value.type) {
    case "message":
      if (
        !isOneOf(sessionMessageRoleResourceValues, value.role) || !Array.isArray(value.content) ||
        !(value.phase === null || isOneOf(messagePhaseResourceValues, value.phase))
      ) return invalid();
      item.content = Array.from(value.content, (part) => projectItemContent(part, invalid));
      break;
    case "command_execution":
      if (
        typeof value.command !== "string" || !nullableString(value.cwd) ||
        !(value.duration_ms === null || isNonnegativeInteger(value.duration_ms)) ||
        !(value.exit_code === null || Number.isSafeInteger(value.exit_code))
      ) return invalid();
      break;
    case "mcp_call":
      if (!nonemptyString(value.server_label) || !nonemptyString(value.name)) return invalid();
      break;
    case "function_call":
      if (!nonemptyString(value.call_id) || !nonemptyString(value.name)) return invalid();
      break;
    case "function_call_output":
      if (!nonemptyString(value.call_id)) return invalid();
      break;
    case "web_search_call":
      if (value.action !== null) item.action = projectWebSearchAction(value.action, invalid);
      break;
    case "reasoning":
      if (!Array.isArray(value.summary)) return invalid();
      item.summary = value.summary.map((part) => {
        if (!isRecord(part) || !exactFields(part, summaryTextResourceFields) || part.type !== "summary_text" || typeof part.text !== "string") return invalid();
        return { type: "summary_text", text: part.text };
      });
      break;
    case "create_subagent_call":
      if (!nonemptyString(value.agent_id) || !nullableString(value.model) || !nullableString(value.reasoning_effort)) return invalid();
      item.content = projectCoordinationContent(value.content, invalid);
      break;
    case "wait_for_subagents_call":
      if (!nonemptyString(value.sender_agent_id) || !Array.isArray(value.recipient_agent_ids) || !value.recipient_agent_ids.every(nonemptyString)) return invalid();
      item.recipient_agent_ids = [...value.recipient_agent_ids];
      break;
    default:
      // agent_message, send_subagent_input_call and the Subagent control calls.
      if (!nonemptyString(value.sender_agent_id) || !nonemptyString(value.recipient_agent_id)) return invalid();
      if (hasOwn(value, "content")) item.content = projectCoordinationContent(value.content, invalid);
  }
  return item as unknown as SessionItem;
}

export function projectAgentTurn(value: unknown, expectedSessionId: string, invalid: () => never, expectedTurnId?: string): AgentTurn {
  if (
    !isRecord(value) || !exactFields(value, turnResourceFields) ||
    !nonemptyString(value.id) ||
    (expectedTurnId !== undefined && !sameResourceId(value.id, expectedTurnId)) ||
    !nonemptyString(value.agent_id) ||
    // A child Turn carries the Session's Agent ID; subagent_id names the child.
    !(value.subagent_id === null || nonemptyString(value.subagent_id)) ||
    typeof value.session_id !== "string" || !sameResourceId(value.session_id, expectedSessionId) ||
    value.object !== "agent.session.turn" || !isOneOf(turnStatusResourceValues, value.status) ||
    !isNonnegativeInteger(value.created_at) ||
    !(value.started_at === null || isNonnegativeInteger(value.started_at)) ||
    !(value.completed_at === null || isNonnegativeInteger(value.completed_at)) ||
    !(value.error === null || (
      isRecord(value.error) && exactFields(value.error, sessionTurnErrorResourceFields) &&
      isOneOf(sessionTurnErrorCodeResourceValues, value.error.code) && typeof value.error.message === "string"
    ))
  ) return invalid();
  return {
    id: value.id,
    object: "agent.session.turn",
    session_id: value.session_id,
    agent_id: value.agent_id,
    subagent_id: value.subagent_id,
    status: value.status,
    created_at: value.created_at,
    started_at: value.started_at,
    completed_at: value.completed_at,
    error: value.error === null ? null : { code: value.error.code as SessionTurnErrorCodeResource, message: value.error.message as string },
    usage: projectTokenUsage(value.usage, invalid),
  };
}

function nonemptyString(value: unknown): value is string {
  return typeof value === "string" && value !== "";
}

function nullableString(value: unknown): boolean {
  return value === null || typeof value === "string";
}

function projectCoordinationContent(value: unknown, invalid: () => never): ItemContent[] {
  if (!Array.isArray(value)) return invalid();
  return value.map((part) => {
    if (!isRecord(part)) return invalid();
    if (part.type === "output_text") return projectItemContent(part, invalid);
    if (part.type !== "encrypted_content" || !exactFields(part, encryptedContentResourceFields) || typeof part.encrypted_content !== "string") return invalid();
    return { type: "encrypted_content", encrypted_content: part.encrypted_content };
  });
}

export function projectHistoryPage<T extends { id: string }>(
  value: unknown,
  options: PageOptions | undefined,
  project: (entry: unknown) => T,
  invalid: () => never,
): ListPage<T> {
  // Item and Turn lists share one list envelope.
  if (!isRecord(value) || !exactFields(value, sessionItemListResourceFields) || value.object !== "list" ||
    !Array.isArray(value.data) || typeof value.has_more !== "boolean" ||
    value.data.length > (options?.limit ?? 20) || (value.has_more && value.data.length === 0)
  ) return invalid();
  const data = value.data.map(project);
  if (data.some((entry, index) =>
    (options?.after !== undefined && sameResourceId(entry.id, options.after)) ||
    data.slice(0, index).some((previous) => sameResourceId(previous.id, entry.id))
  )) return invalid();
  const first = data[0]?.id ?? null;
  const last = data[data.length - 1]?.id ?? null;
  if (value.first_id !== first || value.last_id !== last) return invalid();
  return { object: "list", data, has_more: value.has_more, first_id: first, last_id: last };
}
