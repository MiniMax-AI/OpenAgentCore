import type { ListPage } from "@oac/agents-client";

/**
 * Appends one cursor page to a "Load more" list. It rejects repeated
 * identities and cursors that do not advance, and returns the next cursor, or
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
