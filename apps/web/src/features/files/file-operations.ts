import type { PageOrder, SourceFileListEntry } from "@oac/agents-client";

import type { ProjectClient } from "../../lib/projects";
import { appendCollectionPage } from "../../lib/collection-pagination";

/** Core's single-upload bound for user_data Files. */
export const maxFileUploadBytes = 512 * 1024 * 1024;
/** One page of the Files list; Core allows up to 10000, but the console reads 100 at a time. */
export const filesPageSize = 100;

export type UploadPrecheck = "too-large" | "bad-name" | null;

/** Browser checks before any byte is sent; Core still validates the upload. */
export function uploadPrecheck(file: { name: string; size: number }): UploadPrecheck {
  if (!Number.isSafeInteger(file.size) || file.size < 0 || file.size > maxFileUploadBytes) return "too-large";
  const nameBytes = new TextEncoder().encode(file.name).length;
  if (nameBytes < 1 || nameBytes > 1024 || file.name.includes("\0")) return "bad-name";
  return null;
}

export function isUnrecognizedFile(entry: SourceFileListEntry): entry is Extract<SourceFileListEntry, { unrecognized: true }> {
  return entry.unrecognized === true;
}

/** Local filter over loaded rows only: Core has no search parameter. */
export function filterFiles(files: readonly SourceFileListEntry[], query: string): SourceFileListEntry[] {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return [...files];
  return files.filter((file) => file.id.toLocaleLowerCase().includes(needle) ||
    (!isUnrecognizedFile(file) && file.filename.toLocaleLowerCase().includes(needle)));
}

/** Reads one page after the loaded rows and applies the shared identity and cursor checks. */
export async function readFilesPage(
  core: Pick<ProjectClient, "listSourceFiles">,
  loaded: readonly SourceFileListEntry[],
  order: PageOrder,
  after: string | undefined,
  signal?: AbortSignal,
): Promise<{ values: SourceFileListEntry[]; nextAfter: string | null }> {
  const page = await core.listSourceFiles({ limit: filesPageSize, order, after, signal });
  return appendCollectionPage(loaded, page, after);
}
