import {
  AdminClient,
  type AdminAPIKey,
  type AdminKeyProvenance,
  type AdminProject,
  type AdminResourceType,
  type AdminRuntimeObservation,
  type AdminSummaryEntry,
  type AdminWriteOperationOptions,
  type AdminWriteOperationPage,
} from "@oac/agents-client";

/**
 * Reads over the typed management client (`AdminClient`, `/core/v1`): they
 * walk cursor pages, resolve the creating key of summary key rows and turn an
 * unmeasured usage sum into `null`, so pages never treat missing data as zero.
 */
export const admin = new AdminClient();

/** A summary row; a key row carries its key when the project's key list holds it. */
export type ProjectSummary = Omit<AdminSummaryEntry, "usage"> & {
  key: AdminAPIKey | null;
  /** null when no Session in the group reported usage. */
  usage: AdminSummaryEntry["usage"] | null;
};

/** A Runtime observation with its project; disk is E2B-only and null elsewhere. */
export type OwnedRuntimeObservation = AdminRuntimeObservation["observation"] & { project_id: string };

const PAGE = 100;
/** Bounded walks: a deployment has a handful of projects and keys. */
const MAX_PAGES = 100;

export async function listAllProjects(signal?: AbortSignal): Promise<AdminProject[]> {
  const projects: AdminProject[] = [];
  let after: string | undefined;
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const result = await admin.listProjects({ after, limit: PAGE, order: "asc", signal });
    projects.push(...result.data);
    const last = result.data.at(-1)?.id;
    if (!result.has_more || !last) break;
    after = last;
  }
  return projects;
}

export async function listKeys(projectId: string, signal?: AbortSignal): Promise<AdminAPIKey[]> {
  const keys: AdminAPIKey[] = [];
  let after: string | undefined;
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const result = await admin.listAPIKeys(projectId, { after, limit: PAGE, signal });
    keys.push(...result.data);
    const last = result.data.at(-1)?.id;
    if (!result.has_more || !last) break;
    after = last;
  }
  return keys;
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

/** Every summary row for the query; key rows carry the creating key. */
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
  const keys = new Map<string, AdminAPIKey>();
  if (query.group_by === "key") {
    const projectIds = [...new Set(entries.filter((entry) => entry.key_id).map((entry) => entry.project_id))];
    const lists = await Promise.allSettled(projectIds.map((projectId) => listKeys(projectId, query.signal)));
    for (const list of lists) if (list.status === "fulfilled") for (const key of list.value) keys.set(key.id, key);
  }
  return entries.map((entry) => ({
    ...entry,
    key: entry.key_id === null ? null : keys.get(entry.key_id) ?? null,
    // Unmeasured Sessions add no tokens; an all-unmeasured group has no usage, not zero.
    usage: entry.coverage.measured_sessions === 0 ? null : entry.usage,
  }));
}

export type WriteOperationQuery = Pick<AdminWriteOperationOptions, "key_id" | "resource_type" | "resource_id" | "after" | "limit" | "signal">;

export async function listWriteOperations(projectId: string, query: WriteOperationQuery = {}): Promise<AdminWriteOperationPage> {
  const page = await admin.listWriteOperations(projectId, { ...query, limit: query.limit ?? 50 });
  return { ...page, has_more: page.has_more && page.next_cursor !== "" };
}

/** Creators of any number of resources of one project, in batches of 100; null when Core has no creation record. */
export async function listCreators(projectId: string, type: AdminResourceType, ids: readonly string[], signal?: AbortSignal): Promise<Map<string, AdminKeyProvenance | null>> {
  const unique = [...new Set(ids)];
  const creators = new Map<string, AdminKeyProvenance | null>();
  for (let start = 0; start < unique.length; start += PAGE) {
    const batch = unique.slice(start, start + PAGE);
    const result = await admin.retrieveResourceOwners(projectId, type, batch, { signal });
    for (const owner of result.data) creators.set(owner.resource_id, owner.api_key);
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
