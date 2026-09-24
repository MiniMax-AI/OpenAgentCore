/**
 * Management-plane client for the Web console (`/core/v1/admin/**`).
 *
 * The console is an administration tool, not an Agents API caller: it never
 * calls `/v1`. A project owns an isolated set of assets (a Core tenant) shared
 * by all of its named API keys. Reads of a project's resources return objects
 * that are byte-identical to the public `/v1` responses, so `scopeClient()`
 * reuses the public client's strict projections and only rewrites paths. Only
 * GET and DELETE are admitted through a scope: the console views, deletes and
 * copies assets but never creates or edits them on a caller's behalf.
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

export type ProjectStatus = "active" | "archived";
export type ProjectSource = "console" | "config";

/** One named API key of a project. The plaintext is never listed. */
export interface AdminKey {
  id: string;
  name: string;
  prefix: string;
  /** Unix seconds. */
  created_at: number;
  revoked_at: number | null;
}

export interface AdminIssuedKey extends AdminKey {
  /** Returned exactly once, by the issuing response. */
  key: string;
}

/** The unit of asset ownership: a Core tenant shared by all of its keys. */
export interface Project {
  id: string;
  name: string;
  /** `config` projects come from the static key file and cannot be renamed or archived here. */
  source: ProjectSource;
  status: ProjectStatus;
  created_at: number;
  archived_at: number | null;
  active_key_count: number;
}

/** The key recorded by #87 for a write; also the "creator" of a resource. */
export interface KeyRef {
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
  api_key: KeyRef | null;
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

export const ownerResourceTypes = ["agent", "session", "environment", "environment_template", "skill", "skill_version", "file", "vault", "credential", "artifact"] as const;
export type OwnerResourceType = (typeof ownerResourceTypes)[number];

export const copyableResourceTypes = ["agent", "skill", "environment_template", "file", "vault", "credential"] as const;
export type CopyableResourceType = (typeof copyableResourceTypes)[number];

export interface CopyRequest {
  source_project_id: string;
  target_project_id: string;
  resource_type: CopyableResourceType;
  resource_id: string;
  include_dependencies: boolean;
  /** Required when copying a single Credential: the target project's Vault. */
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

/** One row of `/summary`: a project (default), one Agent (`group_by=agent`) or one creating key (`group_by=key`). */
export interface ProjectSummary {
  project_id: string;
  agent_id: string | null;
  /** Key rows only: the creating key, or null when Sessions have no provenance ("unknown"). */
  key: KeyRef | null;
  /** Asset counts; null for Agent and key rows. */
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
  group_by?: "project" | "agent" | "key";
  project_id?: string;
  signal?: AbortSignal;
}

export type OwnedRuntimeObservation = RuntimeObservation & { project_id: string };

const idPattern = /^[A-Za-z0-9_.:-]{1,128}$/;

export function isProjectName(value: string): boolean {
  const trimmed = value.trim();
  return trimmed === value && trimmed.length >= 1 && [...trimmed].length <= 128 && !/[\u0000-\u001f]/.test(trimmed);
}

export function isKeyName(value: string): boolean {
  const trimmed = value.trim();
  return trimmed === value && trimmed.length >= 1 && [...trimmed].length <= 80 && !/[\u0000-\u001f]/.test(trimmed);
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
  const created = seconds(value.created_at);
  const revoked = seconds(value.revoked_at);
  if (typeof value.name !== "string" || !value.name || [...value.name].length > 80 || typeof value.prefix !== "string" || !value.prefix || value.prefix.length > 32 || created == null || revoked === undefined) {
    invalid("Core returned an invalid API key.");
  }
  return { id: value.id, name: value.name, prefix: value.prefix, created_at: created, revoked_at: revoked };
}

export function projectProject(value: unknown): Project {
  if (!isRecord(value) || typeof value.id !== "string" || !idPattern.test(value.id)) invalid("Core returned an invalid project.");
  if (typeof value.name !== "string" || !value.name.trim() || [...value.name].length > 128) invalid("Core returned an invalid project name.");
  const created = seconds(value.created_at);
  const archived = seconds(value.archived_at);
  const keys = count(value.active_key_count);
  if (created == null || archived === undefined || keys === undefined) invalid("Core returned an invalid project.");
  if (value.source !== "console" && value.source !== "config") invalid("Core returned an invalid project source.");
  if (value.status !== undefined && value.status !== "active" && value.status !== "archived") invalid("Core returned an invalid project status.");
  const status: ProjectStatus = value.status === "archived" || archived !== null ? "archived" : "active";
  return { id: value.id, name: value.name, source: value.source, status, created_at: created, archived_at: archived, active_key_count: keys };
}

function projectOperationKey(value: unknown): KeyRef | null {
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

export function projectSummary(value: unknown): ProjectSummary[] {
  if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid summary.");
  return value.data.map((entry): ProjectSummary => {
    if (!isRecord(entry) || typeof entry.project_id !== "string" || !idPattern.test(entry.project_id)) invalid("Core returned an invalid summary row.");
    const agentId = entry.agent_id === undefined || entry.agent_id === null ? null : typeof entry.agent_id === "string" && idPattern.test(entry.agent_id) ? entry.agent_id : invalid("Core returned an invalid summary row.");
    let assets: ProjectSummary["assets"] = null;
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
      project_id: entry.project_id,
      agent_id: agentId,
      key: entry.key === undefined ? null : projectOperationKey(entry.key),
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

/** #87 batch ownership for one project: resource ID → creating key (null: unknown). */
export function projectResourceOwners(value: unknown, requested: readonly string[]): Map<string, KeyRef | null> {
  if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid ownership list.");
  const wanted = new Set(requested);
  const owners = new Map<string, KeyRef | null>();
  for (const entry of value.data) {
    if (!isRecord(entry) || typeof entry.resource_id !== "string" || !wanted.has(entry.resource_id)) continue;
    try {
      owners.set(entry.resource_id, projectOperationKey(entry.api_key ?? null));
    } catch {
      // One malformed owner stays unknown; the others still show.
      owners.set(entry.resource_id, null);
    }
  }
  return owners;
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

const SCOPE_MARKER = "/__project_scope__";

export class AdminClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;

  constructor(options: AdminClientOptions = {}) {
    this.baseUrl = trimTrailingSlash(options.baseUrl ?? "/core/v1/admin");
    this.fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis);
  }

  /** A read/delete client over one project, reusing the public projections. */
  scopeClient(projectId: string): OpenAIAgentsClient {
    if (!idPattern.test(projectId)) throw new TypeError("A project ID is required.");
    return new OpenAIAgentsClient({ baseUrl: SCOPE_MARKER, fetch: scopedFetch(`${this.baseUrl}/projects/${encodeURIComponent(projectId)}`, this.fetchImpl) });
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

  async listProjects(options: { signal?: AbortSignal } = {}): Promise<Project[]> {
    const projects: Project[] = [];
    let after: string | null = null;
    // Bounded walk: a deployment has a handful of projects; stop at 10,000.
    for (let page = 0; page < 100; page += 1) {
      const query: string = after ? `?limit=100&after=${encodeURIComponent(after)}` : "?limit=100";
      const value = await this.request(`/projects${query}`, { signal: options.signal });
      if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid project list.");
      projects.push(...value.data.map(projectProject));
      const last = typeof value.last_id === "string" && value.last_id ? value.last_id : projects.at(-1)?.id ?? null;
      if (value.has_more !== true || !last) return projects;
      after = last;
    }
    return projects;
  }

  async createProject(name: string, options: { signal?: AbortSignal } = {}): Promise<Project> {
    if (!isProjectName(name)) throw new TypeError("A project name has 1–128 characters without leading or trailing spaces.");
    return projectProject(await this.request("/projects", { method: "POST", body: JSON.stringify({ name }), signal: options.signal }));
  }

  async renameProject(projectId: string, name: string, options: { signal?: AbortSignal } = {}): Promise<Project> {
    if (!isProjectName(name)) throw new TypeError("A project name has 1–128 characters without leading or trailing spaces.");
    return projectProject(await this.request(`/projects/${encodeURIComponent(projectId)}`, { method: "POST", body: JSON.stringify({ name }), signal: options.signal }));
  }

  /** Revokes every key of the project; its assets stay viewable and copyable. */
  async archiveProject(projectId: string, options: { signal?: AbortSignal } = {}): Promise<Project> {
    return projectProject(await this.request(`/projects/${encodeURIComponent(projectId)}/archive`, { method: "POST", signal: options.signal }));
  }

  async listKeys(projectId: string, options: { signal?: AbortSignal } = {}): Promise<AdminKey[]> {
    const value = await this.request(`/projects/${encodeURIComponent(projectId)}/keys`, { signal: options.signal });
    if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid key list.");
    return value.data.map(projectAdminKey);
  }

  /** Issues a named key; the plaintext is in this response only and must not be stored. */
  async issueKey(projectId: string, options: { name: string; signal?: AbortSignal }): Promise<AdminIssuedKey> {
    if (!isKeyName(options.name)) throw new TypeError("A key name has 1–80 characters without leading or trailing spaces.");
    const value = await this.request(`/projects/${encodeURIComponent(projectId)}/keys`, { method: "POST", body: JSON.stringify({ name: options.name }), signal: options.signal });
    const key = projectAdminKey(value);
    const secret = isRecord(value) ? value.key : undefined;
    if (typeof secret !== "string" || secret.length < 20 || secret.length > 256 || /\s/.test(secret)) invalid("Core returned an invalid issued key.");
    return { ...key, key: secret };
  }

  async revokeKey(projectId: string, keyId: string, options: { signal?: AbortSignal } = {}): Promise<void> {
    await this.request(`/projects/${encodeURIComponent(projectId)}/keys/${encodeURIComponent(keyId)}`, { method: "DELETE", signal: options.signal });
  }

  /** Creating keys of up to any number of resources, in batches of 100 IDs. */
  async listResourceOwners(projectId: string, resourceType: OwnerResourceType, ids: readonly string[], options: { signal?: AbortSignal } = {}): Promise<Map<string, KeyRef | null>> {
    const unique = [...new Set(ids)].filter((id) => idPattern.test(id));
    const owners = new Map<string, KeyRef | null>();
    for (let start = 0; start < unique.length; start += 100) {
      const batch = unique.slice(start, start + 100);
      const params = new URLSearchParams({ resource_type: resourceType, resource_ids: batch.join(",") });
      const value = await this.request(`/projects/${encodeURIComponent(projectId)}/resource-owners?${params}`, { signal: options.signal });
      for (const [id, owner] of projectResourceOwners(value, batch)) owners.set(id, owner);
    }
    return owners;
  }

  async listWriteOperations(projectId: string, query: WriteOperationQuery = {}): Promise<WriteOperationPage> {
    const params = new URLSearchParams();
    for (const field of ["key_id", "resource_type", "resource_id", "created_after", "created_before", "after"] as const) {
      const value = query[field];
      if (value) params.set(field, value);
    }
    params.set("limit", String(query.limit ?? 50));
    return projectWriteOperationPage(await this.request(`/projects/${encodeURIComponent(projectId)}/write-operations?${params}`, { signal: query.signal }));
  }

  async summary(query: SummaryQuery = {}): Promise<ProjectSummary[]> {
    const params = new URLSearchParams();
    if (query.created_after !== undefined) params.set("created_after", new Date(query.created_after * 1000).toISOString());
    if (query.created_before !== undefined) params.set("created_before", new Date(query.created_before * 1000).toISOString());
    if (query.group_by) params.set("group_by", query.group_by);
    if (query.project_id) params.set("project_id", query.project_id);
    const suffix = params.toString() ? `?${params}` : "";
    return projectSummary(await this.request(`/summary${suffix}`, { signal: query.signal }));
  }

  /** Copies an asset (optionally with its dependencies) into another project as independent copies. */
  async copy(input: CopyRequest, options: { idempotencyKey: string; signal?: AbortSignal }): Promise<CopyResult> {
    if (input.source_project_id === input.target_project_id) throw new TypeError("Choose a different target project.");
    return projectCopyResult(await this.request("/copies", {
      method: "POST",
      body: JSON.stringify(input),
      headers: { "Idempotency-Key": options.idempotencyKey },
      signal: options.signal,
    }));
  }

  /** Runtime snapshots of every project, each labelled with its project. */
  async listRuntimeObservations(options: { signal?: AbortSignal } = {}): Promise<OwnedRuntimeObservation[]> {
    const value = await this.request("/runtime-observations?limit=1000", { signal: options.signal });
    if (!isRecord(value) || !Array.isArray(value.data)) invalid("Core returned an invalid runtime observation list.");
    return value.data.map((entry) => {
      if (!isRecord(entry) || typeof entry.project_id !== "string" || !idPattern.test(entry.project_id)) return invalid("Core returned an invalid runtime observation.");
      const { project_id: projectId, ...observation } = entry;
      return { ...projectRuntimeObservation(observation), project_id: projectId };
    });
  }

  async retrieveStartupConfiguration(options: { signal?: AbortSignal } = {}): Promise<CoreStartupConfiguration> {
    return projectStartupConfiguration(await this.request("/startup-configuration", { signal: options.signal }));
  }
}
