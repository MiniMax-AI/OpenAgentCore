import { AdminClient, type KeyRef, type OpenAIAgentsClient, type OwnerResourceType, type Project } from "@agents-core-web/agents-client";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../components/console-ui";

/**
 * The console reaches Core only through the Web API (`/core/v1/admin`). A
 * project owns an isolated set of assets shared by all of its named API keys;
 * pages filter by project and read each project through a read/delete scope
 * that reuses the public projections.
 */
export const admin = new AdminClient();

export type ProjectsState =
  | { status: "loading"; projects: Project[]; error: null }
  | { status: "ready"; projects: Project[]; error: null }
  | { status: "failed"; projects: Project[]; error: string };

interface ProjectsContextValue {
  state: ProjectsState;
  refresh: () => void;
  byId: ReadonlyMap<string, Project>;
}

const ProjectsContext = createContext<ProjectsContextValue | null>(null);

export function ProjectsProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<ProjectsState>({ status: "loading", projects: [], error: null });
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setState((current) => ({ status: "loading", projects: current.projects, error: null }));
    admin.listProjects({ signal: controller.signal }).then(
      (projects) => setState({ status: "ready", projects, error: null }),
      (error: unknown) => {
        if (controller.signal.aborted) return;
        setState((current) => ({ status: "failed", projects: current.projects, error: error instanceof Error ? error.message : String(error) }));
      },
    );
    return () => controller.abort();
  }, [revision]);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const byId = useMemo(() => new Map(state.projects.map((project) => [project.id, project])), [state.projects]);
  return <ProjectsContext.Provider value={{ state, refresh, byId }}>{children}</ProjectsContext.Provider>;
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
  return (
    <label className="select-control project-filter">
      <span className="visually-hidden">{t("project.filter")}</span>
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        {includeAll ? <option value="">{t("project.all")}</option> : null}
        {state.projects.map((project) => (
          <option key={project.id} value={project.id}>
            {project.status === "archived" ? t("project.archivedOption", { name: project.name }) : project.name}
          </option>
        ))}
      </select>
    </label>
  );
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

const clients = new Map<string, OpenAIAgentsClient>();
export function projectClient(projectId: string): OpenAIAgentsClient {
  let client = clients.get(projectId);
  if (!client) { client = admin.scopeClient(projectId); clients.set(projectId, client); }
  return client;
}

/**
 * Loads one collection from the selected project, or from every project in
 * parallel. A failing project is reported by name; the others still show.
 */
export function useProjectCollection<T>(
  filter: ProjectFilterValue,
  load: (client: OpenAIAgentsClient, signal: AbortSignal) => Promise<T[]>,
  deps: readonly unknown[] = [],
): ProjectCollection<T> {
  const { state } = useProjects();
  const [collection, setCollection] = useState<Omit<ProjectCollection<T>, "refresh">>({ status: "loading", items: [], failures: [] });
  const [revision, setRevision] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;
  const targets = useMemo(() => state.projects.filter((project) => !filter || project.id === filter), [filter, state.projects]);
  useEffect(() => {
    if (state.status === "loading" && !state.projects.length) return;
    const controller = new AbortController();
    setCollection((current) => ({ status: "loading", items: current.items, failures: [] }));
    void Promise.allSettled(targets.map(async (project) => ({ project, values: await loadRef.current(projectClient(project.id), controller.signal) })))
      .then((results) => {
        if (controller.signal.aborted) return;
        const items: Owned<T>[] = [];
        const failures: ProjectFailure[] = [];
        results.forEach((result, index) => {
          if (result.status === "fulfilled") items.push(...result.value.values.map((value) => ({ project: result.value.project, value })));
          else failures.push({ project: targets[index]!, message: result.reason instanceof Error ? result.reason.message : String(result.reason) });
        });
        setCollection({ status: "ready", items, failures });
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [targets, revision, state.status, ...deps]);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  return { ...collection, refresh };
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
const creatorCache = new Map<string, KeyRef | null>();
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
  /** undefined: not loaded yet or the lookup failed; null: unknown creator. */
  creatorOf: (projectId: string, id: string) => KeyRef | null | undefined;
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
      const owners = await admin.listResourceOwners(projectId, type, ids, { signal: controller.signal });
      for (const id of ids) creatorCache.set(creatorKey(type, projectId, id), owners.get(id) ?? null);
    })).then(() => { if (!controller.signal.aborted) setVersion((value) => value + 1); });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [type, signature, generation]);
  return {
    creatorOf: (projectId, id) => {
      const key = creatorKey(type, projectId, id);
      return creatorCache.has(key) ? creatorCache.get(key) ?? null : undefined;
    },
  };
}

/** "Creator" column heading with its explanation behind the help tip. */
export function CreatorHeading() {
  const { t } = useTranslation("common");
  return <span className="column-help">{t("creator.column")}<HelpTip>{t("creator.help")}</HelpTip></span>;
}

/** The creating key's name; "Unknown" when Core has no record (historical, copied by an administrator). */
export function CreatorCell({ creator }: { creator: KeyRef | null | undefined }) {
  const { t } = useTranslation("common");
  if (creator === undefined) return <span className="owner-missing">—</span>;
  if (creator === null) return <span className="owner-missing" title={t("creator.unknownHelp")}>{t("creator.unknown")}</span>;
  const label = creator.name ?? (creator.prefix ? `${creator.prefix}…` : t("creator.unknown"));
  return (
    <span className={creator.revoked_at ? "owner-name owner-revoked" : "owner-name"} title={creator.prefix ? `${creator.prefix}…` : undefined}>
      {label}
      {creator.revoked_at ? <span className="owner-flag">{t("creator.revoked")}</span> : null}
    </span>
  );
}
