/**
 * Management-plane client for the Web console (`/core/v1/admin/**`).
 *
 * The console is an administration tool, not an Agents API caller: it never
 * calls `/v1`. Each API key owns one isolated asset space (Core models it as a
 * user with a tenant). Reads of a space's resources return objects that are
 * byte-identical to the public `/v1` responses, so `scopeClient()` reuses the
 * public client's strict projections and only rewrites paths. Only GET and
 * DELETE are admitted through a scope: the console views, deletes and copies
 * assets but never creates or edits them on a caller's behalf.
 */
import {
  AgentCoreError,
  OpenAIAgentsClient,
  projectRuntimeObservation,
  projectStartupConfiguration,
  trimTrailingSlash,
} from "./client";
import type { CoreStartupConfiguration, RuntimeObservation } from "./types";

export interface AdminClientOptions {
  /** Defaults to the same-origin console route. */
  baseUrl?: string;
  fetch?: typeof fetch;
}

export type KeySpaceStatus = "active" | "disabled";

/** One issued key of a space. The plaintext is never listed. */
export interface AdminKey {
  id: string;
  name: string | null;
  prefix: string;
  /** Unix seconds. */
  created_at: number;
  revoked_at: number | null;
}

export interface AdminIssuedKey extends AdminKey {
  /** Returned exactly once, by the issuing response. */
  key: string;
}

/** An isolated asset space reached with one API key (Core's `api_users`). */
export interface KeySpace {
  id: string;
  /** The space's name; also the name shown for its key. Immutable. */
  username: string;
  status: KeySpaceStatus;
  created_at: number;
  disabled_at: number | null;
  /** Unrevoked keys. Usually one; two while a rotation is in progress. */
  active_keys: AdminKey[];
}

export interface WriteOperationKey {
  id: string;
  name: string | null;
  prefix: string | null;
  kind: "issued" | "static" | "console";
  revoked_at: number | null;
}

export interface WriteOperation {
  id: string;
  created_at: number;
  /** null: an administrator copy or an unrecorded origin. */
  api_key: WriteOperationKey | null;
  action: string;
  resource_type: string;
  resource_id: string;
  /** Empty when the resource has no parent. */
  parent_id: string;
  request_id: string;
  trace_id: string;
}

export interface WriteOperationPage {
  data: WriteOperation[];
  has_more: boolean;
  next_cursor: string;
}

export interface WriteOperationQuery {
  key_id?: string;
  resource_type?: string;
  resource_id?: string;
  created_after?: string;
  created_before?: string;
  after?: string;
  limit?: number;
  signal?: AbortSignal;
}

export const copyableResourceTypes = ["agent", "skill", "environment_template", "file", "vault", "credential"] as const;
export type CopyableResourceType = (typeof copyableResourceTypes)[number];

export interface CopyRequest {
  source_user_id: string;
  target_user_id: string;
  resource_type: CopyableResourceType;
  resource_id: string;
  include_dependencies: boolean;
  /** Required when copying a single Credential: the target space's Vault. */
  target_vault_id?: string;
}

export interface CopyResult {
  mappings: Array<{ type: string; source_id: string; target_id: string }>;
  skipped: Array<{ type: string; source_id: string; reason: string }>;
}

export interface SpaceUsage {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
}

/** One row of `/summary`: a space, or one Agent of a space with `group_by=agent`. */
export interface SpaceSummary {
  user_id: string;
  agent_id: string | null;
  /** Asset counts; null for Agent rows. */
  assets: { agents: number; skills: number; environment_templates: number; files: number; vaults: number } | null;
  sessions: { total: number; idle: number; in_progress: number; requires_action: number; failed: number };
  /** Sum over Sessions that reported usage; null when none did. */
  usage: SpaceUsage | null;
  /** Sessions in range, and how many reported usage (coverage denominator/numerator). */
  coverage: { sessions: number; reported: number };
  last_active_at: number | null;
}

export interface SummaryQuery {
  created_after?: number;
  created_before?: number;
  group_by?: "agent";
  user_id?: string;
  signal?: AbortSignal;
}

export type OwnedRuntimeObservation = RuntimeObservation & { user_id: string };

const idPattern = /^[A-Za-z0-9_.:-]{1,128}$/;
const usernamePattern = /^[a-z0-9._-]{1,64}$/;

export function isKeySpaceName(value: string): boolean {
  return usernamePattern.test(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function invalid(message: string): never {
  throw new AgentCoreError(message, 502, "invalid_admin_response");
}

/** Admin objects may carry Unix seconds or RFC 3339 timestamps; both normalise to seconds. */
function seconds(value: unknown): number | null | undefined {
  if (value === null || value === undefined) return null;
  if (typeof value === "number" && Number.isSafeInteger(value) && value >= 0) return value;
  if (typeof value === "string") {
    const parsed = Date.parse(value);
    if (Number.isFinite(parsed)) return Math.floor(parsed / 1000);
  }
  return undefined;
}

function count(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function optionalText(value: unknown, max: number): string | null | undefined {
  if (value === null || value === undefined || value === "") return null;
  return typeof value === "string" && value.length <= max ? value : undefined;
}

export function projectAdminKey(value: unknown): AdminKey {
  if (!isRecord(value) || typeof value.id !== "string" || !idPattern.test(value.id)) invalid("Core returned an invalid API key.");
  const name = optionalText(value.name, 80);
  const created = seconds(value.created_at);
  const revoked = seconds(value.revoked_at);
  if (typeof value.prefix !== "string" || !value.prefix || value.prefix.length > 32 || name === undefined || created == null || revoked === undefined) {
    invalid("Core returned an invalid API key.");
  }
  return { id: value.id, name, prefix: value.prefix, created_at: created, revoked_at: revoked };
}

export function projectKeySpace(value: unknown): KeySpace {
  if (!isRecord(value) || typeof value.id !== "string" || !idPattern.test(value.id)) invalid("Core returned an invalid key space.");
  if (typeof value.username !== "string" || !isKeySpaceName(value.username)) invalid("Core returned an invalid key space name.");
  const created = seconds(value.created_at);
  const disabled = seconds(value.disabled_at);
  if (created == null || disabled === undefined) invalid("Core returned an invalid key space.");
  const status: KeySpaceStatus = value.status === "disabled" || disabled !== null ? "disabled" : "active";
  if (value.status !== undefined && value.status !== "active" && value.status !== "disabled") invalid("Core returned an invalid key space status.");
  const keys = value.active_keys === undefined ? [] : Array.isArray(value.active_keys) ? value.active_keys.map(projectAdminKey) : invalid("Core returned invalid active keys.");
  return { id: value.id, username: value.username, status, created_at: created, disabled_at: disabled, active_keys: keys };
}

function projectOperationKey(value: unknown): WriteOperationKey | null {
  if (value === null) return null;
  if (!isRecord(value) || typeof value.id !== "string" || !value.id || value.id.length > 128) return invalid("Core returned an invalid write operation key.");
  const kind = value.kind === "issued" || value.kind === "static" || value.kind === "console" ? value.kind : invalid("Core returned an invalid key kind.");
  const name = optionalText(value.name, 80);
  const prefix = optionalText(value.prefix, 32);
  const revoked = seconds(value.revoked_at);
  if (name === undefined || prefix === undefined || revoked === undefined) invalid("Core returned an invalid write operation key.");
  return { id: value.id, name, prefix, kind, revoked_at: revoked };
}

export function projectWriteOperationPage(value: unknown): WriteOperationPage {
  if (!isRecord(value) || !Array.isArray(value.data) || typeof value.has_more !== "boolean") invalid("Core returned an invalid write operation page.");
  const data = value.data.map((entry): WriteOperation => {
    if (!isRecord(entry) || typeof entry.id !== "string" || !entry.id) invalid("Core returned an invalid write operation.");
    const created = seconds(entry.created_at);
    if (created == null || typeof entry.action !== "string" || !entry.action || typeof entry.resource_type !== "string" || !entry.resource_type
      || typeof entry.resource_id !== "string" || !entry.resource_id) invalid("Core returned an invalid write operation.");
    const text = (field: unknown) => (typeof field === "string" ? field : field == null ? "" : invalid("Core returned an invalid write operation."));
    return {
      id: entry.id,
      created_at: created,
      api_key: projectOperationKey(entry.api_key ?? null),
      action: entry.action,
      resource_type: entry.resource_type,
      resource_id: entry.resource_id,
      parent_id: text(entry.parent_id),
      request_id: text(entry.request_id),
      trace_id: text(entry.trace_id),
    };
  });
  const cursor = typeof value.next_cursor === "string" ? value.next_cursor : "";
  return { data, has_more: value.has_more && cursor !== "", next_cursor: cursor };
}

function projectUsage(value: unknown): SpaceUsage | null {
  if (value === null || value === undefined) return null;
  if (!isRecord(value)) return invalid("Core returned invalid usage.");
  const fields = ["input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens"] as const;
  const usage = {} as SpaceUsage;
  for (const field of fields) {
    const number = count(value[field]);
    if (number === undefined) invalid("Core returned invalid usage.");
    usage[field] = number;
  }
  return usage;
}

export function projectSummary(value: unknown): SpaceSummary[] {
  if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid summary.");
  return value.data.map((entry): SpaceSummary => {
    if (!isRecord(entry) || typeof entry.user_id !== "string" || !idPattern.test(entry.user_id)) invalid("Core returned an invalid summary row.");
    const agentId = entry.agent_id === undefined || entry.agent_id === null ? null : typeof entry.agent_id === "string" && idPattern.test(entry.agent_id) ? entry.agent_id : invalid("Core returned an invalid summary row.");
    let assets: SpaceSummary["assets"] = null;
    if (entry.assets !== undefined && entry.assets !== null) {
      if (!isRecord(entry.assets)) invalid("Core returned invalid asset counts.");
      const read = (field: string) => count((entry.assets as Record<string, unknown>)[field]) ?? invalid("Core returned invalid asset counts.");
      assets = { agents: read("agents"), skills: read("skills"), environment_templates: read("environment_templates"), files: read("files"), vaults: read("vaults") };
    }
    if (!isRecord(entry.sessions) || !isRecord(entry.coverage)) invalid("Core returned an invalid summary row.");
    const sessions = entry.sessions as Record<string, unknown>;
    const coverage = entry.coverage as Record<string, unknown>;
    const sessionCount = (field: string) => count(sessions[field]) ?? invalid("Core returned invalid Session counts.");
    const lastActive = seconds(entry.last_active_at);
    const covered = count(coverage.sessions);
    const reported = count(coverage.reported);
    if (lastActive === undefined || covered === undefined || reported === undefined || reported > covered) invalid("Core returned an invalid summary row.");
    return {
      user_id: entry.user_id,
      agent_id: agentId,
      assets,
      sessions: { total: sessionCount("total"), idle: sessionCount("idle"), in_progress: sessionCount("in_progress"), requires_action: sessionCount("requires_action"), failed: sessionCount("failed") },
      usage: projectUsage(entry.usage),
      coverage: { sessions: covered, reported },
      last_active_at: lastActive,
    };
  });
}

export function projectCopyResult(value: unknown): CopyResult {
  if (!isRecord(value) || !Array.isArray(value.mappings) || !Array.isArray(value.skipped)) invalid("Core returned an invalid copy result.");
  const text = (field: unknown) => (typeof field === "string" && field ? field : invalid("Core returned an invalid copy result."));
  return {
    mappings: value.mappings.map((entry) => {
      if (!isRecord(entry)) return invalid("Core returned an invalid copy result.");
      return { type: text(entry.type), source_id: text(entry.source_id), target_id: text(entry.target_id) };
    }),
    skipped: value.skipped.map((entry) => {
      if (!isRecord(entry)) return invalid("Core returned an invalid copy result.");
      return { type: text(entry.type), source_id: text(entry.source_id), reason: text(entry.reason) };
    }),
  };
}

/** Maps a public client path (relative to `/v1`) onto a space's admin path, or null when the admin API offers no equivalent. */
export function adminScopePath(path: string): string | null {
  const matches = (prefix: string) => path === prefix || path.startsWith(`${prefix}/`) || path.startsWith(`${prefix}?`);
  if (matches("/agents/sessions")) return `/sessions${path.slice("/agents/sessions".length)}`;
  if (matches("/agents/environments/templates")) return `/environment-templates${path.slice("/agents/environments/templates".length)}`;
  // Deployment-level reads and Environment resources are not offered per space.
  if (path.startsWith("/agents/core/") || path.startsWith("/agents/runtime-") || path.startsWith("/agents/environments")) return null;
  if (matches("/agents") || matches("/skills") || matches("/files") || matches("/vaults")) return path;
  return null;
}

function scopedFetch(scopeBase: string, inner: typeof fetch): typeof fetch {
  return async (input, init) => {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.pathname + input.search : input.url;
    const path = raw.startsWith(SCOPE_MARKER) ? adminScopePath(raw.slice(SCOPE_MARKER.length)) : null;
    const method = (init?.method ?? "GET").toUpperCase();
    if (path === null) {
      return new Response(JSON.stringify({ error: { message: "This operation is not offered by the console.", code: "unsupported_in_console", type: "invalid_request_error", param: null } }), { status: 404, headers: { "Content-Type": "application/json" } });
    }
    if (method !== "GET" && method !== "DELETE" && method !== "HEAD") {
      // The console never creates or edits assets on a caller's behalf.
      return new Response(JSON.stringify({ error: { message: "The console only views, deletes and copies assets.", code: "console_read_only", type: "invalid_request_error", param: null } }), { status: 405, headers: { "Content-Type": "application/json" } });
    }
    const headers = new Headers(init?.headers);
    headers.delete("OpenAI-Beta");
    headers.delete("Authorization");
    return inner(`${scopeBase}${path}`, { ...init, headers, credentials: "same-origin" });
  };
}

const SCOPE_MARKER = "/__key_space__";

export class AdminClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;

  constructor(options: AdminClientOptions = {}) {
    this.baseUrl = trimTrailingSlash(options.baseUrl ?? "/core/v1/admin");
    this.fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis);
  }

  /** A read/delete client over one key space, reusing the public projections. */
  scopeClient(userId: string): OpenAIAgentsClient {
    if (!idPattern.test(userId)) throw new TypeError("A key space ID is required.");
    return new OpenAIAgentsClient({ baseUrl: SCOPE_MARKER, fetch: scopedFetch(`${this.baseUrl}/users/${encodeURIComponent(userId)}`, this.fetchImpl) });
  }

  private async request(path: string, init: RequestInit = {}, expected?: number): Promise<unknown> {
    const headers = new Headers(init.headers);
    if (init.body !== undefined && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, { ...init, headers, credentials: "same-origin", cache: "no-store" });
    if (!response.ok || (expected !== undefined && response.status !== expected)) {
      let message = `Core returned HTTP ${response.status}.`;
      let code: string | null = null;
      try {
        const body = await response.json() as { error?: { message?: unknown; code?: unknown } };
        if (typeof body.error?.message === "string") message = body.error.message;
        if (typeof body.error?.code === "string") code = body.error.code;
      } catch { /* keep the status-only message */ }
      throw new AgentCoreError(message, response.status, code ?? undefined);
    }
    if (response.status === 204) return undefined;
    return response.json();
  }

  async listKeySpaces(options: { signal?: AbortSignal } = {}): Promise<KeySpace[]> {
    const spaces: KeySpace[] = [];
    let after: string | null = null;
    // Bounded walk: a deployment has a handful of spaces; stop at 10,000.
    for (let page = 0; page < 100; page += 1) {
      const query: string = after ? `?limit=100&after=${encodeURIComponent(after)}` : "?limit=100";
      const value = await this.request(`/users${query}`, { signal: options.signal });
      if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid key space list.");
      spaces.push(...value.data.map(projectKeySpace));
      const last = typeof value.last_id === "string" && value.last_id ? value.last_id : spaces.at(-1)?.id ?? null;
      if (value.has_more !== true || !last) return spaces;
      after = last;
    }
    return spaces;
  }

  async createKeySpace(username: string, options: { signal?: AbortSignal } = {}): Promise<KeySpace> {
    if (!isKeySpaceName(username)) throw new TypeError("Names use 1–64 characters from a–z, 0–9, dot, underscore and hyphen.");
    return projectKeySpace(await this.request("/users", { method: "POST", body: JSON.stringify({ username }), signal: options.signal }));
  }

  async disableKeySpace(userId: string, options: { signal?: AbortSignal } = {}): Promise<KeySpace> {
    return projectKeySpace(await this.request(`/users/${encodeURIComponent(userId)}/disable`, { method: "POST", signal: options.signal }));
  }

  async listKeys(userId: string, options: { signal?: AbortSignal } = {}): Promise<AdminKey[]> {
    const value = await this.request(`/users/${encodeURIComponent(userId)}/keys`, { signal: options.signal });
    if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid key list.");
    return value.data.map(projectAdminKey);
  }

  /** Issues a key; the plaintext is in this response only and must not be stored. */
  async issueKey(userId: string, options: { name?: string; signal?: AbortSignal } = {}): Promise<AdminIssuedKey> {
    const value = await this.request(`/users/${encodeURIComponent(userId)}/keys`, { method: "POST", body: JSON.stringify(options.name ? { name: options.name } : {}), signal: options.signal });
    const key = projectAdminKey(value);
    const secret = isRecord(value) ? value.key : undefined;
    if (typeof secret !== "string" || secret.length < 20 || secret.length > 256 || /\s/.test(secret)) invalid("Core returned an invalid issued key.");
    return { ...key, key: secret };
  }

  async revokeKey(userId: string, keyId: string, options: { signal?: AbortSignal } = {}): Promise<void> {
    await this.request(`/users/${encodeURIComponent(userId)}/keys/${encodeURIComponent(keyId)}`, { method: "DELETE", signal: options.signal });
  }

  async listWriteOperations(userId: string, query: WriteOperationQuery = {}): Promise<WriteOperationPage> {
    const params = new URLSearchParams();
    for (const field of ["key_id", "resource_type", "resource_id", "created_after", "created_before", "after"] as const) {
      const value = query[field];
      if (value) params.set(field, value);
    }
    params.set("limit", String(query.limit ?? 50));
    return projectWriteOperationPage(await this.request(`/users/${encodeURIComponent(userId)}/write-operations?${params}`, { signal: query.signal }));
  }

  async summary(query: SummaryQuery = {}): Promise<SpaceSummary[]> {
    const params = new URLSearchParams();
    if (query.created_after !== undefined) params.set("created_after", new Date(query.created_after * 1000).toISOString());
    if (query.created_before !== undefined) params.set("created_before", new Date(query.created_before * 1000).toISOString());
    if (query.group_by) params.set("group_by", query.group_by);
    if (query.user_id) params.set("user_id", query.user_id);
    const suffix = params.toString() ? `?${params}` : "";
    return projectSummary(await this.request(`/summary${suffix}`, { signal: query.signal }));
  }

  /** Copies an asset (optionally with its dependencies) into another space as independent copies. */
  async copy(input: CopyRequest, options: { idempotencyKey: string; signal?: AbortSignal }): Promise<CopyResult> {
    if (input.source_user_id === input.target_user_id) throw new TypeError("Choose a different target space.");
    return projectCopyResult(await this.request("/copies", {
      method: "POST",
      body: JSON.stringify(input),
      headers: { "Idempotency-Key": options.idempotencyKey },
      signal: options.signal,
    }));
  }

  /** Runtime snapshots of every space, each labelled with its space. */
  async listRuntimeObservations(options: { signal?: AbortSignal } = {}): Promise<OwnedRuntimeObservation[]> {
    const value = await this.request("/runtime-observations?limit=1000", { signal: options.signal });
    if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid runtime observation list.");
    return value.data.map((entry) => {
      if (!isRecord(entry) || typeof entry.user_id !== "string" || !idPattern.test(entry.user_id)) return invalid("Core returned an invalid runtime observation.");
      const { user_id: userId, ...observation } = entry;
      return { ...projectRuntimeObservation(observation), user_id: userId };
    });
  }

  async retrieveStartupConfiguration(options: { signal?: AbortSignal } = {}): Promise<CoreStartupConfiguration> {
    return projectStartupConfiguration(await this.request("/startup-configuration", { signal: options.signal }));
  }
}
