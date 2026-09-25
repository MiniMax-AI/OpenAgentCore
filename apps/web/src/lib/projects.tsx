import type { CoreProjectReader } from "@agents-core-web/agents-client";
import { QueryClientProvider, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { ConsoleSelect } from "../components/console-select";
import { HelpTip } from "../components/console-ui";
import { admin, listCreators, type Creator, type OwnerResourceType, type Project } from "./admin-view";
import { collectionQuery, projectsQuery, queryClient, type CollectionSpec } from "./queries";

export { admin };
export type { Project };

/**
 * The console reaches Core only through the Web API (`/core/v1`). A
 * project owns an isolated set of assets shared by all of its named API keys;
 * pages filter by project and read each project through `projectClient`.
 */

export type ProjectsState =
  | { status: "loading"; projects: Project[]; error: null }
  | { status: "ready"; projects: Project[]; error: null }
  | { status: "failed"; projects: Project[]; error: string };

interface ProjectsContextValue {
  state: ProjectsState;
  refresh: () => void;
  byId: ReadonlyMap<string, Project>;
  /** Why the last refresh failed while earlier projects stay on screen. */
  refreshError: string | null;
}

const ProjectsContext = createContext<ProjectsContextValue | null>(null);

/** Provides the query cache and the project list every page filters by. */
export function ProjectsProvider({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      <ProjectsState>{children}</ProjectsState>
    </QueryClientProvider>
  );
}

function ProjectsState({ children }: { children: ReactNode }) {
  const query = useQuery(projectsQuery);
  const projects = useMemo(() => query.data ?? [], [query.data]);
  const state: ProjectsState = query.isPending
    ? { status: "loading", projects, error: null }
    : query.isError && !query.data
      ? { status: "failed", projects, error: query.error instanceof Error ? query.error.message : String(query.error) }
      : { status: query.isFetching ? "loading" : "ready", projects, error: null };
  const { refetch } = query;
  const refresh = useCallback(() => { void refetch(); }, [refetch]);
  const byId = useMemo(() => new Map(projects.map((project) => [project.id, project])), [projects]);
  const refreshError = query.isError && query.data ? (query.error instanceof Error ? query.error.message : String(query.error)) : null;
  const value = useMemo(() => ({ state, refresh, byId, refreshError }), [state.status, state.error, projects, refresh, byId, refreshError]); // eslint-disable-line react-hooks/exhaustive-deps
  return <ProjectsContext.Provider value={value}>{children}</ProjectsContext.Provider>;
}

export function useProjects(): ProjectsContextValue {
  const value = useContext(ProjectsContext);
  if (!value) throw new Error("useProjects needs a ProjectsProvider.");
  return value;
}

/** "" means every project; otherwise one project ID. */
export type ProjectFilterValue = string;

/** First control of a toolbar on project data: which project to show. */
export function ProjectFilter({ value, onChange, includeAll = true }: { value: ProjectFilterValue; onChange: (value: ProjectFilterValue) => void; includeAll?: boolean }) {
  const { t } = useTranslation("common");
  const { state } = useProjects();
  const options = useMemo(() => [
    ...(includeAll ? [{ value: "", label: t("project.all") }] : []),
    ...state.projects.map((project) => ({
      value: project.id,
      label: project.status === "archived" ? t("project.archivedOption", { name: project.name }) : project.name,
    })),
  ], [includeAll, state.projects, t]);
  return <ConsoleSelect value={value} onChange={onChange} options={options} label={t("project.filter")} />;
}

/** The project a row belongs to when a table shows every project. */
export function ProjectName({ project }: { project: Project | undefined }) {
  const { t } = useTranslation("common");
  if (!project) return <span className="table-muted">—</span>;
  return (
    <span className={project.status === "archived" ? "space-name space-disabled" : "space-name"} title={project.status === "archived" ? t("project.archived") : undefined}>
      {project.name}
    </span>
  );
}

export interface Owned<T> {
  project: Project;
  value: T;
}

export interface ProjectFailure {
  project: Project;
  message: string;
}

export interface ProjectCollection<T> {
  status: "loading" | "ready";
  items: Owned<T>[];
  failures: ProjectFailure[];
  refresh: () => void;
}

/** The Core reads and deletes a project page uses, bound to one project. */
export type ProjectClient = CoreProjectReader;

async function content(result: Promise<{ blob: Blob; contentType: string | null; contentDisposition: string | null }>) {
  const value = await result;
  return { data: value.blob, bytes: value.blob.size, content_type: "application/octet-stream" as const, content_disposition: value.contentDisposition ?? "" };
}

/**
 * Binds the management client to one project in the public projections'
 * shapes, so pages read a project through `/core/v1/projects/{id}`.
 * Deletions only; no creation or editing exists here.
 */
function createProjectClient(projectId: string): CoreProjectReader {
  const listSessions = async (options: Parameters<CoreProjectReader["listSessions"]>[0] = {}) => {
    const page = await admin.listSessions(projectId, { after: options.after, limit: options.limit, order: options.order, agentId: options.agentId, signal: options.signal });
    return { ...page, object: "list" as const, first_id: page.first_id ?? null, last_id: page.last_id ?? null };
  };
  return {
    listAgents: (options) => admin.listAgents(projectId, options),
    retrieveAgent: (agentId: string) => admin.retrieveAgent(projectId, agentId),
    deleteAgent: (agentId: string) => admin.deleteAgent(projectId, agentId),
    listSkills: (options) => admin.listSkills(projectId, options),
    retrieveSkill: (skillId, options) => admin.retrieveSkill(projectId, skillId, options),
    deleteSkill: (skillId, options) => admin.deleteSkill(projectId, skillId, options),
    listSkillVersions: (skillId, options) => admin.listSkillVersions(projectId, skillId, options),
    deleteSkillVersion: (skillId, version, options) => admin.deleteSkillVersion(projectId, skillId, version, options),
    downloadSkill: (skillId, options) => content(admin.downloadSkill(projectId, skillId, options)),
    downloadSkillVersion: (skillId, version, options) => content(admin.downloadSkillVersion(projectId, skillId, version, options)),
    listEnvironmentTemplates: (options) => admin.listEnvironmentTemplates(projectId, options),
    retrieveEnvironmentTemplate: (templateId, options) => admin.retrieveEnvironmentTemplate(projectId, templateId, options),
    deleteEnvironmentTemplate: (templateId, options) => admin.deleteEnvironmentTemplate(projectId, templateId, options),
    listSourceFiles: (options) => admin.listSourceFiles(projectId, options),
    deleteSourceFile: (fileId, options) => admin.deleteSourceFile(projectId, fileId, options),
    listVaults: (options) => admin.listVaults(projectId, options),
    retrieveVault: (vaultId, options) => admin.retrieveVault(projectId, vaultId, options),
    listVaultCredentials: (vaultId, options) => admin.listVaultCredentials(projectId, vaultId, options),
    deleteVault: (vaultId) => admin.deleteVault(projectId, vaultId),
    deleteVaultCredential: (vaultId, credentialId) => admin.deleteVaultCredential(projectId, vaultId, credentialId),
    listSessions,
    // The management list is strict: a malformed Session fails the page rather than being skipped.
    listSessionsTolerant: async (options) => ({ ...(await listSessions(options)), unrecognized: [] }),
    retrieveSession: (sessionId, options) => admin.retrieveSession(projectId, sessionId, options),
    deleteSession: (sessionId) => admin.deleteSession(projectId, sessionId),
    listTurns: (sessionId, options) => admin.listTurns(projectId, sessionId, options),
    listItems: (sessionId, options) => admin.listItems(projectId, sessionId, options),
    retrieveRuntimeObservation: (sessionId, options) => admin.retrieveRuntimeObservation(projectId, sessionId, options),
    retrieveRuntimeHistory: (sessionId, query) => admin.retrieveRuntimeHistory(projectId, sessionId, query),
  } satisfies CoreProjectReader;
}

const clients = new Map<string, ProjectClient>();
export function projectClient(projectId: string): ProjectClient {
  let client = clients.get(projectId);
  if (!client) { client = createProjectClient(projectId); clients.set(projectId, client); }
  return client;
}

/**
 * Loads one collection from the selected project, or from every project in
 * parallel, through the query cache: each project is cached on its own, so the
 * all-projects view and a single project share reads, a revisit opens at once,
 * and a refresh keeps the rows on screen. A failing project is reported by
 * name; the others still show.
 */
export function useProjectCollection<T>(spec: CollectionSpec<T>, filter: ProjectFilterValue): ProjectCollection<T> {
  const { state } = useProjects();
  const queryClient = useQueryClient();
  const targets = useMemo(() => state.projects.filter((project) => !filter || project.id === filter), [filter, state.projects]);
  const projectsKnown = state.status !== "loading" || state.projects.length > 0;
  const results = useQueries({
    queries: targets.map((project) => ({ ...collectionQuery(spec, project.id), enabled: projectsKnown })),
  });
  // `items` keeps its identity until a project's cached data changes, so pages
  // may depend on it in effects and memos without re-running on every render.
  const cache = useRef<{ targets: readonly Project[]; sources: readonly (T[] | undefined)[]; items: Owned<T>[] } | null>(null);
  const sources = results.map((result) => result.data);
  const previous = cache.current;
  let items: Owned<T>[];
  if (previous && previous.targets === targets && previous.sources.length === sources.length && previous.sources.every((source, index) => source === sources[index])) {
    items = previous.items;
  } else {
    items = sources.flatMap((source, index) => (source ?? []).map((value) => ({ project: targets[index]!, value })));
    cache.current = { targets, sources, items };
  }
  const failures: ProjectFailure[] = [];
  let pending = !projectsKnown;
  results.forEach((result, index) => {
    const project = targets[index]!;
    if (result.isError) failures.push({ project, message: result.error instanceof Error ? result.error.message : String(result.error) });
    if (result.isFetching || result.isPending) pending = true;
  });
  const specKey = JSON.stringify(spec.key);
  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ["collection", ...(JSON.parse(specKey) as unknown[])] });
  }, [queryClient, specKey]);
  return { status: pending ? "loading" : "ready", items, failures, refresh };
}

/** Reads every page of a cursor-paginated list, bounded like the rest of the console. */
export async function readAllPages<T extends { id: string }>(
  page: (after: string | undefined) => Promise<{ data: T[]; has_more: boolean; last_id?: string | null }>,
  limit = 10_000,
): Promise<T[]> {
  const values: T[] = [];
  let after: string | undefined;
  while (values.length < limit) {
    const result = await page(after);
    values.push(...result.data);
    const last = result.last_id ?? result.data.at(-1)?.id;
    if (!result.has_more || !last) break;
    after = last;
  }
  return values;
}

/** Creator lookups are cached per project and resource; a refresh forgets them. */
const creatorCache = new Map<string, Creator>();
const creatorKey = (type: OwnerResourceType, projectId: string, id: string) => `${type}:${projectId}:${id}`;
let creatorGeneration = 0;
const creatorListeners = new Set<(generation: number) => void>();

/** Drops cached creators; every mounted lookup reads them again. */
export function forgetCreators() {
  creatorCache.clear();
  creatorGeneration += 1;
  for (const listener of creatorListeners) listener(creatorGeneration);
}

export interface Creators {
  /** undefined: not loaded yet or the lookup failed. */
  creatorOf: (projectId: string, id: string) => Creator | undefined;
}

/** The key that created each row (#87 ownership), batched per project. */
export function useCreators(type: OwnerResourceType, rows: ReadonlyArray<{ projectId: string; id: string }>): Creators {
  const [, setVersion] = useState(0);
  const [generation, setGeneration] = useState(creatorGeneration);
  useEffect(() => {
    creatorListeners.add(setGeneration);
    return () => { creatorListeners.delete(setGeneration); };
  }, []);
  const signature = useMemo(() => rows.map((row) => `${row.projectId}:${row.id}`).sort().join(","), [rows]);
  useEffect(() => {
    const byProject = new Map<string, string[]>();
    for (const row of rows) {
      if (creatorCache.has(creatorKey(type, row.projectId, row.id))) continue;
      byProject.set(row.projectId, [...(byProject.get(row.projectId) ?? []), row.id]);
    }
    if (!byProject.size) return;
    const controller = new AbortController();
    void Promise.allSettled([...byProject].map(async ([projectId, ids]) => {
      const creators = await listCreators(projectId, type, ids, controller.signal);
      for (const id of ids) creatorCache.set(creatorKey(type, projectId, id), creators.get(id) ?? { key: null, source: null });
    })).then(() => { if (!controller.signal.aborted) setVersion((value) => value + 1); });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [type, signature, generation]);
  return {
    creatorOf: (projectId, id) => creatorCache.get(creatorKey(type, projectId, id)),
  };
}

/** "Creator" column heading with its explanation behind the help tip. */
export function CreatorHeading() {
  const { t } = useTranslation("common");
  return <span className="column-help">{t("creator.column")}<HelpTip>{t("creator.help")}</HelpTip></span>;
}

/** The creating key's name, "Admin copy" for a copied asset, "Unknown" when Core has no record. */
export function CreatorCell({ creator }: { creator: Creator | undefined }) {
  const { t } = useTranslation("common");
  if (creator === undefined) return <span className="owner-missing">—</span>;
  if (creator.source === "admin_copy") return <span className="owner-missing" title={t("creator.adminCopyHelp")}>{t("creator.adminCopy")}</span>;
  const key = creator.key;
  if (!key) return <span className="owner-missing" title={t("creator.unknownHelp")}>{t("creator.unknown")}</span>;
  const label = key.name ?? (key.prefix ? `${key.prefix}…` : t("creator.unknown"));
  return (
    <span className={key.revoked_at ? "owner-name owner-revoked" : "owner-name"} title={key.prefix ? `${key.prefix}…` : undefined}>
      {label}
      {key.revoked_at ? <span className="pill">{t("creator.revoked")}</span> : null}
    </span>
  );
}
