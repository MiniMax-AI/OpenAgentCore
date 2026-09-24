import { AdminClient, type KeySpace, type OpenAIAgentsClient } from "@agents-core-web/agents-client";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/**
 * The console reaches Core only through the Web API (`/core/v1/admin`). Every
 * API key owns one isolated space of assets; pages filter by key and load each
 * space through a read/delete scope that reuses the public projections.
 */
export const admin = new AdminClient();

export type SpacesState =
  | { status: "loading"; spaces: KeySpace[]; error: null }
  | { status: "ready"; spaces: KeySpace[]; error: null }
  | { status: "failed"; spaces: KeySpace[]; error: string };

interface KeySpacesContextValue {
  state: SpacesState;
  refresh: () => void;
  byId: ReadonlyMap<string, KeySpace>;
}

const KeySpacesContext = createContext<KeySpacesContextValue | null>(null);

export function KeySpacesProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SpacesState>({ status: "loading", spaces: [], error: null });
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setState((current) => ({ status: "loading", spaces: current.spaces, error: null }));
    admin.listKeySpaces({ signal: controller.signal }).then(
      (spaces) => setState({ status: "ready", spaces, error: null }),
      (error: unknown) => {
        if (controller.signal.aborted) return;
        setState((current) => ({ status: "failed", spaces: current.spaces, error: error instanceof Error ? error.message : String(error) }));
      },
    );
    return () => controller.abort();
  }, [revision]);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const byId = useMemo(() => new Map(state.spaces.map((space) => [space.id, space])), [state.spaces]);
  return <KeySpacesContext.Provider value={{ state, refresh, byId }}>{children}</KeySpacesContext.Provider>;
}

export function useKeySpaces(): KeySpacesContextValue {
  const value = useContext(KeySpacesContext);
  if (!value) throw new Error("useKeySpaces needs a KeySpacesProvider.");
  return value;
}

/** "" means every API key; otherwise one space ID. */
export type SpaceFilter = string;

/** First control of a resource toolbar: which API key's assets to show. */
export function KeySpaceFilter({ value, onChange, includeAll = true }: { value: SpaceFilter; onChange: (value: SpaceFilter) => void; includeAll?: boolean }) {
  const { t } = useTranslation("common");
  const { state } = useKeySpaces();
  return (
    <label className="select-control key-space-filter">
      <span className="visually-hidden">{t("keySpace.filter")}</span>
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        {includeAll ? <option value="">{t("keySpace.all")}</option> : null}
        {state.spaces.map((space) => (
          <option key={space.id} value={space.id}>
            {space.status === "disabled" ? t("keySpace.disabledOption", { name: space.username }) : space.username}
          </option>
        ))}
      </select>
    </label>
  );
}

/** The owning key of a row when the table shows every key. */
export function SpaceName({ space }: { space: KeySpace | undefined }) {
  const { t } = useTranslation("common");
  if (!space) return <span className="table-muted">—</span>;
  return (
    <span className={space.status === "disabled" ? "space-name space-disabled" : "space-name"} title={space.status === "disabled" ? t("keySpace.disabled") : undefined}>
      {space.username}
    </span>
  );
}

export interface Owned<T> {
  space: KeySpace;
  value: T;
}

export type SpaceCollection<T> =
  | { status: "loading"; items: Owned<T>[]; failures: SpaceFailure[] }
  | { status: "ready"; items: Owned<T>[]; failures: SpaceFailure[] };

export interface SpaceFailure {
  space: KeySpace;
  message: string;
}

const clients = new Map<string, OpenAIAgentsClient>();
export function spaceClient(spaceId: string): OpenAIAgentsClient {
  let client = clients.get(spaceId);
  if (!client) { client = admin.scopeClient(spaceId); clients.set(spaceId, client); }
  return client;
}

/**
 * Loads one collection from the selected space, or from every space in
 * parallel. A failing space is reported by name; the others still show.
 */
export function useSpaceCollection<T>(
  filter: SpaceFilter,
  load: (client: OpenAIAgentsClient, signal: AbortSignal) => Promise<T[]>,
  deps: readonly unknown[] = [],
): SpaceCollection<T> & { refresh: () => void } {
  const { state } = useKeySpaces();
  const [collection, setCollection] = useState<SpaceCollection<T>>({ status: "loading", items: [], failures: [] });
  const [revision, setRevision] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;
  const targets = useMemo(() => state.spaces.filter((space) => !filter || space.id === filter), [filter, state.spaces]);
  useEffect(() => {
    if (state.status === "loading" && !state.spaces.length) return;
    const controller = new AbortController();
    setCollection((current) => ({ status: "loading", items: current.items, failures: [] }));
    void Promise.allSettled(targets.map(async (space) => ({ space, values: await loadRef.current(spaceClient(space.id), controller.signal) })))
      .then((results) => {
        if (controller.signal.aborted) return;
        const items: Owned<T>[] = [];
        const failures: SpaceFailure[] = [];
        results.forEach((result, index) => {
          if (result.status === "fulfilled") items.push(...result.value.values.map((value) => ({ space: result.value.space, value })));
          else failures.push({ space: targets[index]!, message: result.reason instanceof Error ? result.reason.message : String(result.reason) });
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
