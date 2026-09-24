import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";

import { listKeys, listWriteOperations, loadSummary, type OwnerResourceType, type ProjectSummary } from "../../lib/admin-view";
import { projectsQuery } from "../../lib/queries";
import { sortKeys } from "./key-flows";

/**
 * Cache entries of Platform › Projects and keys. Everything read for one
 * project lives under `["project", id]`, so one invalidation refreshes a
 * project's keys, usage and write operations together. Issued key plaintext
 * never enters the cache: key lists hold only what `listKeys` returns.
 */

export type Loaded<T> = { status: "loading"; value: T | null } | { status: "ready"; value: T } | { status: "failed"; value: T | null };

/** A query's result in the shape the pages render: cached data stays while a refresh runs or fails. */
export function loadedFrom<T>(query: { data: T | undefined; isFetching: boolean; isError: boolean }): Loaded<T> {
  const value = query.data ?? null;
  if (query.isFetching || (value === null && !query.isError)) return { status: "loading", value };
  if (query.isError) return { status: "failed", value };
  return { status: "ready", value: value as T };
}

export const projectScope = (projectId: string) => ["project", projectId] as const;

/** Last activity per project for the list: the per-project summary rows. */
export const projectActivityQuery = queryOptions({
  queryKey: ["project-activity"],
  queryFn: async ({ signal }) => (await loadSummary({ signal })).filter((row) => row.agent_id === null && row.key === null),
  select: (rows: ProjectSummary[]): ReadonlyMap<string, ProjectSummary> => new Map(rows.map((row) => [row.project_id, row])),
});

/** A project's keys, active first. */
export function projectKeysQuery(projectId: string) {
  return queryOptions({
    queryKey: [...projectScope(projectId), "keys"],
    queryFn: async ({ signal }) => sortKeys(await listKeys(projectId, signal)),
  });
}

export interface ProjectSummaries {
  project: ProjectSummary | null;
  /** Per creating key (the `null` entry holds Sessions whose key is unknown); null when unavailable. */
  byKey: Map<string | null, ProjectSummary> | null;
}

/** Asset counts and usage of one project, in total and per creating key. */
export function projectSummaryQuery(projectId: string) {
  return queryOptions({
    queryKey: [...projectScope(projectId), "summary"],
    queryFn: async ({ signal }): Promise<ProjectSummaries> => {
      const [projectRows, keyRows] = await Promise.all([
        loadSummary({ project_id: projectId, signal }),
        loadSummary({ project_id: projectId, group_by: "key", signal }).catch(() => null),
      ]);
      const project = projectRows.find((row) => row.project_id === projectId && row.agent_id === null && row.key === null) ?? null;
      let byKey: ProjectSummaries["byKey"] = null;
      if (keyRows) {
        byKey = new Map();
        for (const row of keyRows) if (row.project_id === projectId) byKey.set(row.key?.id ?? null, row);
      }
      return { project, byKey };
    },
  });
}

export const WRITE_OPERATIONS_PAGE_SIZE = 50;

export interface WriteOperationFilter {
  keyId: string;
  type: OwnerResourceType | "";
}

/** One project's write operations for one filter, newest first, paged by `next_cursor`. */
export function writeOperationsQuery(projectId: string, filter: WriteOperationFilter) {
  return infiniteQueryOptions({
    queryKey: [...projectScope(projectId), "write-operations", filter],
    queryFn: ({ pageParam, signal }) => listWriteOperations(projectId, {
      key_id: filter.keyId || undefined,
      resource_type: filter.type || undefined,
      after: pageParam,
      limit: WRITE_OPERATIONS_PAGE_SIZE,
      signal,
    }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.has_more ? page.next_cursor : undefined),
  });
}

/**
 * Re-reads what a project or key write may have changed: the project list
 * and, for a project, its keys, usage and write operations. Only reads are
 * repeated; a write is never sent again.
 */
export function invalidateProjects(queryClient: QueryClient, options: { projectId?: string | null; activity?: boolean } = {}): Promise<unknown> {
  const tasks: Promise<unknown>[] = [queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey })];
  if (options.projectId) tasks.push(queryClient.invalidateQueries({ queryKey: projectScope(options.projectId) }));
  if (options.activity) tasks.push(queryClient.invalidateQueries({ queryKey: projectActivityQuery.queryKey }));
  return Promise.all(tasks);
}
