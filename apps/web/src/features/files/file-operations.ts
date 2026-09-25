import {
  AgentCoreError,
  type AgentCore,
  type PageOrder,
  type SourceFileListEntry,
} from "@agents-core-web/agents-client";

import { appendCollectionPage } from "../../lib/collection-pagination";

/** Core's single-upload bound for user_data Files. */
export const maxFileUploadBytes = 512 * 1024 * 1024;
/** One page of the Files list; Core allows up to 10000, but the console reads 100 at a time. */
export const filesPageSize = 100;

export type FilesOperations = Pick<AgentCore, "listSourceFiles" | "uploadSourceFile" | "deleteSourceFile">;

export type UploadPrecheck = "too-large" | "bad-name" | null;

/** Browser checks before any byte is sent; Core still validates the upload. */
export function uploadPrecheck(file: { name: string; size: number }): UploadPrecheck {
  if (!Number.isSafeInteger(file.size) || file.size < 0 || file.size > maxFileUploadBytes) return "too-large";
  const nameBytes = new TextEncoder().encode(file.name).length;
  if (nameBytes < 1 || nameBytes > 1024 || file.name.includes("\0")) return "bad-name";
  return null;
}

/**
 * A request that failed with a 4xx answer never stored anything. Everything
 * else (a lost response, a server error, an unrecognized success body) may
 * have been stored, so the caller must not retry automatically.
 */
export function isDefiniteRejection(error: unknown): error is AgentCoreError {
  return error instanceof AgentCoreError && error.status >= 400 && error.status < 500;
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

export type FilesListFailure = "unsupported" | "storage-unavailable" | "failed";

/** How a failed first list read is presented. */
export function classifyFilesListError(error: unknown): FilesListFailure {
  if (error instanceof AgentCoreError) {
    if (error.status === 404 || error.status === 405) return "unsupported";
    if (error.status === 503 && error.code === "file_storage_unavailable") return "storage-unavailable";
  }
  return "failed";
}

type Translate = (key: string) => string;

/** A short, safe reason for a failed request. Core's own message is shown for API errors. */
export function filesErrorReason(error: unknown, t: Translate): string {
  if (error instanceof AgentCoreError) {
    if (error.status === 401 || error.status === 403) return t("errors.unauthorized");
    if (error.status === 413) return t("errors.tooLarge");
    if (error.status === 503 && error.code === "file_storage_unavailable") return t("errors.storage");
    if (error.status === 502 && typeof error.code === "string" && error.code.startsWith("invalid_source_file")) {
      return t("errors.invalidResponse");
    }
    return error.message.trim() || t("errors.transport");
  }
  return t("errors.transport");
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
  core: Pick<AgentCore, "listSourceFiles">,
  loaded: readonly SourceFileListEntry[],
  order: PageOrder,
  after: string | undefined,
  signal?: AbortSignal,
): Promise<{ values: SourceFileListEntry[]; nextAfter: string | null }> {
  const page = await core.listSourceFiles({ limit: filesPageSize, order, after, signal });
  return appendCollectionPage(loaded, page, after);
}
