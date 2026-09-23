import {
  AgentCoreError,
  isSessionDeletionConflict,
  type AgentCore,
  type AgentSession,
  type SessionDeleted,
} from "@agents-core-web/agents-client";

export interface SessionMetadataValues {
  title: string;
  metadata: string;
}

export interface SessionMetadataValidation {
  metadata?: Record<string, string>;
  metadataError?: string;
}

export type SessionActionFailureKind =
  | "not_found"
  | "lifecycle_conflict"
  | "session_busy"
  | "session_input_pending"
  | "core_unavailable"
  | "metadata_conflict"
  | "request_failed"
  | "unknown_write";

export type SessionDeleteReconciliation =
  | { state: "missing" }
  | { state: "present"; session: AgentSession }
  | { state: "unknown"; error: unknown };

export class SessionActionError extends Error {
  readonly kind: SessionActionFailureKind;

  constructor(message: string, kind: SessionActionFailureKind, options?: ErrorOptions) {
    super(message, options);
    this.name = "SessionActionError";
    this.kind = kind;
  }
}

export class SessionMetadataConflictError extends SessionActionError {
  readonly keys: string[];
  readonly latestSession?: AgentSession;

  constructor(keys: string[], latestSession?: AgentSession) {
    const sorted = [...keys].sort((left, right) => left.localeCompare(right));
    super(
      `Metadata changed in Agent Core while you were editing: ${sorted.join(", ")}. Your draft was kept; review the latest values before saving again.`,
      "metadata_conflict",
    );
    this.name = "SessionMetadataConflictError";
    this.keys = sorted;
    this.latestSession = latestSession;
  }
}

function withoutTitle(metadata: Record<string, string>): Record<string, string> {
  return Object.fromEntries(Object.entries(metadata).filter(([key]) => key !== "title"));
}

export function valuesFromMetadata(metadata: Record<string, string>): SessionMetadataValues {
  return {
    title: metadata.title ?? "",
    metadata: JSON.stringify(withoutTitle(metadata), null, 2),
  };
}

export function valuesFromSession(session: AgentSession): SessionMetadataValues {
  return valuesFromMetadata(session.metadata);
}

export function validateSessionMetadata(values: SessionMetadataValues): SessionMetadataValidation {
  let parsed: unknown;
  try {
    parsed = values.metadata.trim() ? JSON.parse(values.metadata) : {};
  } catch {
    return { metadataError: "Metadata must be valid JSON." };
  }

  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
    return { metadataError: "Metadata must be a JSON object." };
  }

  const entries = Object.entries(parsed as Record<string, unknown>);
  if (entries.some(([, value]) => typeof value !== "string")) {
    return { metadataError: "Every metadata value must be a string." };
  }
  if (entries.some(([key]) => key === "title")) {
    return { metadataError: "Edit title in the Title field, not in additional metadata." };
  }

  const metadata = Object.fromEntries(entries) as Record<string, string>;
  const title = values.title.trim();
  if (title) metadata.title = title;
  if (Object.keys(metadata).length > 16) {
    return { metadataError: "Session metadata supports at most 16 pairs, including title." };
  }
  if (Object.entries(metadata).some(([key, value]) => (
    Array.from(key).length > 64 || Array.from(value).length > 512
  ))) {
    return { metadataError: "Metadata keys must be at most 64 characters and values at most 512 characters." };
  }
  return { metadata };
}

function metadataValueEquals(
  left: Record<string, string>,
  right: Record<string, string>,
  key: string,
): boolean {
  return Object.hasOwn(left, key) === Object.hasOwn(right, key) && left[key] === right[key];
}

function metadataEquals(left: Record<string, string>, right: Record<string, string>): boolean {
  const keys = new Set([...Object.keys(left), ...Object.keys(right)]);
  return [...keys].every((key) => metadataValueEquals(left, right, key));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

function isNullableString(value: unknown): value is string | null {
  return value === null || typeof value === "string";
}

function isFiniteNonNegativeNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0;
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return isRecord(value) && Object.values(value).every((entry) => typeof entry === "string");
}

const reasoningEfforts = new Set<string>(["none", "minimal", "low", "medium", "high", "xhigh", "max"]);
const reasoningSummaries = new Set<string>(["concise", "detailed", "auto"]);
const serviceTiers = new Set<string>(["auto", "default", "flex", "priority", "fast"]);
const textVerbosities = new Set<string>(["low", "medium", "high"]);
const sessionStatuses = new Set<string>(["idle", "in_progress", "requires_action", "failed"]);

function isAgentReasoning(value: unknown): boolean {
  if (!isRecord(value)) return false;
  return (!Object.hasOwn(value, "effort") || value.effort === null || (
    typeof value.effort === "string" && reasoningEfforts.has(value.effort)
  )) && (!Object.hasOwn(value, "summary") || value.summary === null || (
    typeof value.summary === "string" && reasoningSummaries.has(value.summary)
  ));
}

function isAgentText(value: unknown): boolean {
  if (
    !isRecord(value) ||
    typeof value.verbosity !== "string" ||
    !textVerbosities.has(value.verbosity) ||
    !isRecord(value.format)
  ) return false;
  if (value.format.type === "text") return true;
  return value.format.type === "json_schema" && isRecord(value.format.schema);
}

function isAgentSnapshot(value: unknown): boolean {
  if (!isRecord(value) || !isRecord(value.multi_agent)) return false;
  const maxSubagents = value.multi_agent.max_concurrent_subagents;
  return isNonEmptyString(value.id) &&
    isNonEmptyString(value.model) &&
    isNullableString(value.name) &&
    isNullableString(value.instructions) &&
    typeof value.multi_agent.enabled === "boolean" &&
    (maxSubagents === null || isFiniteNonNegativeNumber(maxSubagents)) &&
    isAgentReasoning(value.reasoning) &&
    typeof value.service_tier === "string" && serviceTiers.has(value.service_tier) &&
    isAgentText(value.text) &&
    Array.isArray(value.tools);
}

function isAgentEnvironment(value: unknown): boolean {
  if (!isRecord(value) || !isNonEmptyString(value.type)) return false;
  if (value.type === "none") return true;
  if (value.type !== "self_hosted") return true;
  return isNonEmptyString(value.id) &&
    typeof value.remote_url === "string" &&
    typeof value.workspace_directory === "string" &&
    Array.isArray(value.capability_directories) &&
    value.capability_directories.every((directory) => typeof directory === "string");
}

function isRequiredAction(value: unknown): boolean {
  if (!isRecord(value)) return false;
  if (value.type === "environment_connection") return isNonEmptyString(value.environment_id);
  if (value.type !== "function_call") return false;
  return isNonEmptyString(value.call_id) &&
    isNonEmptyString(value.turn_id) &&
    isNonEmptyString(value.name) &&
    Object.hasOwn(value, "arguments");
}

function isTokenUsage(value: unknown): boolean {
  if (!isRecord(value) || !isRecord(value.input_tokens_details) || !isRecord(value.output_tokens_details)) {
    return false;
  }
  return isFiniteNonNegativeNumber(value.input_tokens) &&
    isFiniteNonNegativeNumber(value.output_tokens) &&
    isFiniteNonNegativeNumber(value.total_tokens) &&
    isFiniteNonNegativeNumber(value.input_tokens_details.cached_tokens) &&
    isFiniteNonNegativeNumber(value.output_tokens_details.reasoning_tokens);
}

function isCanonicalSession(value: unknown, sessionId: string): value is AgentSession {
  if (!isRecord(value)) return false;
  return value.id === sessionId &&
    value.object === "agent.session" &&
    isAgentSnapshot(value.agent) &&
    isAgentEnvironment(value.environment) &&
    typeof value.status === "string" && sessionStatuses.has(value.status) &&
    isNullableString(value.error) &&
    isStringRecord(value.metadata) &&
    Array.isArray(value.required_actions) && value.required_actions.every(isRequiredAction) &&
    Array.isArray(value.vault_ids) && value.vault_ids.every((entry) => typeof entry === "string") &&
    (value.usage === null || isTokenUsage(value.usage)) &&
    typeof value.created_at === "number" && Number.isFinite(value.created_at) &&
    typeof value.last_active_at === "number" && Number.isFinite(value.last_active_at);
}

async function retrieveCanonicalSession(core: AgentCore, sessionId: string): Promise<AgentSession> {
  const session: unknown = await core.retrieveSession(sessionId);
  if (!isCanonicalSession(session, sessionId)) {
    throw new SessionActionError(
      "Agent Core returned an invalid Session retrieval response. The Web kept its current durable view and did not send a write.",
      "request_failed",
    );
  }
  return session;
}

export function mergeSessionMetadata(
  baseline: Record<string, string>,
  draft: Record<string, string>,
  latest: Record<string, string>,
): Record<string, string> {
  const keys = new Set([...Object.keys(baseline), ...Object.keys(draft)]);
  const changed = [...keys].filter((key) => !metadataValueEquals(baseline, draft, key));
  const conflicts = changed.filter((key) => (
    !metadataValueEquals(latest, baseline, key) && !metadataValueEquals(latest, draft, key)
  ));
  if (conflicts.length) throw new SessionMetadataConflictError(conflicts);

  return rebaseSessionMetadataDraft(baseline, draft, latest);
}

export function rebaseSessionMetadataDraft(
  baseline: Record<string, string>,
  draft: Record<string, string>,
  latest: Record<string, string>,
): Record<string, string> {
  const keys = new Set([...Object.keys(baseline), ...Object.keys(draft)]);
  const changed = [...keys].filter((key) => !metadataValueEquals(baseline, draft, key));
  const merged = { ...latest };
  for (const key of changed) {
    if (Object.hasOwn(draft, key)) merged[key] = draft[key]!;
    else delete merged[key];
  }
  return merged;
}

function actionLabel(action: "update" | "delete"): string {
  return action === "update" ? "update" : "deletion";
}

function normalizeSessionActionError(
  error: unknown,
  action: "update" | "delete",
  phase: "read" | "write",
): SessionActionError {
  if (error instanceof SessionActionError) return error;
  const label = actionLabel(action);
  if (error instanceof AgentCoreError) {
    if (
      phase === "read" &&
      (error.code === "invalid_session_resource" || error.code === "invalid_session_vaults")
    ) {
      return new SessionActionError(
        "Agent Core returned an invalid Session retrieval response. The Web kept its current durable view and did not send a write.",
        "request_failed",
        { cause: error },
      );
    }
    if (error.status === 404) {
      return new SessionActionError(
        "This Session was not found in Agent Core. The Web kept its current durable view; refresh Sessions before trying again.",
        "not_found",
        { cause: error },
      );
    }
    if (action === "delete" && phase === "write" && isSessionDeletionConflict(error)) {
      return new SessionActionError(
        "Agent Core deletes a Session only when it is idle or failed without required actions. This Session still has queued, running or waiting work or pending input, so nothing was changed. Choose Cancel work and delete to cancel it, wait until it is idle and then delete it.",
        "session_busy",
        { cause: error },
      );
    }
    if (error.status === 409) {
      if (phase === "read") {
        return new SessionActionError(
          "The latest Session could not be retrieved because this compatible Core returned a lifecycle conflict (409), so no update request was sent. Your draft and current durable view were kept.",
          "lifecycle_conflict",
          { cause: error },
        );
      }
      return new SessionActionError(
        `This compatible Core rejected the Session ${label} because it conflicts with the current lifecycle state. The Web did not retry.`,
        "lifecycle_conflict",
        { cause: error },
      );
    }
    if (error.status === 503 || error.status >= 500) {
      const suffix = phase === "read"
        ? "No update request was sent."
        : "The write result is unknown. The Web kept its current durable view and did not retry; refresh before deciding whether to try again.";
      return new SessionActionError(
        `Agent Core could not complete the Session ${label} (${error.status}). ${suffix}`,
        phase === "read" ? "core_unavailable" : "unknown_write",
        { cause: error },
      );
    }
    if (phase === "write" && [408, 425, 429].includes(error.status)) {
      return new SessionActionError(
        `The Session ${label} result is uncertain after Agent Core returned ${error.status}. The Web kept its current durable view and did not retry; refresh before deciding whether to try again.`,
        "unknown_write",
        { cause: error },
      );
    }
    return new SessionActionError(
      phase === "read"
        ? `${error.message} No update request was sent; your draft and current durable view were kept.`
        : `${error.message} The Web kept its current durable view and did not retry.`,
      "request_failed",
      { cause: error },
    );
  }

  if (phase === "read") {
    return new SessionActionError(
      "The latest Session could not be retrieved, so no update request was sent. Your draft and the current durable view were kept.",
      "request_failed",
      { cause: error },
    );
  }
  return new SessionActionError(
    `The Session ${label} result is unknown because the connection ended before Core confirmed it. The Web kept its current durable view and did not retry; refresh before deciding whether to try again.`,
    "unknown_write",
    { cause: error },
  );
}

export function requestSessionDetail(core: AgentCore, sessionId: string): Promise<AgentSession> {
  return retrieveCanonicalSession(core, sessionId);
}

export async function requestSessionUpdate(
  core: AgentCore,
  sessionId: string,
  baselineMetadata: Record<string, string>,
  draftMetadata: Record<string, string>,
): Promise<AgentSession> {
  let latest: AgentSession;
  try {
    latest = await retrieveCanonicalSession(core, sessionId);
  } catch (error) {
    throw normalizeSessionActionError(error, "update", "read");
  }

  let metadata: Record<string, string>;
  try {
    metadata = mergeSessionMetadata(baselineMetadata, draftMetadata, latest.metadata);
  } catch (error) {
    if (error instanceof SessionMetadataConflictError) {
      throw new SessionMetadataConflictError(error.keys, latest);
    }
    throw error;
  }
  try {
    const updated: unknown = await core.updateSession(sessionId, metadata);
    if (!isCanonicalSession(updated, sessionId) || !metadataEquals(updated.metadata, metadata)) {
      throw new SessionActionError(
        "Agent Core returned an invalid Session update confirmation. The write result is unknown; the Web kept its current durable view and did not retry.",
        "unknown_write",
      );
    }
    return { ...latest, metadata: { ...updated.metadata } };
  } catch (error) {
    throw normalizeSessionActionError(error, "update", "write");
  }
}

/**
 * A Session that reads idle or failed, or only awaits its Environment
 * connection, yet cannot be deleted is holding input that has not started.
 * Core rejects cancellation while that input is pending.
 */
function onlyInputPending(session: AgentSession): boolean {
  if (session.status === "idle" || session.status === "failed") return true;
  return session.status === "requires_action" &&
    session.required_actions.length > 0 &&
    session.required_actions.every((action) => action.type === "environment_connection");
}

// Reads the Session once after a busy conflict so the dialog only offers
// cancellation when there is work that cancellation can stop.
async function classifyBusyDelete(core: AgentCore, sessionId: string, busy: SessionActionError): Promise<SessionActionError> {
  let latest: AgentSession;
  try {
    latest = await retrieveCanonicalSession(core, sessionId);
  } catch {
    return busy;
  }
  if (!onlyInputPending(latest)) return busy;
  return new SessionActionError(
    "Agent Core deletes a Session only when it is idle or failed without required actions. This Session has input waiting to start in its Environment, which cannot be cancelled, so nothing was changed. Delete it after that input starts, expires or fails; once it starts, cancel its work first.",
    "session_input_pending",
    { cause: busy.cause },
  );
}

export async function requestSessionDelete(core: AgentCore, sessionId: string): Promise<SessionDeleted> {
  let deleted: SessionDeleted;
  try {
    deleted = await core.deleteSession(sessionId);
  } catch (error) {
    const failure = normalizeSessionActionError(error, "delete", "write");
    throw failure.kind === "session_busy" ? await classifyBusyDelete(core, sessionId, failure) : failure;
  }
  if (
    deleted?.id !== sessionId ||
    deleted.object !== "agent.session.deleted" ||
    deleted.deleted !== true
  ) {
    throw new SessionActionError(
      "Agent Core returned an invalid deletion confirmation. The Web kept the Session; refresh durable state before taking another action.",
      "unknown_write",
    );
  }
  return deleted;
}

/**
 * Sends one explicit cancellation before a confirmed delete. It never deletes;
 * a rejected or uncertain cancellation keeps the Session.
 */
export async function requestSessionCancelBeforeDelete(
  core: AgentCore,
  sessionId: string,
  idempotencyKey: string,
): Promise<void> {
  try {
    await core.cancelTurn(sessionId, idempotencyKey);
  } catch (error) {
    if (error instanceof AgentCoreError && error.status === 404) {
      throw normalizeSessionActionError(error, "delete", "write");
    }
    const detail = error instanceof AgentCoreError
      ? `Agent Core rejected the cancellation (${error.status}): ${error.message}`
      : "The cancellation result is unknown because the connection ended before Core confirmed it.";
    throw new SessionActionError(
      `${detail} The Session was not deleted and the Web did not retry.`,
      "request_failed",
      { cause: error },
    );
  }
}

export type SessionIdleWait = "idle" | "missing" | "stale";

export interface SessionIdleWaitOptions {
  /** Stops waiting without error once the result would no longer be applied. */
  isCurrent?: () => boolean;
  intervalMs?: number;
  timeoutMs?: number;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
}

function isDeletableSession(session: AgentSession): boolean {
  return (session.status === "idle" || session.status === "failed") && session.required_actions.length === 0;
}

/**
 * Reads the Session until it is idle or failed without required actions, the
 * public state that permits deletion. The wait is bounded and never writes.
 */
export async function waitForSessionIdle(
  core: AgentCore,
  sessionId: string,
  {
    isCurrent = () => true,
    intervalMs = 1_000,
    timeoutMs = 30_000,
    now = Date.now,
    sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
  }: SessionIdleWaitOptions = {},
): Promise<SessionIdleWait> {
  const deadline = now() + timeoutMs;
  for (;;) {
    if (!isCurrent()) return "stale";
    let latest: AgentSession;
    try {
      latest = await retrieveCanonicalSession(core, sessionId);
    } catch (error) {
      if (error instanceof AgentCoreError && error.status === 404) return "missing";
      throw new SessionActionError(
        "Cancellation was requested, but the Session could not be read while waiting for it to become idle. It was not deleted.",
        "request_failed",
        { cause: error },
      );
    }
    if (!isCurrent()) return "stale";
    if (isDeletableSession(latest)) return "idle";
    if (now() >= deadline) {
      throw new SessionActionError(
        `Cancellation was requested, but the Session was still ${latest.status.replaceAll("_", " ")} after ${Math.round(timeoutMs / 1000)} seconds. It was not deleted; try again once it is idle.`,
        "session_busy",
      );
    }
    await sleep(intervalMs);
  }
}

export async function reconcileUnknownSessionDelete(
  core: AgentCore,
  sessionId: string,
): Promise<SessionDeleteReconciliation> {
  try {
    return { state: "present", session: await retrieveCanonicalSession(core, sessionId) };
  } catch (error) {
    if (error instanceof AgentCoreError && error.status === 404) return { state: "missing" };
    return { state: "unknown", error };
  }
}

export function replaceSessionMetadata(sessions: AgentSession[], updated: AgentSession): AgentSession[] {
  return sessions.map((session) => (
    session.id === updated.id ? { ...session, metadata: updated.metadata } : session
  ));
}

export function removeSession(sessions: AgentSession[], sessionId: string): AgentSession[] {
  return sessions.filter((session) => session.id !== sessionId);
}

export function selectionAfterSessionDelete(
  sessions: AgentSession[],
  selectedId: string | null,
  deletedId: string,
): string | null {
  if (selectedId !== deletedId) return selectedId;
  const deletedIndex = sessions.findIndex((session) => session.id === deletedId);
  const remaining = removeSession(sessions, deletedId);
  if (deletedIndex < 0) return remaining[0]?.id ?? null;
  return remaining[deletedIndex]?.id ?? remaining[deletedIndex - 1]?.id ?? null;
}
