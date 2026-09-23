import { exactFields, onlyFields, isRecord, hasOwn, canonicalUuid, isNonnegativeInteger, sameResourceId } from "./response-projection";
import { projectTokenUsage } from "./usage-projection";
import { projectAgentTurn, projectSessionItem, projectItemContent, projectHistoryPage, validateHistoryPageOptions } from "./history-projection";
import { projectRuntimeHistory, projectRuntimeHistoryCapabilities } from "./runtime-history-projection";
import { createSSEDecoder } from "./sse";
import { projectVaultCredentialAuth, validCredentialURL } from "./vault-credential-auth";
import type {
  AgentCore,
  AgentDeleted,
  AgentEnvironmentInput,
  AgentEnvironmentResource,
  AgentSessionEnvironmentEvent,
  EnvironmentFileList,
  EnvironmentFileListOptions,
  EnvironmentFile,
  EnvironmentFileCreateInput,
  AgentSession,
  AgentTurn,
  CreateEnvironmentTemplateInput,
  CreateVaultCredentialInput,
  CreateVaultInput,
  EnvironmentTemplate,
  EnvironmentTemplateDeleted,
  EnvironmentTemplateList,
  UpdateEnvironmentTemplateInput,
  CreateAgentInput,
  CreateSessionInput,
  CreateSessionStreamOptions,
  CoreStartupConfiguration,
  CoreHarnessKind,
  CoreManagedSandboxProvider,
  FunctionResultContent,
  FunctionResultInput,
  InputMessage,
  ListPage,
  PageOptions,
  ReadOptions,
  SavedAgent,
  SessionDeleted,
  SessionEvent,
  SessionInputEvent,
  SessionMessageInputEvent,
  SessionToolResultInputEvent,
  SessionItem,
  SourceFile,
  SourceFileContent,
  SourceFileDeleted,
  SourceFileUploadInput,
  StreamOptions,
  StreamError,
  UpdateAgentInput,
  ReplaceVaultCredentialTokenInput,
  RuntimeObservation,
  RuntimeObservationList,
  RuntimeHistory,
  RuntimeHistoryCapabilities,
  RuntimeHistoryQuery,
  Vault,
  VaultCredential,
  VaultCredentialDeleted,
  VaultCredentialList,
  VaultDeleted,
  VaultList,
  VaultListOptions,
  EnvironmentResourceStatus,
} from "./types";

export interface OpenAIAgentsClientOptions {
  baseUrl?: string;
  token?: string | (() => string | undefined);
  fetch?: typeof fetch;
}

interface APIErrorEnvelope {
  error?: {
    code?: string | null;
    message?: string;
    param?: string | null;
    type?: string;
  };
}

export class AgentCoreError extends Error {
  readonly status: number;
  readonly code?: string | null;
  readonly param?: string | null;
  readonly errorType?: string;

  constructor(
    message: string,
    status: number,
    code?: string | null,
    param?: string | null,
    errorType?: string,
  ) {
    super(message);
    this.name = "AgentCoreError";
    this.status = status;
    this.code = code;
    this.param = param;
    this.errorType = errorType;
  }
}

/**
 * A creation stream ended without events. Core sends no events on a same-key
 * retry of an existing creation; repeat the same request and Idempotency-Key
 * with `stream=false` to retrieve the Session.
 */
export class CreationStreamRetryError extends AgentCoreError {
  constructor() {
    super(
      "Agent Core already recorded this Session creation, so its stream sends no events. Repeat the same request and Idempotency-Key with stream=false to retrieve the Session.",
      409,
      "creation_stream_retry",
    );
    this.name = "CreationStreamRetryError";
  }
}

/**
 * Core deletes only a durably idle or failed Session without required actions
 * or pending input. Any other Session is rejected with HTTP 409 and code
 * `conflict_error` and left unchanged: cancel its work, wait until it is idle,
 * then delete it.
 */
export function isSessionDeletionConflict(error: unknown): error is AgentCoreError {
  return error instanceof AgentCoreError && error.status === 409 && error.code === "conflict_error";
}

function trimTrailingSlash(value: string): string {
  return value.replace(/\/+$/, "");
}

const startupConfigurationFields = new Set(["object", "schema_version", "supported", "configured"]);
const startupSupportedFields = new Set(["harnesses", "managed_sandbox_providers"]);
const startupConfiguredFields = new Set(["default_harness", "enabled_harnesses", "daemon_gateway", "self_hosted", "managed_sandbox", "model_providers"]);
const startupManagedSandboxFields = new Set(["enabled", "provider", "maintenance"]);
const startupModelProviderFields = new Set(["harness", "endpoint_configured"]);
const harnessKinds = new Set<CoreHarnessKind>(["claude_sdk", "codex", "mcode"]);
const sandboxProviders = new Set<CoreManagedSandboxProvider>(["docker", "microsandbox"]);

function isHarnessKind(value: unknown): value is CoreHarnessKind {
  return typeof value === "string" && harnessKinds.has(value as CoreHarnessKind);
}

function isSandboxProvider(value: unknown): value is CoreManagedSandboxProvider {
  return typeof value === "string" && sandboxProviders.has(value as CoreManagedSandboxProvider);
}

function sortedUnique<T extends string>(value: unknown, accept: (entry: unknown) => entry is T): value is T[] {
  return Array.isArray(value) && value.every(accept) && new Set(value).size === value.length &&
    value.every((entry, index) => index === 0 || value[index - 1]! < entry);
}

function invalidStartupConfiguration(): never {
  throw new AgentCoreError("Agent Core returned an invalid startup configuration.", 502, "invalid_startup_configuration");
}

function projectStartupConfiguration(value: unknown): CoreStartupConfiguration {
  if (!isRecord(value) || !exactFields(value, startupConfigurationFields) || value.object !== "agents.core.startup_configuration" || value.schema_version !== 1 ||
    !isRecord(value.supported) || !exactFields(value.supported, startupSupportedFields) ||
    !sortedUnique(value.supported.harnesses, isHarnessKind) || !sortedUnique(value.supported.managed_sandbox_providers, isSandboxProvider) ||
    !isRecord(value.configured) || !exactFields(value.configured, startupConfiguredFields) ||
    !isHarnessKind(value.configured.default_harness) || !sortedUnique(value.configured.enabled_harnesses, isHarnessKind) || typeof value.configured.daemon_gateway !== "boolean" ||
    typeof value.configured.self_hosted !== "boolean" || value.configured.self_hosted !== value.configured.daemon_gateway ||
    !isRecord(value.configured.managed_sandbox) || !exactFields(value.configured.managed_sandbox, startupManagedSandboxFields) ||
    typeof value.configured.managed_sandbox.enabled !== "boolean" || typeof value.configured.managed_sandbox.maintenance !== "boolean" ||
    !Array.isArray(value.configured.model_providers)) {
    return invalidStartupConfiguration();
  }
  const configured = value.configured;
  const managed = configured.managed_sandbox as Record<string, unknown>;
  const supportedHarnesses = value.supported.harnesses as CoreHarnessKind[];
  const supportedSandboxProviders = value.supported.managed_sandbox_providers as CoreManagedSandboxProvider[];
  const enabledHarnesses = configured.enabled_harnesses as CoreHarnessKind[];
  if (configured.daemon_gateway
    ? !enabledHarnesses.includes(configured.default_harness as CoreHarnessKind)
    : enabledHarnesses.length !== 0) {
    return invalidStartupConfiguration();
  }
  if (managed.enabled
    ? !isSandboxProvider(managed.provider) || !supportedSandboxProviders.includes(managed.provider) || !configured.daemon_gateway
    : managed.provider !== null || managed.maintenance) {
    return invalidStartupConfiguration();
  }
  if (enabledHarnesses.some((harness) => !supportedHarnesses.includes(harness))) {
    return invalidStartupConfiguration();
  }
  const modelProviders = configured.model_providers as unknown[];
  if (modelProviders.length !== enabledHarnesses.length || modelProviders.some((entry, index) =>
    !isRecord(entry) || !exactFields(entry, startupModelProviderFields) || entry.harness !== enabledHarnesses[index] || typeof entry.endpoint_configured !== "boolean")) {
    return invalidStartupConfiguration();
  }
  const projectedProviders = modelProviders as Array<Record<string, unknown>>;
  return {
    object: "agents.core.startup_configuration",
    schema_version: 1,
    supported: {
      harnesses: [...supportedHarnesses],
      managed_sandbox_providers: [...supportedSandboxProviders],
    },
    configured: {
      default_harness: configured.default_harness as CoreHarnessKind,
      enabled_harnesses: [...enabledHarnesses],
      daemon_gateway: configured.daemon_gateway as boolean,
      self_hosted: configured.self_hosted as boolean,
      managed_sandbox: { enabled: managed.enabled as boolean, provider: managed.provider as CoreManagedSandboxProvider | null, maintenance: managed.maintenance as boolean },
      model_providers: projectedProviders.map((entry) => ({ harness: entry.harness as CoreHarnessKind, endpoint_configured: entry.endpoint_configured as boolean })),
    },
  };
}

export function createIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function requireIdempotencyKey(value: unknown): asserts value is string {
  if (
    typeof value !== "string" ||
    goWhitespaceOnlyPattern.test(value) ||
    new TextEncoder().encode(value).length > 128
  ) {
    throw new TypeError("Idempotency key must be non-blank and at most 128 UTF-8 bytes.");
  }
}

function addPageOptions(params: URLSearchParams, options?: PageOptions): void {
  if (options?.after) params.set("after", options.after);
  if (options?.limit !== undefined) params.set("limit", String(options.limit));
  if (options?.order) params.set("order", options.order);
}

function addVaultPageOptions(params: URLSearchParams, options?: VaultListOptions): void {
  if (options?.limit !== undefined && (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100)) {
    throw new TypeError("Vault list limit must be an integer from 1 through 100.");
  }
  if (options?.order !== undefined && options.order !== "asc" && options.order !== "desc") {
    throw new TypeError("Vault list order must be asc or desc.");
  }
  addPageOptions(params, options);
  if (options?.status === undefined) return;
  const statuses = Array.isArray(options.status) ? options.status : [options.status];
  if (statuses.length === 0 || statuses.some((status) => status !== "active" && status !== "archived")) {
    throw new TypeError("Vault status must be active, archived, or a non-empty array of those values.");
  }
  if (Array.isArray(options.status)) {
    statuses.forEach((status) => params.append("status[]", status));
  } else {
    params.set("status", options.status);
  }
}

function withQuery(path: string, params: URLSearchParams): string {
  const query = params.toString();
  return query ? `${path}?${query}` : path;
}

const environmentResourceFields = new Set(["id", "object", "type", "status", "files", "plugins", "skills"]);
const environmentFileFields = new Set(["environment_id", "object", "path", "size_bytes"]);
const environmentFileListFields = new Set(["object", "data", "next", "has_more"]);
const sourceFileFields = new Set([
  "id", "object", "bytes", "created_at", "filename", "purpose", "status", "expires_at", "status_details",
]);
const sourceFileDeletedFields = new Set(["id", "object", "deleted"]);
const canonicalUuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const sourceFileIdPattern = /^file-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const maxSourceFileBytes = 512 * 1024 * 1024;
const maxEnvironmentFileBytes = 50 * 1024 * 1024;
const environmentTemplateFields = new Set([
  "id", "object", "name", "network", "capability_directories", "packages",
  "files", "plugins", "skills", "created_at", "updated_at",
]);
const environmentTemplateDeletedFields = new Set(["id", "object", "deleted"]);
const vaultFields = new Set(["id", "object", "created_at", "name", "metadata"]);
const vaultListFields = new Set(["object", "data", "has_more", "first_id", "last_id"]);
const vaultDeletedFields = new Set(["id", "object", "deleted"]);
const vaultCredentialFields = new Set(["id", "vault_id", "name", "object", "auth", "created_at", "updated_at"]);
const vaultCredentialDeletedFields = new Set(["id", "object", "deleted"]);
const sessionFields = new Set([
  "id", "object", "agent", "environment", "status", "error", "metadata",
  "required_actions", "vault_ids", "usage", "created_at", "last_active_at",
]);
const hostedSessionEnvironmentFields = new Set([
  "type", "id", "capability_directories", "network", "packages", "files", "plugins", "skills",
]);
const environmentNetworkFields = new Set(["access", "allowed_domains"]);
const environmentPackagesFields = new Set(["npm", "python", "system"]);
const agentSnapshotFields = new Set([
  "id", "model", "name", "instructions", "multi_agent", "reasoning",
  "service_tier", "text", "tools",
]);
const agentSnapshotAcceptedFields = new Set([...agentSnapshotFields, "x_agents_core"]);
const multiAgentFields = new Set(["enabled", "max_concurrent_subagents"]);
const reasoningFields = new Set(["effort", "summary"]);
const textFields = new Set(["format", "verbosity"]);
const sessionStatuses = new Set(["idle", "in_progress", "requires_action", "failed"]);
const serviceTiers = new Set(["auto", "default", "flex", "priority", "fast"]);
const reasoningEfforts = new Set(["none", "minimal", "low", "medium", "high", "xhigh", "max"]);
const reasoningSummaries = new Set(["concise", "detailed", "auto"]);
const sessionMessageEventFields = new Set(["type", "input"]);
const sessionCancelEventFields = new Set(["type"]);
const sessionToolResultEventFields = new Set(["type", "call_id", "turn_id", "success", "output", "error"]);
const inputMessageFields = new Set(["type", "role", "content"]);
const inputTextFields = new Set(["type", "text"]);
const inputImageFields = new Set(["type", "image_url"]);
const maxSessionInputEvents = 64;
const maxSessionInputRequestBytes = 1024 * 1024;
const goWhitespaceOnlyPattern = /^[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/u;
const streamErrorFields = new Set(["code", "type", "message"]);
const environmentStateFields = new Set(["id", "type", "status", "error"]);
const snapshotEventFields = new Set(["type", "event_id", "session_id", "session"]);
const turnEventFields = new Set(["type", "event_id", "session_id", "turn_id", "turn"]);
// Terminal Turn events also carry top-level usage mirroring the Turn snapshot.
const terminalTurnEventFields = new Set([...turnEventFields, "usage"]);
const terminalTurnEventTypes = new Set([
  "agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled",
]);
const itemEventFields = new Set(["type", "event_id", "session_id", "turn_id", "item_id", "output_index", "item"]);
const contentPartEventFields = new Set([
  "type", "event_id", "session_id", "turn_id", "item_id", "output_index", "content_index", "part",
]);
const outputTextEventBaseFields = [
  "type", "event_id", "session_id", "turn_id", "item_id", "output_index", "content_index",
] as const;
const commandDeltaEventFields = new Set([
  "type", "event_id", "session_id", "turn_id", "item_id", "output_index", "delta",
]);
const environmentEventFields = new Set(["type", "event_id", "session_id", "environment"]);
const errorEventFields = new Set(["type", "event_id", "session_id", "error"]);
const unsafeUnknownEventFields = new Set([
  "session", "turn", "turn_id", "item", "item_id", "output_index", "content_index", "part", "delta", "text", "error", "environment",
]);
const runtimeObservationFields = new Set([
  "id", "object", "session_id", "environment_id", "mode", "provider_type", "instance", "status", "reason",
  "allocation_created_at", "resolved_at", "observed_at", "started_at", "cpu", "memory",
]);
const runtimeInstanceFields = new Set(["kind", "allocation_id", "device_id", "connection_generation"]);
const runtimeCPUFields = new Set(["usage_seconds_total", "capacity_cores", "usage_cores", "utilization_ratio"]);
const runtimeMemoryFields = new Set(["usage_bytes", "limit_bytes"]);
const runtimeObservationReasons = new Set([
  "runtime_mode_not_observable", "allocation_pending", "runtime_not_running",
  "source_not_configured", "sample_timeout", "sample_unavailable",
]);
const runtimeProviderTypePattern = /^[a-z][a-z0-9_]{0,31}$/;
function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}

function invalidSessionInputBatch(message = "Invalid Session input event batch."): never {
  throw new TypeError(message);
}

function canonicalInputMessage(value: unknown): InputMessage {
  if (
    !isRecord(value) || !onlyFields(value, inputMessageFields) ||
    !hasOwn(value, "role") || !hasOwn(value, "content") ||
    value.role !== "user" || !Array.isArray(value.content) || value.content.length === 0 ||
    (hasOwn(value, "type") && value.type !== "message")
  ) {
    return invalidSessionInputBatch();
  }
  let text = "";
  const content = Array.from(value.content, (part) => {
    if (
      !isRecord(part) || !exactFields(part, inputTextFields) ||
      part.type !== "input_text" || typeof part.text !== "string"
    ) {
      return invalidSessionInputBatch();
    }
    text += part.text;
    return { type: "input_text" as const, text: part.text };
  });
  if (goWhitespaceOnlyPattern.test(text)) return invalidSessionInputBatch();
  return hasOwn(value, "type")
    ? { type: "message", role: "user", content }
    : { role: "user", content };
}

function canonicalFunctionOutput(value: unknown): string | FunctionResultContent[] | null {
  if (value === null || typeof value === "string") return value;
  if (!Array.isArray(value)) return invalidSessionInputBatch();
  return Array.from(value, (part) => {
    if (!isRecord(part)) return invalidSessionInputBatch();
    if (part.type === "input_text") {
      if (!exactFields(part, inputTextFields) || typeof part.text !== "string") {
        return invalidSessionInputBatch();
      }
      return { type: "input_text" as const, text: part.text };
    }
    if (part.type === "input_image") {
      if (!exactFields(part, inputImageFields) || typeof part.image_url !== "string") {
        return invalidSessionInputBatch();
      }
      return { type: "input_image" as const, image_url: part.image_url };
    }
    return invalidSessionInputBatch();
  });
}

function canonicalSessionInputEvent(value: unknown): SessionInputEvent {
  if (!isRecord(value) || !hasOwn(value, "type") || typeof value.type !== "string") {
    return invalidSessionInputBatch();
  }
  switch (value.type) {
    case "agent.session.input.message": {
      if (!exactFields(value, sessionMessageEventFields) || !Array.isArray(value.input) || value.input.length === 0) {
        return invalidSessionInputBatch();
      }
      return {
        type: "agent.session.input.message",
        input: Array.from(value.input, canonicalInputMessage),
      } satisfies SessionMessageInputEvent;
    }
    case "agent.session.input.cancel":
      if (!exactFields(value, sessionCancelEventFields)) return invalidSessionInputBatch();
      return { type: "agent.session.input.cancel" };
    case "agent.session.input.tool_result": {
      if (
        !onlyFields(value, sessionToolResultEventFields) ||
        !hasOwn(value, "call_id") || !hasOwn(value, "turn_id") || !hasOwn(value, "success") ||
        typeof value.call_id !== "string" || value.call_id.length === 0 ||
        typeof value.turn_id !== "string" || value.turn_id.length === 0 ||
        typeof value.success !== "boolean" ||
        (hasOwn(value, "error") && value.error !== null && typeof value.error !== "string")
      ) {
        return invalidSessionInputBatch();
      }
      const event: SessionToolResultInputEvent = {
        type: "agent.session.input.tool_result",
        call_id: value.call_id,
        turn_id: value.turn_id,
        success: value.success,
      };
      if (hasOwn(value, "output")) event.output = canonicalFunctionOutput(value.output);
      if (hasOwn(value, "error")) event.error = value.error as string | null;
      return event;
    }
    default:
      return invalidSessionInputBatch();
  }
}

function encodeSessionInputBatch(events: readonly SessionInputEvent[]): string {
  if (!Array.isArray(events) || events.length > maxSessionInputEvents) {
    return invalidSessionInputBatch("Session input event batch must contain at most 64 events.");
  }
  const body = JSON.stringify({ events: Array.from(events, canonicalSessionInputEvent) });
  // Core's HTTP handler bounds the complete wire request at 1 MiB. Its separate
  // 512 KiB internal limit is measured after Go decodes, reshapes and canonically
  // re-encodes individual payloads, so approximating it from browser JSON would
  // reject valid wire requests (notably around Go's HTML and U+2028 escaping).
  if (utf8Length(body) > maxSessionInputRequestBytes) {
    return invalidSessionInputBatch("Session input event request exceeds 1 MiB.");
  }
  return body;
}

function isTrimmedName(value: unknown): value is string {
  return typeof value === "string" && value === value.trim() && utf8Length(value) >= 1 && utf8Length(value) <= 256;
}

function validMetadata(value: unknown): value is Record<string, string> {
  return isRecord(value) && Object.values(value).every((entry) => typeof entry === "string");
}

function sameUuid(value: unknown, expected: string): boolean {
  const actual = canonicalUuid(value);
  const canonicalExpected = canonicalUuid(expected);
  return actual !== null && canonicalExpected !== null && actual === canonicalExpected;
}

function sameMetadata(left: Record<string, string>, right: Record<string, string>): boolean {
  const leftEntries = Object.entries(left);
  return leftEntries.length === Object.keys(right).length && leftEntries.every(([key, value]) => right[key] === value);
}

function invalidVaultResponse(code: string, message: string): never {
  throw new AgentCoreError(message, 502, code);
}

function projectVault(value: unknown, expectedId?: string): Vault {
  if (!isRecord(value) || !exactFields(value, vaultFields)) {
    return invalidVaultResponse("invalid_vault_resource", "Agent Core returned an invalid Vault resource.");
  }
  if (
    typeof value.id !== "string" || !canonicalUuidPattern.test(value.id) ||
    (expectedId !== undefined && !sameUuid(value.id, expectedId)) ||
    value.object !== "vault" ||
    !Number.isSafeInteger(value.created_at) || Number(value.created_at) < 0 ||
    !(value.name === null || isTrimmedName(value.name)) ||
    !validMetadata(value.metadata)
  ) {
    return invalidVaultResponse("invalid_vault_resource", "Agent Core returned an invalid Vault resource.");
  }
  return {
    id: value.id,
    object: "vault",
    created_at: Number(value.created_at),
    name: value.name,
    metadata: { ...value.metadata },
  };
}

function projectVaultCredential(
  value: unknown,
  expectedVaultId: string,
  expectedCredentialId?: string,
): VaultCredential {
  if (!isRecord(value) || !exactFields(value, vaultCredentialFields) || !isRecord(value.auth)) {
    return invalidVaultResponse("invalid_vault_credential", "Agent Core returned invalid Credential metadata.");
  }
  if (
    typeof value.id !== "string" || !canonicalUuidPattern.test(value.id) ||
    (expectedCredentialId !== undefined && !sameUuid(value.id, expectedCredentialId)) ||
    !sameUuid(value.vault_id, expectedVaultId) ||
    value.object !== "vault.credential" || !isTrimmedName(value.name) ||
    !Number.isSafeInteger(value.created_at) || Number(value.created_at) < 0 ||
    !Number.isSafeInteger(value.updated_at) || Number(value.updated_at) < Number(value.created_at)
  ) {
    return invalidVaultResponse("invalid_vault_credential", "Agent Core returned invalid Credential metadata.");
  }
  const auth = projectVaultCredentialAuth(value.auth);
  if (!auth) return invalidVaultResponse("invalid_vault_credential", "Agent Core returned invalid Credential metadata.");
  return {
    id: value.id,
    vault_id: value.vault_id as string,
    name: value.name,
    object: "vault.credential",
    auth,
    created_at: Number(value.created_at),
    updated_at: Number(value.updated_at),
  };
}

function compareCreatedResource(
  left: { id: string; created_at: number },
  right: { id: string; created_at: number },
  order: "asc" | "desc",
): number {
  const direction = order === "asc" ? 1 : -1;
  return (left.created_at - right.created_at) * direction;
}

function projectVaultPage<T extends { id: string; created_at: number }>(
  value: unknown,
  options: VaultListOptions | undefined,
  project: (entry: unknown) => T,
  code: string,
  message: string,
): { object: "list"; data: T[]; has_more: boolean; first_id: string | null; last_id: string | null } {
  if (!isRecord(value) || !exactFields(value, vaultListFields) || value.object !== "list" || !Array.isArray(value.data) || typeof value.has_more !== "boolean") {
    return invalidVaultResponse(code, message);
  }
  const limit = options?.limit ?? 20;
  const order = options?.order ?? "desc";
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100 || (order !== "asc" && order !== "desc") || value.data.length > limit) {
    return invalidVaultResponse(code, message);
  }
  const data = value.data.map(project);
  const ids = new Set(data.map((entry) => entry.id));
  const firstId = data[0]?.id ?? null;
  const lastId = data[data.length - 1]?.id ?? null;
  if (
    ids.size !== data.length || value.first_id !== firstId || value.last_id !== lastId ||
    (value.has_more && data.length === 0) ||
    // Core paginates with a higher-precision database timestamp, but the public
    // contract exposes whole seconds. Equal public timestamps therefore cannot
    // prove the private UUID tie-break order and must remain acceptable.
    data.some((entry, index) => index > 0 && compareCreatedResource(data[index - 1]!, entry, order) > 0)
  ) {
    return invalidVaultResponse(code, message);
  }
  return { object: "list", data, has_more: value.has_more, first_id: firstId, last_id: lastId };
}

function projectVaultList(value: unknown, options?: VaultListOptions): VaultList {
  return projectVaultPage(value, options, (entry) => projectVault(entry), "invalid_vault_list", "Agent Core returned an invalid Vault list.");
}

function projectVaultCredentialList(value: unknown, vaultId: string, options?: VaultListOptions): VaultCredentialList {
  return projectVaultPage(
    value,
    options,
    (entry) => projectVaultCredential(entry, vaultId),
    "invalid_vault_credential_list",
    "Agent Core returned an invalid Credential list.",
  );
}

function projectDeletedResource<T extends VaultDeleted | VaultCredentialDeleted>(
  value: unknown,
  expectedId: string,
  object: T["object"],
  fields: Set<string>,
  code: string,
): T {
  if (!isRecord(value) || !exactFields(value, fields) || !sameUuid(value.id, expectedId) || value.object !== object || value.deleted !== true) {
    return invalidVaultResponse(code, "Agent Core returned an invalid deletion receipt.");
  }
  return { id: value.id as string, object, deleted: true } as T;
}

function invalidEnvironmentTemplate(message = "Agent Core returned an invalid Environment Template."): never {
  throw new AgentCoreError(message, 502, "invalid_environment_template");
}

/** Core preserves a Template name verbatim within a 1–256 Unicode character bound. */
function isEnvironmentTemplateName(value: unknown): value is string {
  if (typeof value !== "string") return false;
  const characters = [...value].length;
  return characters >= 1 && characters <= 256;
}

function isEmptyArray(value: unknown): boolean {
  return Array.isArray(value) && value.length === 0;
}

function projectEnvironmentTemplate(value: unknown, expectedId?: string): EnvironmentTemplate {
  if (!isRecord(value) || !exactFields(value, environmentTemplateFields)) {
    return invalidEnvironmentTemplate();
  }
  const network = value.network;
  const packages = value.packages;
  if (
    typeof value.id !== "string" || !canonicalUuidPattern.test(value.id) ||
    (expectedId !== undefined && !sameUuid(value.id, expectedId)) ||
    value.object !== "agent.environment.template" ||
    !(value.name === null || isEnvironmentTemplateName(value.name)) ||
    !isRecord(network) || !exactFields(network, environmentNetworkFields) ||
    (network.access !== "enabled" && network.access !== "disabled") ||
    !isEmptyArray(network.allowed_domains) ||
    !isEmptyArray(value.capability_directories) ||
    !isRecord(packages) || !exactFields(packages, environmentPackagesFields) ||
    !isEmptyArray(packages.npm) || !isEmptyArray(packages.python) || !isEmptyArray(packages.system) ||
    !isEmptyArray(value.files) || !isEmptyArray(value.plugins) || !isEmptyArray(value.skills) ||
    !isNonnegativeInteger(value.created_at) ||
    !isNonnegativeInteger(value.updated_at) || Number(value.updated_at) < Number(value.created_at)
  ) {
    return invalidEnvironmentTemplate();
  }
  return {
    id: value.id,
    object: "agent.environment.template",
    name: value.name as string | null,
    network: { access: network.access, allowed_domains: [] },
    capability_directories: [],
    packages: { npm: [], python: [], system: [] },
    files: [],
    plugins: [],
    skills: [],
    created_at: Number(value.created_at),
    updated_at: Number(value.updated_at),
  };
}

function projectEnvironmentTemplateList(
  value: unknown,
  options?: PageOptions,
): EnvironmentTemplateList {
  return projectVaultPage(
    value,
    options,
    (entry) => projectEnvironmentTemplate(entry),
    "invalid_environment_template_list",
    "Agent Core returned an invalid Environment Template list.",
  );
}

function environmentTemplateRequestBody(
  input: CreateEnvironmentTemplateInput | UpdateEnvironmentTemplateInput,
): string {
  if (!isRecord(input) || !onlyFields(input, new Set(["name", "network"]))) {
    throw new TypeError("Environment Template requests accept only name and network.");
  }
  if (hasOwn(input, "name") && !(input.name === null || isEnvironmentTemplateName(input.name))) {
    throw new TypeError("An Environment Template name must be null or 1 through 256 characters.");
  }
  if (
    hasOwn(input, "network") && !(input.network === null || (
      isRecord(input.network) && exactFields(input.network, new Set(["access"])) &&
      (input.network.access === "enabled" || input.network.access === "disabled")
    ))
  ) {
    throw new TypeError("An Environment Template network accepts only enabled or disabled access.");
  }
  return JSON.stringify(input);
}

function invalidSessionResource(message = "Agent Core returned an invalid Session resource."): never {
  throw new AgentCoreError(message, 502, "invalid_session_resource");
}

function projectAgentSnapshot(value: unknown): AgentSession["agent"] {
  if (
    !isRecord(value) || !onlyFields(value, agentSnapshotAcceptedFields) ||
    [...agentSnapshotFields].some((field) => !hasOwn(value, field))
  ) return invalidSessionResource();
  const agentsCore = value.x_agents_core;
  const multiAgent = value.multi_agent;
  const reasoning = value.reasoning;
  const text = value.text;
  if (
    typeof value.id !== "string" || value.id.trim() === "" ||
    typeof value.model !== "string" || value.model.trim() === "" ||
    !(value.name === null || typeof value.name === "string") ||
    !(value.instructions === null || typeof value.instructions === "string") ||
    !(agentsCore === undefined || agentsCore === null || (
      isRecord(agentsCore) && exactFields(agentsCore, new Set(["harness"])) && isHarnessKind(agentsCore.harness)
    )) ||
    !isRecord(multiAgent) || !exactFields(multiAgent, multiAgentFields) ||
    typeof multiAgent.enabled !== "boolean" ||
    !(multiAgent.max_concurrent_subagents === null ||
      (Number.isSafeInteger(multiAgent.max_concurrent_subagents) && Number(multiAgent.max_concurrent_subagents) > 0)) ||
    !isRecord(reasoning) || !onlyFields(reasoning, reasoningFields) ||
    !(reasoning.effort === undefined || reasoning.effort === null || reasoningEfforts.has(String(reasoning.effort))) ||
    !(reasoning.summary === undefined || reasoning.summary === null || reasoningSummaries.has(String(reasoning.summary))) ||
    !serviceTiers.has(String(value.service_tier)) ||
    !isRecord(text) || !exactFields(text, textFields) ||
    (text.verbosity !== "low" && text.verbosity !== "medium" && text.verbosity !== "high") ||
    !isRecord(text.format) || !Array.isArray(value.tools)
  ) return invalidSessionResource();

  const format = text.format;
  if (
    (format.type === "text" && !exactFields(format, new Set(["type"]))) ||
    (format.type === "json_schema" &&
      (!exactFields(format, new Set(["type", "schema"])) || !isRecord(format.schema))) ||
    (format.type !== "text" && format.type !== "json_schema")
  ) return invalidSessionResource();

  return {
    id: value.id,
    ...(agentsCore === undefined
      ? {}
      : { x_agents_core: agentsCore === null ? null : { harness: agentsCore.harness as CoreHarnessKind } }),
    model: value.model,
    name: value.name,
    instructions: value.instructions,
    multi_agent: {
      enabled: multiAgent.enabled,
      max_concurrent_subagents: multiAgent.max_concurrent_subagents as number | null,
    },
    reasoning: { ...reasoning } as AgentSession["agent"]["reasoning"],
    service_tier: value.service_tier as AgentSession["agent"]["service_tier"],
    text: {
      format: format.type === "text"
        ? { type: "text" }
        : { type: "json_schema", schema: { ...(format.schema as Record<string, unknown>) } },
      verbosity: text.verbosity,
    },
    tools: [...value.tools],
  };
}

function projectSessionEnvironment(value: unknown): AgentSession["environment"] {
  if (!isRecord(value) || typeof value.type !== "string" || value.type.trim() === "") {
    return invalidSessionResource();
  }
  if (value.type === "none") {
    if (!exactFields(value, new Set(["type"]))) return invalidSessionResource();
    return { type: "none" };
  }
  if (value.type === "self_hosted") {
    if (
      !exactFields(value, new Set(["type", "id", "remote_url", "workspace_directory", "capability_directories"])) ||
      typeof value.id !== "string" || value.id.trim() === "" ||
      typeof value.remote_url !== "string" || value.remote_url.trim() === "" ||
      typeof value.workspace_directory !== "string" || value.workspace_directory.trim() === "" ||
      !Array.isArray(value.capability_directories) ||
      value.capability_directories.some((directory) => typeof directory !== "string")
    ) return invalidSessionResource();
    return {
      type: "self_hosted",
      id: value.id,
      remote_url: value.remote_url,
      workspace_directory: value.workspace_directory,
      capability_directories: [...value.capability_directories] as string[],
    };
  }
  if (value.type === "openai_hosted") {
    const network = value.network;
    const packages = value.packages;
    if (
      !exactFields(value, hostedSessionEnvironmentFields) ||
      typeof value.id !== "string" || value.id.trim() === "" ||
      !Array.isArray(value.capability_directories) || value.capability_directories.length !== 0 ||
      !isRecord(network) || !exactFields(network, environmentNetworkFields) ||
      (network.access !== "enabled" && network.access !== "disabled") ||
      !Array.isArray(network.allowed_domains) || network.allowed_domains.length !== 0 ||
      !isRecord(packages) || !exactFields(packages, environmentPackagesFields) ||
      !Array.isArray(packages.npm) || packages.npm.length !== 0 ||
      !Array.isArray(packages.python) || packages.python.length !== 0 ||
      !Array.isArray(packages.system) || packages.system.length !== 0 ||
      !Array.isArray(value.files) || value.files.length !== 0 ||
      !Array.isArray(value.plugins) || value.plugins.length !== 0 ||
      !Array.isArray(value.skills) || value.skills.length !== 0
    ) return invalidSessionResource();
    return {
      type: "openai_hosted",
      id: value.id,
      capability_directories: [],
      network: { access: network.access, allowed_domains: [] },
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
    };
  }
  return { ...value } as AgentSession["environment"];
}

type ExpectedCreationEnvironment =
  | { type: "none" }
  | { type: "self_hosted"; workspaceDirectory: string; capabilityDirectories: string[] }
  /** A null access means the request inherited an unread Template policy. */
  | { type: "openai_hosted"; networkAccess: "enabled" | "disabled" | null };

interface ImmutableSessionProjection {
  agent: AgentSession["agent"];
  environment: AgentSession["environment"];
  vaultIds: string[];
}

function normalizeCreationEnvironment(value: AgentEnvironmentInput): ExpectedCreationEnvironment {
  if (!isRecord(value) || typeof value.type !== "string") {
    throw new TypeError("Session creation requires a supported Environment.");
  }
  if (value.type === "none") {
    if (!exactFields(value, new Set(["type"]))) {
      throw new TypeError("The none Environment accepts no additional fields.");
    }
    return { type: "none" };
  }
  if (value.type === "self_hosted") {
    if (
      !onlyFields(value, new Set(["type", "workspace_directory", "capability_directories"])) ||
      !hasOwn(value, "workspace_directory") ||
      typeof value.workspace_directory !== "string" || value.workspace_directory.trim() === "" ||
      !(value.capability_directories === undefined || value.capability_directories === null ||
        (Array.isArray(value.capability_directories) &&
          value.capability_directories.every((directory) => typeof directory === "string")))
    ) {
      throw new TypeError("The self_hosted Environment requires its exact workspace configuration.");
    }
    return {
      type: "self_hosted",
      workspaceDirectory: value.workspace_directory,
      capabilityDirectories: value.capability_directories === undefined || value.capability_directories === null
        ? []
        : [...value.capability_directories],
    };
  }
  if (value.type === "openai_hosted") {
    if (
      !onlyFields(value, new Set(["type", "network", "environment_template_id"])) ||
      !(value.network === undefined || value.network === null || (
        isRecord(value.network) && exactFields(value.network, new Set(["access"])) &&
        (value.network.access === "enabled" || value.network.access === "disabled")
      ))
    ) {
      throw new TypeError("The openai_hosted Environment accepts only its supported network mode.");
    }
    if (!hasOwn(value, "environment_template_id")) {
      return { type: "openai_hosted", networkAccess: (value.network?.access as "enabled" | "disabled" | undefined) ?? "enabled" };
    }
    if (typeof value.environment_template_id !== "string" || !canonicalUuidPattern.test(value.environment_template_id)) {
      throw new TypeError("A referenced Environment Template requires a canonical template ID.");
    }
    // Core rejects an explicit null network beside a reference instead of
    // guessing inheritance, so never send that combination.
    if (hasOwn(value, "network") && value.network === null) {
      throw new TypeError("A referenced Environment Template rejects an explicit null network.");
    }
    return {
      type: "openai_hosted",
      networkAccess: (value.network?.access as "enabled" | "disabled" | undefined) ?? null,
    };
  }
  throw new TypeError("Session creation requires a supported Environment.");
}

function bindCreatedEnvironment(
  environment: AgentSession["environment"],
  expected: ExpectedCreationEnvironment,
): void {
  if (environment.type !== expected.type) {
    return invalidSessionResource("Agent Core returned a different Environment type than the creation request.");
  }
  if (
    expected.type === "self_hosted" && environment.type === "self_hosted" &&
    ((environment as Extract<AgentSession["environment"], { type: "self_hosted" }>).workspace_directory !== expected.workspaceDirectory ||
      !sameStringArray(
        (environment as Extract<AgentSession["environment"], { type: "self_hosted" }>).capability_directories,
        expected.capabilityDirectories,
      ))
  ) {
    return invalidSessionResource("Agent Core returned a different self-hosted Environment configuration.");
  }
  if (
    expected.type === "openai_hosted" && environment.type === "openai_hosted" &&
    expected.networkAccess !== null &&
    (environment as Extract<AgentSession["environment"], { type: "openai_hosted" }>).network.access !== expected.networkAccess
  ) {
    return invalidSessionResource("Agent Core returned a different managed Environment network mode.");
  }
}

function sameStringArray(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function sameJSONValue(left: unknown, right: unknown): boolean {
  if (Object.is(left, right)) return true;
  if (Array.isArray(left) || Array.isArray(right)) {
    return Array.isArray(left) && Array.isArray(right) &&
      left.length === right.length && left.every((value, index) => sameJSONValue(value, right[index]));
  }
  if (!isRecord(left) || !isRecord(right)) return false;
  const leftKeys = Object.keys(left).sort();
  const rightKeys = Object.keys(right).sort();
  return sameStringArray(leftKeys, rightKeys) && leftKeys.every((key) => sameJSONValue(left[key], right[key]));
}

function immutableSessionProjection(session: AgentSession): ImmutableSessionProjection {
  return {
    agent: session.agent,
    environment: session.environment,
    vaultIds: session.vault_ids,
  };
}

function matchesImmutableSession(
  session: AgentSession,
  expected: ImmutableSessionProjection,
): boolean {
  return sameJSONValue(session.agent, expected.agent) &&
    sameJSONValue(session.environment, expected.environment) &&
    sameStringArray(session.vault_ids, expected.vaultIds);
}

function projectRequiredActions(value: unknown): AgentSession["required_actions"] {
  if (!Array.isArray(value)) return invalidSessionResource();
  return value.map((entry) => {
    if (!isRecord(entry) || typeof entry.type !== "string") return invalidSessionResource();
    if (entry.type === "function_call") {
      if (
        !exactFields(entry, new Set(["type", "call_id", "turn_id", "name", "arguments"])) ||
        typeof entry.call_id !== "string" || entry.call_id === "" ||
        typeof entry.turn_id !== "string" || entry.turn_id === "" ||
        typeof entry.name !== "string" || entry.name === ""
      ) return invalidSessionResource();
      return {
        type: "function_call" as const,
        call_id: entry.call_id,
        turn_id: entry.turn_id,
        name: entry.name,
        arguments: entry.arguments,
      };
    }
    if (entry.type === "environment_connection") {
      if (
        !exactFields(entry, new Set(["type", "environment_id"])) ||
        typeof entry.environment_id !== "string" || entry.environment_id === ""
      ) return invalidSessionResource();
      return { type: "environment_connection" as const, environment_id: entry.environment_id };
    }
    return invalidSessionResource();
  });
}

function projectAgentSession(
  value: unknown,
  expectedVaultIds?: string[],
  expectedSessionId?: string,
  expectedEnvironment?: ExpectedCreationEnvironment,
  expectedImmutable?: ImmutableSessionProjection,
): AgentSession {
  if (!isRecord(value) || !exactFields(value, sessionFields)) return invalidSessionResource();
  if (
    typeof value.id !== "string" || value.id.trim() === "" ||
    (expectedSessionId !== undefined && !sameResourceId(value.id, expectedSessionId)) ||
    value.object !== "agent.session" ||
    !sessionStatuses.has(String(value.status)) ||
    !(value.error === null || typeof value.error === "string") ||
    !validMetadata(value.metadata) ||
    !isNonnegativeInteger(value.created_at) ||
    !isNonnegativeInteger(value.last_active_at) ||
    value.last_active_at < value.created_at
  ) return invalidSessionResource();

  if (!Array.isArray(value.vault_ids)) {
    return invalidVaultResponse("invalid_session_vaults", "Agent Core returned invalid Session Vault attachments.");
  }
  const vaultIds = value.vault_ids;
  if (
    vaultIds.some((id) => canonicalUuid(id) === null) ||
    (expectedVaultIds !== undefined &&
      (expectedVaultIds.length !== vaultIds.length || expectedVaultIds.some((id, index) => id !== vaultIds[index])))
  ) {
    return invalidVaultResponse("invalid_session_vaults", "Agent Core returned invalid Session Vault attachments.");
  }

  const environment = projectSessionEnvironment(value.environment);
  const requiredActions = projectRequiredActions(value.required_actions);
  const requiresAction = value.status === "requires_action";
  if (requiresAction !== (requiredActions.length > 0)) {
    return invalidSessionResource("Agent Core returned Session actions inconsistent with its status.");
  }
  const environmentConnections = requiredActions.filter((action) => action.type === "environment_connection");
  if (environmentConnections.length > 0) {
    const environmentId = environment.type === "self_hosted" &&
      "id" in environment && typeof environment.id === "string"
      ? environment.id
      : null;
    if (
      environmentId === null ||
      requiredActions.length !== 1 ||
      environmentConnections.length !== 1
    ) return invalidSessionResource("Agent Core returned an invalid Environment connection action.");
    if (!sameResourceId(environmentConnections[0]!.environment_id, environmentId)) {
      return invalidSessionResource("Agent Core returned an invalid Environment connection action.");
    }
  }

  const session: AgentSession = {
    id: value.id,
    object: "agent.session",
    agent: projectAgentSnapshot(value.agent),
    environment,
    status: value.status as AgentSession["status"],
    error: value.error,
    metadata: { ...value.metadata },
    required_actions: requiredActions,
    vault_ids: [...vaultIds] as string[],
    usage: projectTokenUsage(value.usage, invalidSessionResource),
    created_at: value.created_at,
    last_active_at: value.last_active_at,
  };
  if (expectedEnvironment !== undefined) bindCreatedEnvironment(session.environment, expectedEnvironment);
  if (expectedImmutable !== undefined && !matchesImmutableSession(session, expectedImmutable)) {
    return invalidSessionResource("Agent Core changed immutable Session configuration in the event stream.");
  }
  return session;
}

function invalidRuntimeObservation(message = "Agent Core returned an invalid Runtime observation."): never {
  throw new AgentCoreError(message, 502, "invalid_runtime_observation");
}

function invalidRuntimeHistoryCapabilities(message = "Agent Core returned invalid Runtime history capabilities."): never {
  throw new AgentCoreError(message, 502, "invalid_runtime_history_capabilities");
}

function invalidRuntimeHistory(message = "Agent Core returned invalid Runtime history."): never {
  throw new AgentCoreError(message, 502, "invalid_runtime_history");
}

function nullableRuntimeNumber(value: unknown): number | null {
  if (value === null) return null;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    return invalidRuntimeObservation();
  }
  return value;
}

function nullableRuntimeInteger(value: unknown): number | null {
  const projected = nullableRuntimeNumber(value);
  if (projected !== null && !Number.isSafeInteger(projected)) return invalidRuntimeObservation();
  return projected;
}

function projectRuntimeObservation(value: unknown, expectedSessionId?: string): RuntimeObservation {
  if (!isRecord(value) || !exactFields(value, runtimeObservationFields)) {
    return invalidRuntimeObservation();
  }
  const id = canonicalUuid(value.id);
  const sessionId = canonicalUuid(value.session_id);
  const environmentId = value.environment_id === null ? null : canonicalUuid(value.environment_id);
  if (
    id === null || sessionId === null || id !== sessionId ||
    (expectedSessionId !== undefined && !sameUuid(sessionId, expectedSessionId)) ||
    value.object !== "agent.runtime_observation" ||
    (value.mode !== "none" && value.mode !== "self_hosted" && value.mode !== "openai_hosted") ||
    !(value.provider_type === null || (
      typeof value.provider_type === "string" && runtimeProviderTypePattern.test(value.provider_type)
    )) ||
    !isRecord(value.instance) || !exactFields(value.instance, runtimeInstanceFields) ||
    (value.status !== "observed" && value.status !== "unsupported" && value.status !== "unavailable") ||
    !(value.reason === null || (
      typeof value.reason === "string" && runtimeObservationReasons.has(value.reason)
    )) ||
    !isNonnegativeInteger(value.resolved_at)
  ) return invalidRuntimeObservation();

  const allocationId = value.instance.allocation_id === null ? null : canonicalUuid(value.instance.allocation_id);
  const deviceId = value.instance.device_id === null ? null : canonicalUuid(value.instance.device_id);
  const connectionGeneration = value.instance.connection_generation === null
    ? null
    : canonicalUuid(value.instance.connection_generation);
  if (
    (value.instance.allocation_id !== null && allocationId === null) ||
    (value.instance.device_id !== null && deviceId === null) ||
    (value.instance.connection_generation !== null && connectionGeneration === null)
  ) return invalidRuntimeObservation();

  const allocationCreatedAt = nullableRuntimeInteger(value.allocation_created_at);
  const observedAt = nullableRuntimeInteger(value.observed_at);
  const startedAt = nullableRuntimeInteger(value.started_at);
  const isNone = value.mode === "none";
  const isSelfHosted = value.mode === "self_hosted";
  const isManaged = value.mode === "openai_hosted";
  if (
    (isNone && (
      value.instance.kind !== "none" || environmentId !== null || value.provider_type !== null ||
      allocationId !== null || deviceId !== null || connectionGeneration !== null || allocationCreatedAt !== null
    )) ||
    (isSelfHosted && (
      value.instance.kind !== "self_hosted_connection" || environmentId === null ||
      allocationId !== null || allocationCreatedAt !== null
    )) ||
    (isManaged && (
      value.instance.kind !== "managed_allocation" || environmentId === null || connectionGeneration !== null ||
      (allocationId === null && (deviceId !== null || allocationCreatedAt !== null))
    ))
  ) return invalidRuntimeObservation();

  const observed = value.status === "observed";
  if (
    (observed && (
      !isManaged || allocationId === null || value.reason !== null || observedAt === null ||
      observedAt > value.resolved_at
    )) ||
    (!observed && (
      observedAt !== null || startedAt !== null || value.cpu !== null || value.memory !== null
    )) ||
    (value.status === "unsupported" && (
      (!isNone && !isSelfHosted) || value.reason !== "runtime_mode_not_observable"
    )) ||
    (value.status === "unavailable" && (
      !isManaged || value.reason === null || value.reason === "runtime_mode_not_observable"
    )) ||
    (startedAt !== null && observedAt !== null && startedAt > observedAt) ||
    (allocationCreatedAt !== null && allocationCreatedAt > value.resolved_at)
  ) return invalidRuntimeObservation();

  let cpu: RuntimeObservation["cpu"] = null;
  if (value.cpu !== null) {
    if (!observed || !isRecord(value.cpu) || !exactFields(value.cpu, runtimeCPUFields)) {
      return invalidRuntimeObservation();
    }
    cpu = {
      usage_seconds_total: nullableRuntimeNumber(value.cpu.usage_seconds_total),
      capacity_cores: nullableRuntimeNumber(value.cpu.capacity_cores),
      usage_cores: nullableRuntimeNumber(value.cpu.usage_cores),
      utilization_ratio: nullableRuntimeNumber(value.cpu.utilization_ratio),
    };
    if (
      Object.values(cpu).every((entry) => entry === null) ||
      (cpu.capacity_cores !== null && cpu.capacity_cores === 0)
    ) return invalidRuntimeObservation();
  }

  let memory: RuntimeObservation["memory"] = null;
  if (value.memory !== null) {
    if (!observed || !isRecord(value.memory) || !exactFields(value.memory, runtimeMemoryFields)) {
      return invalidRuntimeObservation();
    }
    memory = {
      usage_bytes: nullableRuntimeInteger(value.memory.usage_bytes),
      limit_bytes: nullableRuntimeInteger(value.memory.limit_bytes),
    };
    if (
      (memory.usage_bytes === null && memory.limit_bytes === null) ||
      memory.limit_bytes === 0
    ) return invalidRuntimeObservation();
  }

  return {
    id, object: "agent.runtime_observation", session_id: sessionId, environment_id: environmentId,
    mode: value.mode, provider_type: value.provider_type, instance: {
      kind: value.instance.kind as RuntimeObservation["instance"]["kind"],
      allocation_id: allocationId, device_id: deviceId, connection_generation: connectionGeneration,
    },
    status: value.status, reason: value.reason as RuntimeObservation["reason"],
    allocation_created_at: allocationCreatedAt, resolved_at: value.resolved_at,
    observed_at: observedAt, started_at: startedAt, cpu, memory,
  } as RuntimeObservation;
}

function projectRuntimeObservationList(value: unknown, options?: PageOptions): RuntimeObservationList {
  if (
    !isRecord(value) || !exactFields(value, vaultListFields) || value.object !== "list" ||
    !Array.isArray(value.data) || typeof value.has_more !== "boolean"
  ) return invalidRuntimeObservation("Agent Core returned an invalid Runtime observation list.");
  const limit = options?.limit ?? 20;
  if (
    !Number.isSafeInteger(limit) || limit < 1 || limit > 100 ||
    (options?.order !== undefined && options.order !== "asc" && options.order !== "desc") ||
    value.data.length > limit
  ) return invalidRuntimeObservation("Agent Core returned an invalid Runtime observation list.");
  const data = value.data.map((entry) => projectRuntimeObservation(entry));
  const firstId = data[0]?.id ?? null;
  const lastId = data[data.length - 1]?.id ?? null;
  if (
    new Set(data.map((entry) => entry.id)).size !== data.length ||
    value.first_id !== firstId || value.last_id !== lastId ||
    (value.has_more && data.length === 0)
  ) return invalidRuntimeObservation("Agent Core returned an invalid Runtime observation list.");
  return { object: "list", data, has_more: value.has_more, first_id: firstId, last_id: lastId };
}

function projectStreamError(value: unknown): StreamError {
  if (
    !isRecord(value) || !exactFields(value, streamErrorFields) ||
    typeof value.code !== "string" || value.code === "" ||
    typeof value.type !== "string" || value.type === "" ||
    typeof value.message !== "string"
  ) return invalidStreamEvent();
  return { code: value.code, type: value.type, message: value.message };
}

function requiredEventString(event: Record<string, unknown>, field: string, allowEmpty = false): string {
  const value = event[field];
  if (typeof value !== "string" || (!allowEmpty && value === "")) return invalidStreamEvent();
  return value;
}

function optionalEventIndex(event: Record<string, unknown>, field: string, required = false): number | undefined {
  const value = event[field];
  if (value === undefined && !required) return undefined;
  if (!isNonnegativeInteger(value)) return invalidStreamEvent();
  return value;
}

function eventSessionId(event: Record<string, unknown>, expectedSessionId: string, required: boolean): string | undefined {
  if (event.session_id === undefined && !required) return undefined;
  if (typeof event.session_id !== "string" || !sameResourceId(event.session_id, expectedSessionId)) {
    return invalidStreamEvent("Agent Core returned an event for a different Session.");
  }
  return event.session_id;
}

function projectEnvironmentState(value: unknown, status: string): AgentSessionEnvironmentEvent["environment"] {
  if (
    !isRecord(value) || !onlyFields(value, environmentStateFields) ||
    typeof value.id !== "string" || value.id === "" ||
    typeof value.type !== "string" || value.type === "" || value.status !== status ||
    !(value.error === undefined || value.error === null || isRecord(value.error))
  ) return invalidStreamEvent();
  return {
    id: value.id,
    type: value.type,
    status: value.status as AgentSessionEnvironmentEvent["environment"]["status"],
    error: value.error === undefined || value.error === null ? null : projectStreamError(value.error),
  };
}

function invalidStreamEvent(message = "Agent Core returned an invalid event stream payload."): never {
  throw new AgentCoreError(message, 502, "invalid_stream_event");
}

function invalidHistoryResource(): never {
  throw new AgentCoreError("Agent Core returned an invalid history resource.", 502, "invalid_history_resource");
}

function parseStreamEvent(message: { event?: string; data: string }): SessionEvent {
  let value: unknown;
  try {
    value = JSON.parse(message.data);
  } catch {
    return invalidStreamEvent();
  }
  if (!isRecord(value)) return invalidStreamEvent();

  if (message.event !== undefined && message.event !== value.type) {
    return invalidStreamEvent();
  }
  const type = value.type;
  if (
    typeof type !== "string" || type.trim() === "" ||
    typeof value.event_id !== "string" || value.event_id.trim() === ""
  ) return invalidStreamEvent();

  return { ...value, type } as SessionEvent;
}

function projectCreatedSessionEvent(
  event: SessionEvent,
  expectedVaultIds: string[],
  expectedEnvironment: ExpectedCreationEnvironment,
): { event: SessionEvent; session: AgentSession } {
  const value = event as unknown as Record<string, unknown>;
  if (event.type !== "agent.session.created" || !onlyFields(value, snapshotEventFields) || event.session === undefined) {
    return invalidStreamEvent("Agent Core did not begin Session creation with a created Session snapshot.");
  }
  const session = projectAgentSession(event.session, expectedVaultIds, undefined, expectedEnvironment);
  const sessionId = eventSessionId(value, session.id, false);
  return {
    event: {
      type: "agent.session.created",
      event_id: event.event_id,
      ...(sessionId === undefined ? {} : { session_id: sessionId }),
      session,
    },
    session,
  };
}

function projectStreamEventSession(
  event: SessionEvent,
  expectedSessionId: string,
  immutable?: ImmutableSessionProjection,
): SessionEvent {
  const value = event as unknown as Record<string, unknown>;
  const base = { type: event.type, event_id: event.event_id };
  const sessionStatusesByEvent: Record<string, AgentSession["status"]> = {
    "agent.session.in_progress": "in_progress",
    "agent.session.requires_action": "requires_action",
    "agent.session.idle": "idle",
    "agent.session.failed": "failed",
  };
  if (event.type === "agent.session.created" || hasOwn(sessionStatusesByEvent, event.type)) {
    if (!onlyFields(value, snapshotEventFields) || event.session === undefined) return invalidStreamEvent();
    const sessionId = eventSessionId(value, expectedSessionId, false);
    const session = projectAgentSession(event.session, undefined, expectedSessionId, undefined, immutable);
    if (event.type !== "agent.session.created" && session.status !== sessionStatusesByEvent[event.type]) {
      return invalidStreamEvent();
    }
    return { ...base, ...(sessionId === undefined ? {} : { session_id: sessionId }), session } as SessionEvent;
  }

  const turnStatusByEvent: Record<string, AgentTurn["status"]> = {
    "agent.session.turn.created": "queued",
    "agent.session.turn.in_progress": "in_progress",
    "agent.session.turn.waiting": "waiting",
    "agent.session.turn.completed": "completed",
    "agent.session.turn.failed": "failed",
    "agent.session.turn.cancelled": "cancelled",
  };
  if (hasOwn(turnStatusByEvent, event.type)) {
    // Older Core releases omit terminal usage; no other Turn event may carry it.
    const withUsage = terminalTurnEventTypes.has(event.type) && hasOwn(value, "usage");
    if (!exactFields(value, withUsage ? terminalTurnEventFields : turnEventFields) || event.turn === undefined) {
      return invalidStreamEvent();
    }
    const sessionId = eventSessionId(value, expectedSessionId, true)!;
    const turnId = requiredEventString(value, "turn_id");
    const turn = projectAgentTurn(event.turn, expectedSessionId, invalidStreamEvent);
    const usage = withUsage ? { usage: projectTokenUsage(value.usage, invalidStreamEvent) } : {};
    if (
      !sameResourceId(turn.id, turnId) ||
      // Core no longer streams child Turns. Earlier releases did, and native child
      // history could first appear there as a completed created snapshot.
      (!(event.type === "agent.session.turn.created" && turn.subagent_id != null) && turn.status !== turnStatusByEvent[event.type]) ||
      (immutable !== undefined && turn.subagent_id == null && !sameResourceId(turn.agent_id, immutable.agent.id))
    ) return invalidStreamEvent();
    return { ...base, session_id: sessionId, turn_id: turnId, turn, ...usage } as SessionEvent;
  }

  if (event.type === "agent.session.turn.item.added" || event.type === "agent.session.turn.item.done") {
    if (!onlyFields(value, itemEventFields) || event.item === undefined) return invalidStreamEvent();
    const sessionId = eventSessionId(value, expectedSessionId, true)!;
    const turnId = requiredEventString(value, "turn_id");
    const item = projectSessionItem(event.item, invalidStreamEvent);
    if (!sameResourceId(item.turn_id, turnId)) return invalidStreamEvent();
    const itemId = value.item_id === undefined ? undefined : requiredEventString(value, "item_id");
    if (itemId !== undefined && !sameResourceId(item.id, itemId)) return invalidStreamEvent();
    const outputIndex = optionalEventIndex(value, "output_index");
    return {
      ...base,
      session_id: sessionId,
      turn_id: turnId,
      ...(itemId === undefined ? {} : { item_id: itemId }),
      ...(outputIndex === undefined ? {} : { output_index: outputIndex }),
      item,
    } as SessionEvent;
  }

  if (event.type === "agent.session.turn.content_part.added" || event.type === "agent.session.turn.content_part.done") {
    if (!exactFields(value, contentPartEventFields)) return invalidStreamEvent();
    const sessionId = eventSessionId(value, expectedSessionId, true)!;
    const turnId = requiredEventString(value, "turn_id");
    const itemId = requiredEventString(value, "item_id");
    const outputIndex = optionalEventIndex(value, "output_index", true)!;
    const contentIndex = optionalEventIndex(value, "content_index", true)!;
    const part = projectItemContent(value.part, invalidStreamEvent);
    return { ...base, session_id: sessionId, turn_id: turnId, item_id: itemId, output_index: outputIndex, content_index: contentIndex, part } as SessionEvent;
  }

  if (event.type === "agent.session.turn.output_text.delta" || event.type === "agent.session.turn.output_text.done") {
    const field = event.type.endsWith(".delta") ? "delta" : "text";
    if (!exactFields(value, new Set([...outputTextEventBaseFields, field]))) return invalidStreamEvent();
    const sessionId = eventSessionId(value, expectedSessionId, true)!;
    const turnId = requiredEventString(value, "turn_id");
    const itemId = requiredEventString(value, "item_id");
    const outputIndex = optionalEventIndex(value, "output_index", true)!;
    const contentIndex = optionalEventIndex(value, "content_index", true)!;
    const text = requiredEventString(value, field, true);
    return {
      ...base,
      session_id: sessionId,
      turn_id: turnId,
      item_id: itemId,
      output_index: outputIndex,
      content_index: contentIndex,
      [field]: text,
    } as SessionEvent;
  }

  if (event.type === "agent.output.command_execution_output.delta") {
    if (!exactFields(value, commandDeltaEventFields)) return invalidStreamEvent();
    const sessionId = eventSessionId(value, expectedSessionId, true)!;
    return {
      ...base,
      session_id: sessionId,
      turn_id: requiredEventString(value, "turn_id"),
      item_id: requiredEventString(value, "item_id"),
      output_index: optionalEventIndex(value, "output_index", true)!,
      delta: requiredEventString(value, "delta", true),
    } as SessionEvent;
  }

  if (event.type.startsWith("agent.session.environment.")) {
    const status = event.type.slice("agent.session.environment.".length);
    if (!new Set(["pending", "ready", "connected", "disconnected", "failed"]).has(status)) {
      return projectUnknownStreamEvent(value, expectedSessionId);
    }
    if (!exactFields(value, environmentEventFields)) return invalidStreamEvent();
    const sessionId = eventSessionId(value, expectedSessionId, true)!;
    const environment = projectEnvironmentState(value.environment, status);
    if (
      immutable !== undefined &&
      (immutable.environment.type === "none" ||
        immutable.environment.type !== environment.type ||
        !("id" in immutable.environment) ||
        typeof immutable.environment.id !== "string" ||
        !sameResourceId(immutable.environment.id, environment.id))
    ) return invalidStreamEvent();
    return { ...base, session_id: sessionId, environment } as SessionEvent;
  }

  return projectUnknownStreamEvent(value, expectedSessionId);
}

function projectUnknownStreamEvent(
  value: Record<string, unknown>,
  expectedSessionId: string,
): SessionEvent {
  const projected: Record<string, unknown> = {
    type: value.type,
    event_id: value.event_id,
  };
  const sessionId = eventSessionId(value, expectedSessionId, false);
  if (sessionId !== undefined) projected.session_id = sessionId;
  for (const [field, fieldValue] of Object.entries(value)) {
    if (
      field === "type" || field === "event_id" || field === "session_id" ||
      field === "__proto__" || field === "constructor" || field === "prototype" ||
      unsafeUnknownEventFields.has(field)
    ) continue;
    projected[field] = fieldValue;
  }
  return projected as SessionEvent;
}

interface EventStreamConsumerOptions extends StreamOptions {
  onParsedEvent: (event: SessionEvent) => void;
  expectedSessionId?: () => string | undefined;
  /** Replaces the generic error when a present stream body ends without events. */
  emptyStreamError?: () => AgentCoreError;
}

async function consumeEventStream(
  body: ReadableStream<Uint8Array> | null,
  options: EventStreamConsumerOptions,
): Promise<void> {
  if (!body) {
    throw new AgentCoreError("Agent core returned an empty event stream.", 502, "empty_stream");
  }

  const reader = body.getReader();
  const text = new TextDecoder();
  let sawEvent = false;
  const decoder = createSSEDecoder((message) => {
    if (message.data.trim() === "[DONE]") return;
    const event = parseStreamEvent(message);
    sawEvent = true;
    if (event.type === "error") {
      const raw = event as unknown as Record<string, unknown>;
      if (!exactFields(raw, errorEventFields) || event.error === undefined) return invalidStreamEvent();
      const sessionId = requiredEventString(raw, "session_id");
      const expectedSessionId = options.expectedSessionId?.();
      if (expectedSessionId !== undefined && !sameResourceId(sessionId, expectedSessionId)) {
        return invalidStreamEvent("Agent Core returned an event for a different Session.");
      }
      const streamError = projectStreamError(event.error);
      throw new AgentCoreError(
        "Agent Core interrupted the live event stream. Reconnect and retrieve durable state.",
        503,
        streamError.code,
        null,
        streamError.type,
      );
    }
    options.onParsedEvent(event);
  });
  const abort = () => {
    void reader.cancel(options.signal?.reason).catch(() => undefined);
  };

  try {
    options.signal?.throwIfAborted();
    options.signal?.addEventListener("abort", abort, { once: true });
    options.onOpen?.();
    while (true) {
      const { done, value } = await reader.read();
      options.signal?.throwIfAborted();
      if (done) break;
      decoder.push(text.decode(value, { stream: true }));
    }
    decoder.push(text.decode());
    decoder.finish();
    options.signal?.throwIfAborted();
    if (!sawEvent) {
      throw options.emptyStreamError?.() ?? new AgentCoreError("Agent core returned an empty event stream.", 502, "empty_stream");
    }
  } catch (error) {
    await reader.cancel(error).catch(() => undefined);
    throw error;
  } finally {
    options.signal?.removeEventListener("abort", abort);
    reader.releaseLock();
  }
}

function isEnvironmentResourceStatus(value: unknown): value is EnvironmentResourceStatus {
  return value === "pending" || value === "connected" || value === "disconnected" || value === "expired" || value === "failed";
}

function isExpectedEnvironmentId(value: unknown, expectedId: string): value is string {
  if (typeof value !== "string") return false;
  const canonicalExpectedId = expectedId.toLowerCase();
  if (canonicalUuidPattern.test(canonicalExpectedId)) return value === canonicalExpectedId;
  return value === expectedId;
}

function projectEnvironmentResource(value: unknown, expectedId: string): AgentEnvironmentResource {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new AgentCoreError("Agent Core returned an invalid Environment resource.", 502, "invalid_environment_resource");
  }
  const resource = value as Record<string, unknown>;
  const fields = Object.keys(resource);
  if (
    fields.length !== environmentResourceFields.size ||
    fields.some((field) => !environmentResourceFields.has(field)) ||
    !isExpectedEnvironmentId(resource.id, expectedId) ||
    resource.object !== "agent.environment" ||
    (resource.type !== "self_hosted" && resource.type !== "openai_hosted") ||
    !isEnvironmentResourceStatus(resource.status) ||
    !Array.isArray(resource.files) ||
    !Array.isArray(resource.plugins) ||
    !Array.isArray(resource.skills) ||
    (resource.type === "openai_hosted" &&
      (resource.files.length !== 0 || resource.plugins.length !== 0 || resource.skills.length !== 0))
  ) {
    throw new AgentCoreError("Agent Core returned an invalid Environment resource.", 502, "invalid_environment_resource");
  }
  if (resource.type === "openai_hosted") {
    return {
      id: resource.id,
      object: "agent.environment",
      type: "openai_hosted",
      status: resource.status,
      files: [],
      plugins: [],
      skills: [],
    };
  }
  return {
    id: resource.id,
    object: "agent.environment",
    type: "self_hosted",
    status: resource.status,
    files: resource.files,
    plugins: resource.plugins,
    skills: resource.skills,
  };
}

function invalidSourceFile(): never {
  throw new AgentCoreError("Agent Core returned invalid Source File metadata.", 502, "invalid_source_file");
}

function invalidSourceFileContent(message: string): never {
  throw new AgentCoreError(message, 502, "invalid_source_file_content");
}

async function readExactSourceFileBody(response: Response, expectedBytes: number): Promise<Uint8Array> {
  if (response.body === null) {
    if (expectedBytes === 0) return new Uint8Array();
    return invalidSourceFileContent("Agent Core returned incomplete Source File content.");
  }
  const reader = response.body.getReader();
  const data = new Uint8Array(expectedBytes);
  let offset = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!(value instanceof Uint8Array) || value.byteLength > expectedBytes - offset) {
        await reader.cancel().catch(() => undefined);
        return invalidSourceFileContent("Agent Core returned Source File content with a mismatched length.");
      }
      data.set(value, offset);
      offset += value.byteLength;
    }
  } finally {
    reader.releaseLock();
  }
  if (offset !== expectedBytes) {
    return invalidSourceFileContent("Agent Core returned incomplete Source File content.");
  }
  return data;
}

function validSourceFileId(value: unknown): value is string {
  return typeof value === "string" && sourceFileIdPattern.test(value);
}

function projectSourceFile(value: unknown, expectedId?: string): SourceFile {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return invalidSourceFile();
  const file = value as Record<string, unknown>;
  const fields = Object.keys(file);
  const filenameBytes = typeof file.filename === "string"
    ? new TextEncoder().encode(file.filename).length
    : 0;
  if (
    fields.length !== sourceFileFields.size ||
    fields.some((field) => !sourceFileFields.has(field)) ||
    !validSourceFileId(file.id) ||
    (expectedId !== undefined && file.id !== expectedId) ||
    file.object !== "file" ||
    !Number.isSafeInteger(file.bytes) ||
    Number(file.bytes) < 0 ||
    Number(file.bytes) > maxSourceFileBytes ||
    !Number.isSafeInteger(file.created_at) ||
    Number(file.created_at) < 0 ||
    typeof file.filename !== "string" ||
    filenameBytes < 1 ||
    filenameBytes > 1024 ||
    file.filename.includes("\0") ||
    file.purpose !== "user_data" ||
    file.status !== "processed" ||
    file.expires_at !== null ||
    file.status_details !== null
  ) return invalidSourceFile();
  return {
    id: file.id,
    object: "file",
    bytes: Number(file.bytes),
    created_at: Number(file.created_at),
    filename: file.filename,
    purpose: "user_data",
    status: "processed",
    expires_at: null,
    status_details: null,
  };
}

function projectSourceFileDeleted(value: unknown, expectedId: string): SourceFileDeleted {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return invalidSourceFile();
  const deleted = value as Record<string, unknown>;
  const fields = Object.keys(deleted);
  if (
    fields.length !== sourceFileDeletedFields.size ||
    fields.some((field) => !sourceFileDeletedFields.has(field)) ||
    deleted.id !== expectedId ||
    deleted.object !== "file" ||
    deleted.deleted !== true
  ) return invalidSourceFile();
  return { id: expectedId, object: "file", deleted: true };
}

function validEnvironmentFilePath(value: unknown): value is string {
  return typeof value === "string" &&
    value.startsWith("/workspace/") &&
    canonicalAbsoluteDirectory(value) === value;
}

function projectEnvironmentFile(
  value: unknown,
  expectedEnvironmentId: string,
  expectedPath: string,
  expectedSize?: number,
): EnvironmentFile {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new AgentCoreError("Agent Core returned an invalid Environment file.", 502, "invalid_environment_file");
  }
  const file = value as Record<string, unknown>;
  const fields = Object.keys(file);
  if (
    fields.length !== environmentFileFields.size ||
    fields.some((field) => !environmentFileFields.has(field)) ||
    !isExpectedEnvironmentId(file.environment_id, expectedEnvironmentId) ||
    file.object !== "agent.environment.file" ||
    file.path !== expectedPath ||
    !validEnvironmentFilePath(file.path) ||
    !Number.isSafeInteger(file.size_bytes) ||
    Number(file.size_bytes) < 0 ||
    Number(file.size_bytes) > maxEnvironmentFileBytes ||
    (expectedSize !== undefined && file.size_bytes !== expectedSize)
  ) {
    throw new AgentCoreError("Agent Core returned an invalid Environment file.", 502, "invalid_environment_file");
  }
  return {
    environment_id: file.environment_id as string,
    object: "agent.environment.file",
    path: file.path,
    size_bytes: Number(file.size_bytes),
  };
}

function strictBase64DecodedBytes(value: unknown): number | null {
  if (typeof value !== "string") return null;
  if (value === "") return 0;
  if (value.length % 4 !== 0 || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) {
    return null;
  }
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  if (value.endsWith("==") && (alphabet.indexOf(value[value.length - 3] ?? "") & 15) !== 0) return null;
  if (value.endsWith("=") && !value.endsWith("==") && (alphabet.indexOf(value[value.length - 2] ?? "") & 3) !== 0) return null;
  const padding = value.endsWith("==") ? 2 : value.endsWith("=") ? 1 : 0;
  return (value.length / 4) * 3 - padding;
}

function canonicalAbsoluteDirectory(value: unknown): string | null {
  if (
    typeof value !== "string" ||
    !value.startsWith("/") ||
    new TextEncoder().encode(value).length > 4096 ||
    value.includes("\\") ||
    value.includes("\0") ||
    value.includes("\r") ||
    value.includes("\n") ||
    value.split("/").includes("..")
  ) return null;
  const components = value.split("/").filter((component) => component && component !== ".");
  return components.length ? `/${components.join("/")}` : "/";
}

function canonicalEnvironmentFilesDirectory(value: unknown): string | null {
  return canonicalAbsoluteDirectory(value);
}

function environmentFileParent(path: string): string {
  const separator = path.lastIndexOf("/");
  return separator <= 0 ? "/" : path.slice(0, separator);
}

function compareUtf8(left: string, right: string): number {
  const leftBytes = new TextEncoder().encode(left);
  const rightBytes = new TextEncoder().encode(right);
  const length = Math.min(leftBytes.length, rightBytes.length);
  for (let index = 0; index < length; index += 1) {
    if (leftBytes[index] !== rightBytes[index]) return Number(leftBytes[index]) - Number(rightBytes[index]);
  }
  return leftBytes.length - rightBytes.length;
}

function invalidEnvironmentFiles(): never {
  throw new AgentCoreError("Agent Core returned an invalid Environment files page.", 502, "invalid_environment_files");
}

function projectEnvironmentFileList(
  value: unknown,
  expectedId: string,
  options: EnvironmentFileListOptions,
): EnvironmentFileList {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return invalidEnvironmentFiles();
  }
  const page = value as Record<string, unknown>;
  const fields = Object.keys(page);
  const limit = options?.limit ?? 20;
  const order = options?.order ?? "desc";
  const requestedDirectory = canonicalEnvironmentFilesDirectory(options.path ?? "/workspace");
  if (
    fields.length !== environmentFileListFields.size ||
    fields.some((field) => !environmentFileListFields.has(field)) ||
    page.object !== "page" ||
    page.has_more !== (page.next !== null) ||
    !Array.isArray(page.data) ||
    !Number.isSafeInteger(limit) ||
    limit < 1 ||
    limit > 100 ||
    page.data.length > limit ||
    (order !== "asc" && order !== "desc") ||
    requestedDirectory === null ||
    !(page.next === null || (
      typeof page.next === "string" &&
      page.next.length > 0 &&
      new TextEncoder().encode(page.next).length <= 1024 &&
      page.data.length === limit
    ))
  ) {
    return invalidEnvironmentFiles();
  }

  let previousPath: string | null = null;
  const seenPaths = new Set<string>();
  const files = page.data.map((entry) => {
    if (entry === null || typeof entry !== "object" || Array.isArray(entry)) {
      return invalidEnvironmentFiles();
    }
    const file = entry as Record<string, unknown>;
    const fileFields = Object.keys(file);
    const canonicalPath = canonicalAbsoluteDirectory(file.path);
    const sorted = previousPath === null || (
      order === "asc"
        ? compareUtf8(previousPath, String(file.path)) < 0
        : compareUtf8(previousPath, String(file.path)) > 0
    );
    if (
      fileFields.length !== environmentFileFields.size ||
      fileFields.some((field) => !environmentFileFields.has(field)) ||
      !isExpectedEnvironmentId(file.environment_id, expectedId) ||
      file.object !== "agent.environment.file" ||
      canonicalPath === null ||
      canonicalPath !== file.path ||
      environmentFileParent(canonicalPath) !== requestedDirectory ||
      seenPaths.has(canonicalPath) ||
      !sorted ||
      !Number.isSafeInteger(file.size_bytes) ||
      Number(file.size_bytes) < 0
    ) {
      return invalidEnvironmentFiles();
    }
    seenPaths.add(canonicalPath);
    previousPath = canonicalPath;
    return {
      environment_id: file.environment_id,
      object: "agent.environment.file" as const,
      path: file.path,
      size_bytes: Number(file.size_bytes),
    };
  });

  return { object: "page", data: files, next: page.next as string | null, has_more: page.has_more as boolean };
}

export class OpenAIAgentsClient implements AgentCore {
  async retrieveStartupConfiguration(options?: ReadOptions): Promise<CoreStartupConfiguration> {
    const value = await this.request<unknown>("/agents/core/startup-configuration", { signal: options?.signal }, 200);
    return projectStartupConfiguration(value);
  }

  private readonly baseUrl: string;
  private readonly token: OpenAIAgentsClientOptions["token"];
  private readonly fetchImpl: typeof fetch;

  constructor(options: OpenAIAgentsClientOptions = {}) {
    this.baseUrl = trimTrailingSlash(options.baseUrl ?? "/v1");
    this.token = options.token;
    this.fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis);
  }

  private headers(extra?: HeadersInit, includeBeta = true): Headers {
    const headers = new Headers(extra);
    if (!headers.has("Accept")) headers.set("Accept", "application/json");
    if (includeBeta) headers.set("OpenAI-Beta", "agents=v1");
    else headers.delete("OpenAI-Beta");
    const token = typeof this.token === "function" ? this.token() : this.token;
    if (token) headers.set("Authorization", `Bearer ${token}`);
    return headers;
  }

  private async toError(response: Response): Promise<AgentCoreError> {
    let envelope: APIErrorEnvelope | undefined;
    try {
      envelope = (await response.json()) as APIErrorEnvelope;
    } catch {
      // Keep the customer-safe HTTP fallback when an intermediary returns HTML.
    }
    return new AgentCoreError(
      envelope?.error?.message ?? `Agent core request failed (${response.status}).`,
      response.status,
      envelope?.error?.code,
      envelope?.error?.param,
      envelope?.error?.type,
    );
  }

  protected async request<T>(
    path: string,
    init: RequestInit = {},
    expectedStatus?: number,
    includeBeta = true,
  ): Promise<T> {
    const headers = this.headers(init.headers, includeBeta);
    const isMultipart = typeof FormData !== "undefined" && init.body instanceof FormData;
    if (init.body !== undefined && !isMultipart && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, { ...init, headers });
    if (!response.ok || (expectedStatus !== undefined && response.status !== expectedStatus)) {
      throw await this.toError(response);
    }
    if (response.status === 204 || expectedStatus === 202) return undefined as T;
    return (await response.json()) as T;
  }

  private async requestCredentialWrite(
    path: string,
    body: string,
    safeMessage: string,
    expectedStatus: 200 | 201,
  ): Promise<unknown> {
    const headers = this.headers({ "Content-Type": "application/json" });
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method: "POST",
      headers,
      body,
    });
    if (!response.ok || response.status !== expectedStatus) {
      // A rejected secret-bearing write may reflect attacker-controlled token
      // bytes in every upstream error field. Never parse or expose that body.
      throw new AgentCoreError(safeMessage, response.status, "credential_write_failed");
    }
    return await response.json() as unknown;
  }

  listAgents(options?: PageOptions): Promise<ListPage<SavedAgent>> {
    const params = new URLSearchParams();
    addPageOptions(params, options);
    return this.request(withQuery("/agents", params), { signal: options?.signal });
  }

  createAgent(input: CreateAgentInput): Promise<SavedAgent> {
    return this.request("/agents", { method: "POST", body: JSON.stringify(input) });
  }

  retrieveAgent(agentId: string): Promise<SavedAgent> {
    return this.request(`/agents/${encodeURIComponent(agentId)}`);
  }

  updateAgent(agentId: string, input: UpdateAgentInput): Promise<SavedAgent> {
    return this.request(`/agents/${encodeURIComponent(agentId)}`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  deleteAgent(agentId: string): Promise<AgentDeleted> {
    return this.request(`/agents/${encodeURIComponent(agentId)}`, { method: "DELETE" });
  }

  async listVaults(options?: VaultListOptions): Promise<VaultList> {
    const params = new URLSearchParams();
    addVaultPageOptions(params, options);
    const value = await this.request<unknown>(withQuery("/vaults", params), { signal: options?.signal }, 200);
    return projectVaultList(value, options);
  }

  async createVault(input: CreateVaultInput): Promise<Vault> {
    const fields = Object.keys(input);
    if (
      fields.some((field) => field !== "name" && field !== "metadata") ||
      (input.name !== undefined && !isTrimmedName(input.name)) ||
      (input.metadata !== undefined && input.metadata !== null && !validMetadata(input.metadata))
    ) {
      throw new TypeError("Vault creation accepts a trimmed name and string metadata only.");
    }
    const value = await this.request<unknown>("/vaults", { method: "POST", body: JSON.stringify(input) }, 201);
    const vault = projectVault(value);
    const expectedName = input.name ?? null;
    const expectedMetadata = input.metadata ?? {};
    if (vault.name !== expectedName || !sameMetadata(vault.metadata, expectedMetadata)) {
      return invalidVaultResponse("invalid_vault_resource", "Agent Core returned mismatched Vault metadata.");
    }
    return vault;
  }

  async retrieveVault(vaultId: string, options?: ReadOptions): Promise<Vault> {
    const value = await this.request<unknown>(`/vaults/${encodeURIComponent(vaultId)}`, { signal: options?.signal }, 200);
    return projectVault(value, vaultId);
  }

  async deleteVault(vaultId: string): Promise<VaultDeleted> {
    const value = await this.request<unknown>(`/vaults/${encodeURIComponent(vaultId)}`, { method: "DELETE" }, 200);
    return projectDeletedResource<VaultDeleted>(value, vaultId, "vault.deleted", vaultDeletedFields, "invalid_vault_deletion");
  }

  async listVaultCredentials(vaultId: string, options?: VaultListOptions): Promise<VaultCredentialList> {
    const params = new URLSearchParams();
    addVaultPageOptions(params, options);
    const value = await this.request<unknown>(
      withQuery(`/vaults/${encodeURIComponent(vaultId)}/credentials`, params),
      { signal: options?.signal },
      200,
    );
    return projectVaultCredentialList(value, vaultId, options);
  }

  async createVaultCredential(vaultId: string, input: CreateVaultCredentialInput): Promise<VaultCredential> {
    const fields = Object.keys(input);
    const authFields = isRecord(input.auth) ? Object.keys(input.auth) : [];
    if (
      fields.length !== 2 || fields.some((field) => field !== "name" && field !== "auth") ||
      !isTrimmedName(input.name) || !isRecord(input.auth) ||
      authFields.length !== 3 || authFields.some((field) => field !== "type" && field !== "mcp_server_url" && field !== "token") ||
      input.auth.type !== "static_bearer" || !validCredentialURL(input.auth.mcp_server_url) || typeof input.auth.token !== "string"
    ) {
      throw new TypeError("Credential creation requires a name, an HTTPS MCP URL, and a write-only token.");
    }
    const value = await this.requestCredentialWrite(
      `/vaults/${encodeURIComponent(vaultId)}/credentials`,
      JSON.stringify(input),
      "Agent Core Credential creation failed.",
      201,
    );
    const credential = projectVaultCredential(value, vaultId);
    if (credential.auth.type !== "static_bearer" || credential.name !== input.name || credential.auth.mcp_server_url !== input.auth.mcp_server_url) {
      return invalidVaultResponse("invalid_vault_credential", "Agent Core returned mismatched Credential metadata.");
    }
    return credential;
  }

  async retrieveVaultCredential(
    vaultId: string,
    credentialId: string,
    options?: ReadOptions,
  ): Promise<VaultCredential> {
    const value = await this.request<unknown>(
      `/vaults/${encodeURIComponent(vaultId)}/credentials/${encodeURIComponent(credentialId)}`,
      { signal: options?.signal },
      200,
    );
    return projectVaultCredential(value, vaultId, credentialId);
  }

  async replaceVaultCredentialToken(
    vaultId: string,
    credentialId: string,
    input: ReplaceVaultCredentialTokenInput,
  ): Promise<VaultCredential> {
    const fields = Object.keys(input);
    const authFields = isRecord(input.auth) ? Object.keys(input.auth) : [];
    if (
      fields.length !== 1 || fields[0] !== "auth" || !isRecord(input.auth) ||
      authFields.length !== 2 || authFields.some((field) => field !== "type" && field !== "token") ||
      input.auth.type !== "static_bearer" || typeof input.auth.token !== "string"
    ) {
      throw new TypeError("Credential replacement accepts one write-only static bearer token.");
    }
    const baseline = await this.retrieveVaultCredential(vaultId, credentialId);
    if (baseline.auth.type !== "static_bearer") {
      throw new TypeError("Static bearer token replacement is unavailable for OAuth credentials.");
    }
    const value = await this.requestCredentialWrite(
      `/vaults/${encodeURIComponent(vaultId)}/credentials/${encodeURIComponent(credentialId)}`,
      JSON.stringify(input),
      "Agent Core Credential token replacement failed.",
      200,
    );
    const credential = projectVaultCredential(value, vaultId, credentialId);
    if (
      credential.auth.type !== "static_bearer" || credential.name !== baseline.name ||
      credential.auth.mcp_server_url !== baseline.auth.mcp_server_url ||
      credential.created_at !== baseline.created_at ||
      credential.updated_at < baseline.updated_at
    ) {
      return invalidVaultResponse("invalid_vault_credential", "Agent Core returned mismatched Credential metadata after token replacement.");
    }
    return credential;
  }

  async deleteVaultCredential(vaultId: string, credentialId: string): Promise<VaultCredentialDeleted> {
    const value = await this.request<unknown>(
      `/vaults/${encodeURIComponent(vaultId)}/credentials/${encodeURIComponent(credentialId)}`,
      { method: "DELETE" },
      200,
    );
    return projectDeletedResource<VaultCredentialDeleted>(
      value,
      credentialId,
      "vault.credential.deleted",
      vaultCredentialDeletedFields,
      "invalid_vault_credential_deletion",
    );
  }

  async listSessions(options?: PageOptions & { agentId?: string }): Promise<ListPage<AgentSession>> {
    const params = new URLSearchParams();
    addPageOptions(params, options);
    if (options?.agentId) params.set("agent_id", options.agentId);
    const page = await this.request<ListPage<unknown>>(withQuery("/agents/sessions", params), { signal: options?.signal });
    if (!isRecord(page) || !Array.isArray(page.data)) {
      return invalidVaultResponse("invalid_session_vaults", "Agent Core returned invalid Session Vault attachments.");
    }
    return { ...page, data: page.data.map((session) => projectAgentSession(session)) };
  }

  async listRuntimeObservations(options?: PageOptions): Promise<RuntimeObservationList> {
    if (
      (options?.after !== undefined && canonicalUuid(options.after) === null) ||
      (options?.limit !== undefined && (
        !Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100
      )) ||
      (options?.order !== undefined && options.order !== "asc" && options.order !== "desc")
    ) throw new TypeError("Runtime observation pagination options are invalid.");
    const params = new URLSearchParams();
    addPageOptions(params, options);
    const value = await this.request<unknown>(
      withQuery("/agents/runtime-observations", params),
      { signal: options?.signal },
    );
    return projectRuntimeObservationList(value, options);
  }

  async retrieveRuntimeObservation(sessionId: string, options?: ReadOptions): Promise<RuntimeObservation> {
    const value = await this.request<unknown>(
      `/agents/sessions/${encodeURIComponent(sessionId)}/runtime-observation`,
      { signal: options?.signal },
    );
    return projectRuntimeObservation(value, sessionId);
  }

  async getRuntimeHistoryCapabilities(options?: ReadOptions): Promise<RuntimeHistoryCapabilities> {
    const value = await this.request<unknown>(
      "/agents/runtime-history/capabilities",
      { signal: options?.signal },
    );
    return projectRuntimeHistoryCapabilities(value, invalidRuntimeHistoryCapabilities);
  }

  async retrieveRuntimeHistory(sessionId: string, query: RuntimeHistoryQuery): Promise<RuntimeHistory> {
    const canonicalSessionId = canonicalUuid(sessionId);
    if (
      query == null || canonicalSessionId === null || !isNonnegativeInteger(query.start) || !isNonnegativeInteger(query.end) ||
      query.end <= query.start || (query.maxPoints !== undefined && (
        !Number.isSafeInteger(query.maxPoints) || query.maxPoints < 2 || query.maxPoints > 10_000
      ))
    ) throw new TypeError("Runtime history query is invalid.");
    const params = new URLSearchParams();
    params.set("start", String(query.start));
    params.set("end", String(query.end));
    if (query.maxPoints !== undefined) params.set("max_points", String(query.maxPoints));
    const value = await this.request<unknown>(
      withQuery(`/agents/sessions/${encodeURIComponent(canonicalSessionId)}/runtime-history`, params),
      { signal: query.signal },
    );
    return projectRuntimeHistory(value, canonicalSessionId, query, invalidRuntimeHistory);
  }

  async createSession(input: CreateSessionInput, idempotencyKey = createIdempotencyKey()): Promise<AgentSession> {
    if ((input as { stream?: boolean }).stream === true) {
      throw new TypeError("createSession only supports the JSON response; connect streamEvents after creation.");
    }
    const expectedVaultIds = input.vault_ids ?? [];
    if (
      expectedVaultIds.some((id) => !canonicalUuidPattern.test(id)) ||
      new Set(expectedVaultIds).size !== expectedVaultIds.length
    ) {
      throw new TypeError("Session vault_ids must contain unique canonical UUIDs.");
    }
    const expectedEnvironment = normalizeCreationEnvironment(input.environment);
    const value = await this.request<unknown>("/agents/sessions", {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: JSON.stringify(input),
    });
    return projectAgentSession(value, expectedVaultIds, undefined, expectedEnvironment);
  }

  async createSessionStream(
    input: Omit<CreateSessionInput, "stream">,
    idempotencyKey: string | undefined,
    options: CreateSessionStreamOptions,
  ): Promise<void> {
    const expectedVaultIds = input.vault_ids ?? [];
    if (
      expectedVaultIds.some((id) => !canonicalUuidPattern.test(id)) ||
      new Set(expectedVaultIds).size !== expectedVaultIds.length
    ) {
      throw new TypeError("Session vault_ids must contain unique canonical UUIDs.");
    }
    const expectedEnvironment = normalizeCreationEnvironment(input.environment);

    const headers = this.headers({
      Accept: "text/event-stream",
      "Content-Type": "application/json",
      "Idempotency-Key": idempotencyKey ?? createIdempotencyKey(),
    });
    const response = await this.fetchImpl(`${this.baseUrl}/agents/sessions`, {
      method: "POST",
      headers,
      body: JSON.stringify({ ...input, stream: true }),
      signal: options.signal,
    });
    if (!response.ok) throw await this.toError(response);

    let createdSessionId: string | undefined;
    let immutableSession: ImmutableSessionProjection | undefined;
    await consumeEventStream(response.body, {
      ...options,
      emptyStreamError: () => new CreationStreamRetryError(),
      expectedSessionId: () => createdSessionId,
      onParsedEvent: (event) => {
        if (createdSessionId === undefined) {
          const created = projectCreatedSessionEvent(event, expectedVaultIds, expectedEnvironment);
          const session = created.session;
          createdSessionId = session.id;
          immutableSession = immutableSessionProjection(session);
          options.onSession(session);
          options.onEvent(created.event);
          return;
        }

        options.onEvent(projectStreamEventSession(event, createdSessionId, immutableSession));
      },
    });
  }

  async retrieveSession(sessionId: string, options?: ReadOptions): Promise<AgentSession> {
    const value = await this.request<unknown>(`/agents/sessions/${encodeURIComponent(sessionId)}`, { signal: options?.signal });
    return projectAgentSession(value, undefined, sessionId);
  }

  async retrieveEnvironment(environmentId: string, options?: ReadOptions): Promise<AgentEnvironmentResource> {
    const value = await this.request<unknown>(
      `/agents/environments/${encodeURIComponent(environmentId)}`,
      { signal: options?.signal },
      200,
    );
    return projectEnvironmentResource(value, environmentId);
  }

  async listEnvironmentTemplates(options?: PageOptions & ReadOptions): Promise<EnvironmentTemplateList> {
    if (options?.limit !== undefined && (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100)) {
      throw new TypeError("Environment Template list limit must be an integer from 1 through 100.");
    }
    if (options?.order !== undefined && options.order !== "asc" && options.order !== "desc") {
      throw new TypeError("Environment Template list order must be asc or desc.");
    }
    const params = new URLSearchParams();
    addPageOptions(params, options);
    const value = await this.request<unknown>(
      withQuery("/agents/environments/templates", params),
      { signal: options?.signal },
      200,
    );
    return projectEnvironmentTemplateList(value, options);
  }

  async createEnvironmentTemplate(
    input: CreateEnvironmentTemplateInput,
    options?: ReadOptions,
  ): Promise<EnvironmentTemplate> {
    const body = environmentTemplateRequestBody(input);
    const value = await this.request<unknown>(
      "/agents/environments/templates",
      { method: "POST", body, signal: options?.signal },
      201,
    );
    const template = projectEnvironmentTemplate(value);
    const expectedName = input.name === undefined ? null : input.name;
    const expectedAccess = input.network == null ? "enabled" : input.network.access;
    if (template.name !== expectedName || template.network.access !== expectedAccess) {
      return invalidEnvironmentTemplate("Agent Core returned mismatched Environment Template configuration.");
    }
    return template;
  }

  async retrieveEnvironmentTemplate(templateId: string, options?: ReadOptions): Promise<EnvironmentTemplate> {
    const value = await this.request<unknown>(
      `/agents/environments/templates/${encodeURIComponent(templateId)}`,
      { signal: options?.signal },
      200,
    );
    return projectEnvironmentTemplate(value, templateId);
  }

  async updateEnvironmentTemplate(
    templateId: string,
    input: UpdateEnvironmentTemplateInput,
    options?: ReadOptions,
  ): Promise<EnvironmentTemplate> {
    const body = environmentTemplateRequestBody(input);
    const value = await this.request<unknown>(
      `/agents/environments/templates/${encodeURIComponent(templateId)}`,
      { method: "POST", body, signal: options?.signal },
      200,
    );
    const template = projectEnvironmentTemplate(value, templateId);
    const supplied = input as Record<string, unknown>;
    if (
      (hasOwn(supplied, "name") && template.name !== (input.name ?? null)) ||
      (hasOwn(supplied, "network") && template.network.access !== (input.network == null ? "enabled" : input.network.access))
    ) {
      return invalidEnvironmentTemplate("Agent Core returned mismatched Environment Template configuration.");
    }
    return template;
  }

  async deleteEnvironmentTemplate(
    templateId: string,
    options?: ReadOptions,
  ): Promise<EnvironmentTemplateDeleted> {
    const value = await this.request<unknown>(
      `/agents/environments/templates/${encodeURIComponent(templateId)}`,
      { method: "DELETE", signal: options?.signal },
      200,
    );
    if (
      !isRecord(value) || !exactFields(value, environmentTemplateDeletedFields) ||
      !sameUuid(value.id, templateId) ||
      value.object !== "agent.environment.template.deleted" || value.deleted !== true
    ) {
      return invalidEnvironmentTemplate("Agent Core returned an invalid Environment Template deletion receipt.");
    }
    return { id: value.id as string, object: "agent.environment.template.deleted", deleted: true };
  }

  async listEnvironmentFiles(
    environmentId: string,
    options: EnvironmentFileListOptions,
  ): Promise<EnvironmentFileList> {
    const requestedDirectory = options.path ?? "/workspace";
    if (canonicalEnvironmentFilesDirectory(requestedDirectory) === null) {
      throw new TypeError("Environment file listing requires an absolute directory without parent traversal or backslashes.");
    }
    const params = new URLSearchParams();
    if (options.path !== undefined) params.set("path", options.path);
    if (options.limit !== undefined) params.set("limit", String(options.limit));
    if (options.order !== undefined) params.set("order", options.order);
    if (options.page !== undefined) params.set("page", options.page);
    const value = await this.request<unknown>(
      withQuery(`/agents/environments/${encodeURIComponent(environmentId)}/files`, params),
      { signal: options.signal },
      200,
    );
    return projectEnvironmentFileList(value, environmentId, options);
  }

  async createEnvironmentFile(
    environmentId: string,
    input: EnvironmentFileCreateInput,
    options?: ReadOptions,
  ): Promise<EnvironmentFile> {
    if (!validEnvironmentFilePath(input.path)) {
      throw new TypeError("Environment file paths must be canonical absolute paths beneath /workspace.");
    }
    const fields = Object.keys(input);
    let expectedSize: number | undefined;
    if (input.type === "inline") {
      if (fields.length !== 3 || fields.some((field) => field !== "type" && field !== "data" && field !== "path")) {
        throw new TypeError("Inline Environment files accept only type, data, and path.");
      }
      const decodedBytes = strictBase64DecodedBytes(input.data);
      if (decodedBytes === null) throw new TypeError("Inline Environment file data must be strict standard Base64.");
      if (decodedBytes > maxEnvironmentFileBytes) throw new TypeError("Environment files must be at most 50 MiB.");
      expectedSize = decodedBytes;
    } else if (input.type === "file_id") {
      if (
        fields.length !== 3 ||
        fields.some((field) => field !== "type" && field !== "file_id" && field !== "path") ||
        !validSourceFileId(input.file_id)
      ) {
        throw new TypeError("Referenced Environment files require an exact Source File ID.");
      }
    } else {
      throw new TypeError("Unsupported Environment file input.");
    }
    const value = await this.request<unknown>(
      `/agents/environments/${encodeURIComponent(environmentId)}/files`,
      { method: "POST", body: JSON.stringify(input), signal: options?.signal },
      201,
    );
    return projectEnvironmentFile(value, environmentId, input.path, expectedSize);
  }

  async uploadSourceFile(input: SourceFileUploadInput, options?: ReadOptions): Promise<SourceFile> {
    const filenameBytes = new TextEncoder().encode(input.filename).length;
    if (
      !(input.file instanceof Blob) ||
      !Number.isSafeInteger(input.file.size) ||
      input.file.size < 0 ||
      input.file.size > maxSourceFileBytes ||
      filenameBytes < 1 ||
      filenameBytes > 1024 ||
      input.filename.includes("\0")
    ) {
      throw new TypeError("Source Files require a valid filename and at most 512 MiB of content.");
    }
    const body = new FormData();
    body.append("file", input.file, input.filename);
    body.append("purpose", "user_data");
    const value = await this.request<unknown>(
      "/files",
      { method: "POST", body, signal: options?.signal },
      200,
      false,
    );
    const sourceFile = projectSourceFile(value);
    if (sourceFile.filename !== input.filename || sourceFile.bytes !== input.file.size) {
      return invalidSourceFile();
    }
    return sourceFile;
  }

  async retrieveSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFile> {
    if (!validSourceFileId(fileId)) throw new TypeError("Invalid Source File ID.");
    const value = await this.request<unknown>(
      `/files/${encodeURIComponent(fileId)}`,
      { signal: options?.signal },
      200,
      false,
    );
    return projectSourceFile(value, fileId);
  }

  async downloadSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFileContent> {
    if (!validSourceFileId(fileId)) throw new TypeError("Invalid Source File ID.");
    const headers = this.headers({ Accept: "application/octet-stream" }, false);
    const response = await this.fetchImpl(
      `${this.baseUrl}/files/${encodeURIComponent(fileId)}/content`,
      { headers, signal: options?.signal },
    );
    if (!response.ok || response.status !== 200) throw await this.toError(response);
    const contentType = response.headers.get("Content-Type");
    const contentDisposition = response.headers.get("Content-Disposition");
    const contentLength = response.headers.get("Content-Length");
    const cacheControl = response.headers.get("Cache-Control");
    const nosniff = response.headers.get("X-Content-Type-Options");
    if (
      contentType !== "application/octet-stream" ||
      contentDisposition === null ||
      !/^attachment(?:;|$)/i.test(contentDisposition) ||
      contentLength === null ||
      !/^(?:0|[1-9][0-9]*)$/.test(contentLength) ||
      !Number.isSafeInteger(Number(contentLength)) ||
      Number(contentLength) > maxSourceFileBytes ||
      cacheControl !== "no-store" ||
      nosniff?.toLowerCase() !== "nosniff"
    ) {
      return invalidSourceFileContent("Agent Core returned invalid Source File content headers.");
    }
    const data = await readExactSourceFileBody(response, Number(contentLength));
    return {
      data,
      bytes: data.byteLength,
      content_type: "application/octet-stream",
      content_disposition: contentDisposition,
    };
  }

  async deleteSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFileDeleted> {
    if (!validSourceFileId(fileId)) throw new TypeError("Invalid Source File ID.");
    const value = await this.request<unknown>(
      `/files/${encodeURIComponent(fileId)}`,
      { method: "DELETE", signal: options?.signal },
      200,
      false,
    );
    return projectSourceFileDeleted(value, fileId);
  }

  async updateSession(sessionId: string, metadata: Record<string, string> | null): Promise<AgentSession> {
    const value = await this.request<unknown>(`/agents/sessions/${encodeURIComponent(sessionId)}`, {
      method: "POST",
      body: JSON.stringify({ metadata }),
    });
    return projectAgentSession(value, undefined, sessionId);
  }

  /**
   * Deletes a durably idle or failed Session. The owner's repeated deletion of
   * a deleted Session returns the same confirmation. A busy Session rejects with
   * an error matched by `isSessionDeletionConflict`.
   */
  deleteSession(sessionId: string): Promise<SessionDeleted> {
    return this.request(`/agents/sessions/${encodeURIComponent(sessionId)}`, { method: "DELETE" });
  }

  async listItems(sessionId: string, options?: PageOptions & ReadOptions): Promise<ListPage<SessionItem>> {
    validateHistoryPageOptions(options);
    const params = new URLSearchParams();
    addPageOptions(params, options);
    const value = await this.request<unknown>(withQuery(`/agents/sessions/${encodeURIComponent(sessionId)}/items`, params), {
      signal: options?.signal,
    });
    return projectHistoryPage(value, options, (entry) => projectSessionItem(entry, invalidHistoryResource), invalidHistoryResource);
  }

  async listTurns(sessionId: string, options?: PageOptions & ReadOptions): Promise<ListPage<AgentTurn>> {
    validateHistoryPageOptions(options);
    const params = new URLSearchParams();
    addPageOptions(params, options);
    const value = await this.request<unknown>(withQuery(`/agents/sessions/${encodeURIComponent(sessionId)}/turns`, params), {
      signal: options?.signal,
    });
    return projectHistoryPage(value, options, (entry) => projectAgentTurn(entry, sessionId, invalidHistoryResource), invalidHistoryResource);
  }

  async retrieveTurn(sessionId: string, turnId: string, options?: ReadOptions): Promise<AgentTurn> {
    const value = await this.request<unknown>(
      `/agents/sessions/${encodeURIComponent(sessionId)}/turns/${encodeURIComponent(turnId)}`,
      { signal: options?.signal },
    );
    return projectAgentTurn(value, sessionId, invalidHistoryResource, turnId);
  }

  submitEvents(
    sessionId: string,
    events: readonly SessionInputEvent[],
    idempotencyKey: string,
  ): Promise<void> {
    requireIdempotencyKey(idempotencyKey);
    const body = encodeSessionInputBatch(events);
    return this.request<void>(
      `/agents/sessions/${encodeURIComponent(sessionId)}/events`,
      {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body,
      },
      202,
    );
  }

  sendMessage(sessionId: string, text: string, idempotencyKey: string): Promise<void> {
    return this.submitEvents(
      sessionId,
      [
        {
          type: "agent.session.input.message",
          input: [
            {
              role: "user",
              content: [{ type: "input_text", text }],
            },
          ],
        },
      ],
      idempotencyKey,
    );
  }

  cancelTurn(sessionId: string, idempotencyKey: string): Promise<void> {
    return this.submitEvents(sessionId, [{ type: "agent.session.input.cancel" }], idempotencyKey);
  }

  submitFunctionResult(sessionId: string, input: FunctionResultInput, idempotencyKey: string): Promise<void> {
    const event: SessionToolResultInputEvent = {
      type: "agent.session.input.tool_result",
      call_id: input.callId,
      turn_id: input.turnId,
      success: input.success,
    };
    if (input.output !== undefined) event.output = input.output;
    if (input.error !== undefined) event.error = input.error;
    return this.submitEvents(
      sessionId,
      [event],
      idempotencyKey,
    );
  }

  async streamEvents(sessionId: string, options: StreamOptions): Promise<void> {
    const headers = this.headers({ Accept: "text/event-stream" });
    const response = await this.fetchImpl(
      `${this.baseUrl}/agents/sessions/${encodeURIComponent(sessionId)}/events`,
      { headers, signal: options.signal },
    );
    if (!response.ok) throw await this.toError(response);
    await consumeEventStream(response.body, {
      ...options,
      expectedSessionId: () => sessionId,
      onParsedEvent: (event) => options.onEvent(projectStreamEventSession(event, sessionId)),
    });
  }
}
