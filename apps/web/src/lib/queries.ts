import { QueryClient, queryOptions } from "@tanstack/react-query";
import type { EnvironmentTemplateResource, SavedAgent, Skill, SourceFileListEntry, Vault } from "@agents-core-web/agents-client";

import { readSessionLog, type SessionLogEntry } from "../features/sessions/session-log";
import { listAllProjects } from "./admin-view";
import { projectClient, readAllPages, type ProjectClient } from "./projects";

/**
 * One cache for every Web API read. A page the administrator has seen opens
 * from the cache at once and refreshes behind it; a refresh keeps the rows on
 * screen; hovering a navigation item reads the page's data before the click.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 15 * 60_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

export const projectsQuery = queryOptions({
  queryKey: ["projects"],
  queryFn: ({ signal }) => listAllProjects(signal),
});

/** A per-project collection: its cache key and how to read it. */
export interface CollectionSpec<T> {
  key: readonly unknown[];
  load: (client: ProjectClient, signal: AbortSignal) => Promise<T[]>;
}

const PAGE = 100;

export const collections = {
  agents: { key: ["agents"], load: (client, signal) => readAllPages((after) => client.listAgents({ after, limit: PAGE, signal })) } satisfies CollectionSpec<SavedAgent>,
  templates: { key: ["templates"], load: (client, signal) => readAllPages((after) => client.listEnvironmentTemplates({ after, limit: PAGE, signal })) } satisfies CollectionSpec<EnvironmentTemplateResource>,
  skills: { key: ["skills"], load: (client, signal) => readAllPages((after) => client.listSkills({ after, limit: PAGE, signal })) } satisfies CollectionSpec<Skill>,
  vaults: { key: ["vaults"], load: (client, signal) => readAllPages((after) => client.listVaults({ after, limit: PAGE, signal })) } satisfies CollectionSpec<Vault>,
  sessions: { key: ["sessions"], load: (client, signal) => readSessionLog(client, signal) } satisfies CollectionSpec<SessionLogEntry>,
};

/** Files are read in the order the page shows, so each order has its own cache entry. */
export function filesCollection(order: "asc" | "desc", pageSize: number): CollectionSpec<SourceFileListEntry> {
  return { key: ["files", order], load: (client, signal) => readAllPages((after) => client.listSourceFiles({ after, limit: pageSize, order, signal })) };
}

export function collectionQuery<T>(spec: CollectionSpec<T>, projectId: string) {
  return queryOptions({
    queryKey: ["collection", ...spec.key, projectId],
    queryFn: ({ signal }) => spec.load(projectClient(projectId), signal),
  });
}
