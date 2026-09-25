import {
  AdminClient,
  type AdminAPIKey,
  type AdminKeyProvenance,
  type AdminProject,
  type AdminResourceType,
  type AdminSummaryEntry,
  type AdminWriteOperation,
  type RuntimeObservation,
} from "@agents-core-web/agents-client";

/**
 * View models over the typed management client (`AdminClient`, `/core/v1/admin`).
 * Pages format Unix seconds and never treat missing data as zero, so this
 * layer converts RFC 3339 timestamps, resolves creating keys by name, walks
 * cursor pages and turns an unmeasured usage sum into `null`.
 */
export const admin = new AdminClient();

export type ProjectStatus = "active" | "archived";

export interface Project {
  id: string;
  name: string;
  status: ProjectStatus;
  /** Unix seconds. */
  created_at: number;
  archived_at: number | null;
  active_key_count: number;
}

export interface AdminKey {
  id: string;
  project_id: string;
  name: string;
  prefix: string;
  created_at: number;
  revoked_at: number | null;
}

export interface AdminIssuedKey extends AdminKey {
  /** Plaintext returned once by issuance; never stored. */
  key: string;
}

/** The key recorded for a write or a resource's creation. */
export interface KeyRef {
  id: string;
  name: string | null;
  prefix: string | null;
  kind: "issued" | "static" | "console";
  revoked_at: number | null;
}

/** Who created a resource: a key, an administrator copy, or unknown. */
export interface Creator {
  key: KeyRef | null;
  source: "api_key" | "admin_copy" | null;
}

export interface SpaceUsage {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
}

export interface ProjectSummary {
  project_id: string;
  agent_id: string | null;
  /** Key rows: the creating key's ID; null groups Sessions with unknown provenance. */
  key_id: string | null;
  /** Key rows: the creating key resolved by name, or null when unknown. */
  key: KeyRef | null;
  assets: { agents: number; skills: number; environment_templates: number; files: number; vaults: number; credentials: number } | null;
  sessions: { total: number; idle: number; in_progress: number; requires_action: number; failed: number };
  /** null when no Session in the group reported usage. */
  usage: SpaceUsage | null;
  /** Sessions in range (denominator) and those that reported usage. */
  coverage: { sessions: number; reported: number };
  last_active_at: number | null;
}

export interface WriteOperation {
  id: string;
  created_at: number;
  api_key: KeyRef | null;
  action: string;
  resource_type: string;
  resource_id: string;
  parent_id: string;
  request_id: string;
  trace_id: string;
}

export type OwnedRuntimeObservation = RuntimeObservation & { project_id: string };
export const ownerResourceTypes = ["agent", "session", "environment", "environment_template", "skill", "skill_version", "file", "vault", "credential", "artifact"] as const satisfies readonly AdminResourceType[];
export type OwnerResourceType = AdminResourceType;

const PAGE = 100;
/** Bounded walks: a deployment has a handful of projects and keys. */
const MAX_PAGES = 100;

function seconds(value: string): number {
  return Math.floor(Date.parse(value) / 1000);
}
function maybeSeconds(value: string | null): number | null {
  return value === null ? null : seconds(value);
}

export function projectView(project: AdminProject): Project {
  return {
    id: project.id,
    name: project.name,
    status: project.archived_at === null ? "active" : "archived",
    created_at: seconds(project.created_at),
    archived_at: maybeSeconds(project.archived_at),
    active_key_count: project.active_key_count,
  };
}

function keyView(key: AdminAPIKey): AdminKey {
  return { ...key, created_at: seconds(key.created_at), revoked_at: maybeSeconds(key.revoked_at) };
}

function keyRef(key: AdminKeyProvenance | null): KeyRef | null {
  if (!key) return null;
  return { id: key.id, name: key.name || null, prefix: key.prefix || null, kind: key.kind, revoked_at: maybeSeconds(key.revoked_at) };
}

export async function listAllProjects(signal?: AbortSignal): Promise<Project[]> {
  const projects: Project[] = [];
  let after: string | undefined;
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const result = await admin.listProjects({ after, limit: PAGE, order: "asc", signal });
    projects.push(...result.data.map(projectView));
    const last = result.data.at(-1)?.id;
    if (!result.has_more || !last) break;
    after = last;
  }
  return projects;
}

export async function createProject(name: string, signal?: AbortSignal): Promise<Project> {
  return projectView(await admin.createProject({ name }, { signal }));
}
export async function renameProject(projectId: string, name: string, signal?: AbortSignal): Promise<Project> {
  return projectView(await admin.renameProject(projectId, { name }, { signal }));
}
/** Revokes every key of the project; its assets stay viewable. */
export async function archiveProject(projectId: string, signal?: AbortSignal): Promise<Project> {
  return projectView(await admin.archiveProject(projectId, { signal }));
}

export async function listKeys(projectId: string, signal?: AbortSignal): Promise<AdminKey[]> {
  const keys: AdminKey[] = [];
  let after: string | undefined;
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const result = await admin.listAPIKeys(projectId, { after, limit: PAGE, signal });
    keys.push(...result.data.map(keyView));
    const last = result.data.at(-1)?.id;
    if (!result.has_more || !last) break;
    after = last;
  }
  return keys;
}

/** Issues a named key; the plaintext exists only in this result. */
export async function issueKey(projectId: string, name: string, signal?: AbortSignal): Promise<AdminIssuedKey> {
  const issued = await admin.issueAPIKey(projectId, { name }, { signal });
  return { ...keyView(issued), key: issued.key };
}
export async function revokeKey(projectId: string, keyId: string, signal?: AbortSignal): Promise<void> {
  await admin.revokeAPIKey(projectId, keyId, { signal });
}

function summaryView(entry: AdminSummaryEntry, keys: ReadonlyMap<string, AdminKey>): ProjectSummary {
  const measured = entry.coverage.measured_sessions;
  const key = entry.key_id ? keys.get(entry.key_id) : undefined;
  return {
    project_id: entry.project_id,
    agent_id: entry.agent_id,
    key_id: entry.key_id,
    key: entry.key_id
      ? { id: entry.key_id, name: key?.name ?? null, prefix: key?.prefix ?? null, kind: "issued", revoked_at: key?.revoked_at ?? null }
      : null,
    assets: entry.assets,
    sessions: entry.sessions,
    // Unmeasured Sessions add no tokens; an all-unmeasured group has no usage, not zero.
    usage: measured === 0 ? null : {
      input_tokens: entry.usage.input_tokens,
      output_tokens: entry.usage.output_tokens,
      total_tokens: entry.usage.total_tokens,
      cached_tokens: entry.usage.input_tokens_details.cached_tokens,
      reasoning_tokens: entry.usage.output_tokens_details.reasoning_tokens,
    },
    coverage: { sessions: entry.coverage.total_sessions, reported: measured },
    last_active_at: entry.last_active_at,
  };
}

export interface SummaryQuery {
  group_by?: "project" | "agent" | "key";
  project_id?: string;
  /** Unix seconds, inclusive. */
  created_after?: number;
  /** Unix seconds, exclusive. */
  created_before?: number;
  signal?: AbortSignal;
}

/** Every summary row for the query; key rows carry the creating key's name. */
export async function loadSummary(query: SummaryQuery = {}): Promise<ProjectSummary[]> {
  const entries: AdminSummaryEntry[] = [];
  let after: string | undefined;
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const result = await admin.retrieveSummary({
      group_by: query.group_by,
      project_id: query.project_id,
      created_after: query.created_after === undefined ? undefined : new Date(query.created_after * 1000).toISOString(),
      created_before: query.created_before === undefined ? undefined : new Date(query.created_before * 1000).toISOString(),
      after,
      limit: PAGE,
      signal: query.signal,
    });
    entries.push(...result.data);
    if (!result.has_more || !result.next_cursor) break;
    after = result.next_cursor;
  }
  const keys = new Map<string, AdminKey>();
  if (query.group_by === "key") {
    const projectIds = [...new Set(entries.filter((entry) => entry.key_id).map((entry) => entry.project_id))];
    const lists = await Promise.allSettled(projectIds.map((projectId) => listKeys(projectId, query.signal)));
    for (const list of lists) if (list.status === "fulfilled") for (const key of list.value) keys.set(key.id, key);
  }
  return entries.map((entry) => summaryView(entry, keys));
}

export interface WriteOperationQuery {
  key_id?: string;
  resource_type?: AdminResourceType;
  resource_id?: string;
  after?: string;
  limit?: number;
  signal?: AbortSignal;
}

function operationView(operation: AdminWriteOperation): WriteOperation {
  return { ...operation, created_at: seconds(operation.created_at), api_key: keyRef(operation.api_key) };
}

export async function listWriteOperations(projectId: string, query: WriteOperationQuery = {}): Promise<{ data: WriteOperation[]; has_more: boolean; next_cursor: string }> {
  const page = await admin.listWriteOperations(projectId, {
    key_id: query.key_id,
    resource_type: query.resource_type,
    resource_id: query.resource_id,
    after: query.after,
    limit: query.limit ?? 50,
    signal: query.signal,
  });
  return { data: page.data.map(operationView), has_more: page.has_more && page.next_cursor !== "", next_cursor: page.next_cursor };
}

/** Creators of up to any number of resources of one project, in batches of 100. */
export async function listCreators(projectId: string, type: OwnerResourceType, ids: readonly string[], signal?: AbortSignal): Promise<Map<string, Creator>> {
  const unique = [...new Set(ids)];
  const creators = new Map<string, Creator>();
  for (let start = 0; start < unique.length; start += PAGE) {
    const batch = unique.slice(start, start + PAGE);
    const result = await admin.retrieveResourceOwners(projectId, type, batch, { signal });
    for (const owner of result.data) creators.set(owner.resource_id, { key: keyRef(owner.api_key), source: owner.source });
  }
  return creators;
}

/** Runtime snapshots of every project, each labelled with its project. */
export async function listRuntimeObservations(signal?: AbortSignal): Promise<OwnedRuntimeObservation[]> {
  const observations: OwnedRuntimeObservation[] = [];
  let after: string | undefined;
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const result = await admin.listRuntimeObservations({ after, limit: PAGE, signal });
    observations.push(...result.data.map((entry) => ({ ...entry.observation, project_id: entry.project_id })));
    if (!result.has_more || !result.last_id) break;
    after = result.last_id;
  }
  return observations;
}

export function isProjectName(value: string): boolean {
  return value.trim() === value && [...value].length >= 1 && [...value].length <= 128 && !/[\u0000-\u001f\u007f]/.test(value);
}

export function isKeyName(value: string): boolean {
  return value.trim() === value && [...value].length >= 1 && [...value].length <= 80 && !/[\u0000-\u001f\u007f]/.test(value);
}
