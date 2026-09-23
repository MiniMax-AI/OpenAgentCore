import { File, FolderOpen, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import {
  AgentCoreError,
  type EnvironmentFile,
  type EnvironmentFileList,
  type EnvironmentFileListOptions,
  type PageOrder,
} from "@agents-core-web/agents-client";

import "./EnvironmentFilesPanel.css";

export type ListEnvironmentFiles = (
  environmentId: string,
  options: EnvironmentFileListOptions,
) => Promise<EnvironmentFileList>;

type LoadState = "idle" | "loading" | "ready" | "loading-more" | "failed";

function canonicalDirectory(value: string): string | null {
  if (
    !value.startsWith("/") ||
    new TextEncoder().encode(value).length > 4096 ||
    value.includes("\\") ||
    value.includes("\0") ||
    value.includes("\r") ||
    value.includes("\n") ||
    value.split("/").includes("..")
  ) return null;
  const components = value.split("/").filter((component) => component && component !== ".");
  return components.length ? `/${components.join("/")}` : "/";
}

export function validEnvironmentFilesDirectory(
  value: string,
  workspaceDirectory = "/workspace",
): boolean {
  const directory = canonicalDirectory(value);
  const root = canonicalDirectory(workspaceDirectory);
  const normalizedDirectory = value.replace(/\/+$/, "") || "/";
  const normalizedRoot = workspaceDirectory.replace(/\/+$/, "") || "/";
  if (directory === null || root === null || directory !== normalizedDirectory || root !== normalizedRoot) return false;
  return root === "/" || directory === root || directory.startsWith(`${root}/`);
}

/** Core accepts only the cleaned form, so a valid trailing separator is dropped before sending. */
export function environmentFilesRequestDirectory(value: string): string {
  return canonicalDirectory(value) ?? value;
}

export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(bytes < 10 * 1024 * 1024 ? 1 : 0)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

export function environmentFilesFailureMessage(error: unknown, loadingMore: boolean): string {
  if (
    error instanceof AgentCoreError &&
    (error.status === 404 || error.status === 405) &&
    error.code === "unsupported_operation"
  ) {
    return "Workspace file listing is not supported by the connected Core. Web will not retry or infer support.";
  }
  if (error instanceof AgentCoreError && error.status === 503) {
    return "Workspace files are temporarily unavailable. The executor may need to connect before Core can perform this read.";
  }
  if (loadingMore && error instanceof AgentCoreError && error.status === 400) {
    return "The continuation is no longer valid. The directory may have changed; refresh the file list to start again.";
  }
  return "Core could not list this Workspace directory. No partial result was accepted.";
}

export function EnvironmentFilesPanel({
  environmentId,
  workspaceDirectory,
  onListFiles,
}: {
  environmentId: string;
  workspaceDirectory: string;
  onListFiles: ListEnvironmentFiles;
}) {
  const [directory, setDirectory] = useState(workspaceDirectory);
  const [appliedDirectory, setAppliedDirectory] = useState(workspaceDirectory);
  const [order, setOrder] = useState<PageOrder>("asc");
  const [appliedOrder, setAppliedOrder] = useState<PageOrder>("asc");
  const [files, setFiles] = useState<EnvironmentFile[]>([]);
  const [nextPage, setNextPage] = useState<string | null>(null);
  const [state, setState] = useState<LoadState>("idle");
  const [error, setError] = useState<string | null>(null);
  const requestRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);
  const directoryValid = validEnvironmentFilesDirectory(directory, workspaceDirectory);
  const loading = state === "loading" || state === "loading-more";

  useEffect(() => {
    requestRef.current += 1;
    abortRef.current?.abort();
    abortRef.current = null;
    setDirectory(workspaceDirectory);
    setAppliedDirectory(workspaceDirectory);
    setOrder("asc");
    setAppliedOrder("asc");
    setFiles([]);
    setNextPage(null);
    setState("idle");
    setError(null);
    return () => abortRef.current?.abort();
  }, [environmentId, workspaceDirectory]);

  const resetQueryResult = () => {
    setFiles([]);
    setNextPage(null);
    setState("idle");
    setError(null);
  };

  const load = async (append: boolean) => {
    if (!directoryValid || (append && !nextPage)) return;
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    const request = requestRef.current + 1;
    requestRef.current = request;
    const requestedDirectory = append ? appliedDirectory : environmentFilesRequestDirectory(directory);
    const requestedOrder = append ? appliedOrder : order;
    setState(append ? "loading-more" : "loading");
    setError(null);
    if (!append) {
      setFiles([]);
      setNextPage(null);
    }
    try {
      const page = await onListFiles(environmentId, {
        path: requestedDirectory,
        limit: 20,
        order: requestedOrder,
        page: append ? nextPage ?? undefined : undefined,
        signal: controller.signal,
      });
      if (request !== requestRef.current || controller.signal.aborted) return;
      setFiles((current) => append ? [...current, ...page.data] : page.data);
      setNextPage(page.next);
      setAppliedDirectory(requestedDirectory);
      setAppliedOrder(requestedOrder);
      setState("ready");
    } catch (reason) {
      if (request !== requestRef.current || controller.signal.aborted) return;
      if (!append) setFiles([]);
      setNextPage(null);
      setError(environmentFilesFailureMessage(reason, append));
      setState("failed");
    }
  };

  return (
    <section className="environment-files" aria-label="Workspace files">
      <header className="environment-files-heading">
        <div>
          <FolderOpen size={14} strokeWidth={1.5} aria-hidden="true" />
          <span><strong>Workspace files</strong><small>Direct regular files · metadata only</small></span>
        </div>
        {state !== "idle" ? (
          <button
            type="button"
            className="button outline environment-files-refresh"
            onClick={() => void load(false)}
            disabled={!directoryValid || state === "loading" || state === "loading-more"}
          >
            <RefreshCw size={12} aria-hidden="true" /> Refresh
          </button>
        ) : null}
      </header>

      <div className="environment-files-controls">
        <label>
          <span>Directory</span>
          <input
            type="text"
            value={directory}
            onChange={(event) => {
              setDirectory(event.target.value);
              resetQueryResult();
            }}
            aria-invalid={!directoryValid}
            disabled={loading}
          />
        </label>
        <label>
          <span>Order</span>
          <select
            value={order}
            onChange={(event) => {
              setOrder(event.target.value as PageOrder);
              resetQueryResult();
            }}
            disabled={loading}
          >
            <option value="asc">A → Z</option>
            <option value="desc">Z → A</option>
          </select>
        </label>
        <button
          type="button"
          className="button primary"
          onClick={() => void load(false)}
          disabled={!directoryValid || loading}
        >
          {state === "loading" ? "Loading…" : "List files"}
        </button>
      </div>
      {!directoryValid ? <p className="environment-files-validation">Enter an absolute directory inside this Workspace. Parent traversal and backslashes are not allowed.</p> : null}

      {error ? <p className="environment-files-error" role="alert">{error}</p> : null}
      {state === "idle" ? (
        <p className="environment-files-empty">Files are loaded only when requested. Listing never starts a Turn or reads file contents.</p>
      ) : state === "loading" ? (
        <p className="environment-files-empty" aria-live="polite">Reading the authorized directory…</p>
      ) : state === "failed" && files.length === 0 ? null : files.length === 0 ? (
        <p className="environment-files-empty">No direct regular files were returned for this directory.</p>
      ) : (
        <div className="environment-files-list" role="table" aria-label="Workspace file metadata">
          <div className="environment-files-row environment-files-row-header" role="row">
            <span role="columnheader">Path</span><span role="columnheader">Size</span>
          </div>
          {files.map((file) => (
            <div className="environment-files-row" role="row" key={file.path}>
              <code role="cell"><File size={12} strokeWidth={1.5} aria-hidden="true" />{file.path}</code>
              <span role="cell">{formatFileSize(file.size_bytes)}</span>
            </div>
          ))}
        </div>
      )}

      {nextPage ? (
        <button
          type="button"
          className="button outline environment-files-more"
          onClick={() => void load(true)}
          disabled={state === "loading" || state === "loading-more"}
        >
          {state === "loading-more" ? "Loading…" : "Load more"}
        </button>
      ) : null}
      <p className="environment-files-boundary">Paths are constrained to this Environment&apos;s Workspace root <code>{workspaceDirectory}</code>. The selected directory is sent to Core for a live, non-recursive read; it is not a durable file inventory.</p>
    </section>
  );
}
