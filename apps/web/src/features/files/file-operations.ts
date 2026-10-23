import type { SourceFileListEntry } from "@oac/agents-client";


/** One page of the Files list; Core allows up to 10000, but the console reads 100 at a time. */
export const filesPageSize = 100;

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
