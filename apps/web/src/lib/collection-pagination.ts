import type { ListPage, PageOptions } from "@agents-core-web/agents-client";

const COLLECTION_PAGE_LIMIT = 100;
const COLLECTION_PAGE_SAFETY_LIMIT = 100;

export async function listAllCollectionPages<T extends { id: string }>(
  readPage: (options: PageOptions) => Promise<ListPage<T>>,
  signal?: AbortSignal,
): Promise<T[]> {
  const values: T[] = [];
  const ids = new Set<string>();
  const cursors = new Set<string>();
  let after: string | undefined;

  for (let pageIndex = 0; pageIndex < COLLECTION_PAGE_SAFETY_LIMIT; pageIndex += 1) {
    signal?.throwIfAborted();
    const page = await readPage({
      after,
      limit: COLLECTION_PAGE_LIMIT,
      order: "desc",
      signal,
    });
    if (!Array.isArray(page.data) || typeof page.has_more !== "boolean") {
      throw new Error("OpenAgentCore returned an invalid collection page.");
    }
    for (const value of page.data) {
      if (!value || typeof value.id !== "string" || value.id.length === 0 || ids.has(value.id)) {
        throw new Error("OpenAgentCore returned duplicate or invalid collection identities.");
      }
      ids.add(value.id);
      values.push(value);
    }
    if (!page.has_more) return values;

    const nextAfter = page.last_id ?? page.data.at(-1)?.id;
    if (!nextAfter || nextAfter === after || cursors.has(nextAfter)) {
      throw new Error("OpenAgentCore returned an invalid collection pagination cursor.");
    }
    cursors.add(nextAfter);
    after = nextAfter;
  }

  throw new Error("OpenAgentCore collection pagination exceeded the Web safety limit.");
}

export async function listStableCollectionPages<T extends { id: string }>(
  readPage: (options: PageOptions) => Promise<ListPage<T>>,
  readRevision: () => number,
  signal?: AbortSignal,
  attempts = 3,
): Promise<T[] | null> {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    signal?.throwIfAborted();
    const revision = readRevision();
    const values = await listAllCollectionPages(readPage, signal);
    signal?.throwIfAborted();
    if (revision === readRevision()) return values;
  }
  return null;
}

/**
 * Appends one cursor page to a "Load more" list. It applies the same identity
 * and cursor checks as the all-pages readers and returns the next cursor, or
 * null once Core reports the end of the collection.
 */
export function appendCollectionPage<T extends { id: string }>(
  loaded: readonly T[],
  page: ListPage<T>,
  after?: string,
): { values: T[]; nextAfter: string | null } {
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean") {
    throw new Error("OpenAgentCore returned an invalid collection page.");
  }
  const ids = new Set(loaded.map((value) => value.id));
  for (const value of page.data) {
    if (!value || typeof value.id !== "string" || value.id.length === 0 || ids.has(value.id)) {
      throw new Error("OpenAgentCore returned duplicate or invalid collection identities.");
    }
    ids.add(value.id);
  }
  const values = [...loaded, ...page.data];
  if (!page.has_more) return { values, nextAfter: null };
  const nextAfter = page.last_id ?? page.data.at(-1)?.id;
  if (!nextAfter || nextAfter === after) {
    throw new Error("OpenAgentCore returned an invalid collection pagination cursor.");
  }
  return { values, nextAfter };
}
