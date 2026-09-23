import { FileUp, FolderInput, RefreshCw, Search, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type ChangeEvent } from "react";

import {
  AgentCoreError,
  type AgentCore,
  type AgentEnvironmentResource,
  type EnvironmentFile,
  type SourceFile,
} from "@agents-core-web/agents-client";

import { formatFileSize } from "../sessions/environment/EnvironmentFilesPanel";
import { isWritableBasicHostedEnvironmentResource } from "../sessions/environment/environment-state";
import "./SourceFilesPanel.css";

export interface SourceFilesOperations {
  uploadSourceFile: AgentCore["uploadSourceFile"];
  retrieveSourceFile: AgentCore["retrieveSourceFile"];
  deleteSourceFile: AgentCore["deleteSourceFile"];
  retrieveEnvironment: AgentCore["retrieveEnvironment"];
  createEnvironmentFile: AgentCore["createEnvironmentFile"];
  listEnvironmentFiles: AgentCore["listEnvironmentFiles"];
}

type Operation = "upload" | "retrieve" | "delete" | "environment" | "copy" | null;
type NoticeTone = "info" | "success" | "error" | "warning";

interface Notice {
  tone: NoticeTone;
  text: string;
}

const maxSourceBytes = 512 * 1024 * 1024;
const maxDestinationBytes = 50 * 1024 * 1024;

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function safeFailure(error: unknown, operation: Exclude<Operation, null>): string {
  if (error instanceof TypeError && !error.message.toLowerCase().includes("fetch")) return error.message;
  if (!(error instanceof AgentCoreError)) return "The Core request did not complete. Private transport details are hidden.";
  if (error.status === 400) return "Core rejected the request as invalid. Check the ID, filename, or destination path.";
  if (error.status === 401) return "Core rejected the current connection credentials.";
  if (error.status === 404) return operation === "environment"
    ? "That Environment is not available to the current project."
    : "That Source File is not available to the current project.";
  if (error.status === 409) return "Core has another unresolved Turn or file mutation. This request was not replayed.";
  if (error.status === 413) return operation === "upload"
    ? "The Source File exceeds the 512 MiB upload limit."
    : "The destination copy exceeds the 50 MiB Environment limit.";
  if (error.status === 503) return operation === "environment"
    ? "The Environment resource is temporarily unavailable."
    : "The file service or execution placement is temporarily unavailable.";
  return "Core could not complete the request. Private server details are hidden.";
}

export function validHostedDestinationPath(value: string): boolean {
  if (
    !value.startsWith("/workspace/") ||
    new TextEncoder().encode(value).length > 4096 ||
    value.includes("\\") ||
    value.includes("\0") ||
    value.includes("\r") ||
    value.includes("\n") ||
    value.split("/").includes("..") ||
    value.split("/").includes(".") ||
    value.includes("//") ||
    value.endsWith("/")
  ) return false;
  return value.length > "/workspace/".length;
}

function parentDirectory(path: string): string {
  return path.slice(0, path.lastIndexOf("/")) || "/workspace";
}

function knownRejected(error: unknown): boolean {
  return error instanceof AgentCoreError && error.status >= 400 && error.status < 500;
}

export function SourceFilesPanel({
  operations,
  environmentFilesEnabled,
}: {
  operations: SourceFilesOperations;
  environmentFilesEnabled: boolean;
}) {
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [fileInputKey, setFileInputKey] = useState(0);
  const [fileId, setFileId] = useState("");
  const [metadata, setMetadata] = useState<SourceFile | null>(null);
  const [notice, setNotice] = useState<Notice>({
    tone: "info",
    text: "Choose a local file to upload, or enter a Source File ID returned by Core.",
  });
  const [environmentId, setEnvironmentId] = useState("");
  const [environment, setEnvironment] = useState<AgentEnvironmentResource | null>(null);
  const [destinationPath, setDestinationPath] = useState("/workspace/input/");
  const [copyResult, setCopyResult] = useState<EnvironmentFile | null>(null);
  const [copyUnknown, setCopyUnknown] = useState(false);
  const [operation, setOperation] = useState<Operation>(null);
  const requestRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);
  const busy = operation !== null;
  const currentId = fileId.trim();
  const sourceReady = metadata !== null && metadata.id === currentId;
  const destinationValid = validHostedDestinationPath(destinationPath);
  const hosted = isWritableBasicHostedEnvironmentResource(environment, environmentId.trim())
    ? environment
    : null;

  useEffect(() => () => {
    requestRef.current += 1;
    abortRef.current?.abort();
  }, []);

  const start = (nextOperation: Exclude<Operation, null>) => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const request = requestRef.current + 1;
    requestRef.current = request;
    setOperation(nextOperation);
    return { controller, request };
  };

  const current = (request: number, controller: AbortController) =>
    request === requestRef.current && !controller.signal.aborted;

  const finish = (request: number, controller: AbortController) => {
    if (!current(request, controller)) return false;
    setOperation(null);
    return true;
  };

  const changeFile = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0] ?? null;
    setSelectedFile(file);
    setNotice(file && file.size > maxSourceBytes
      ? { tone: "error", text: "This file exceeds the 512 MiB Source File limit." }
      : { tone: "info", text: file ? `${file.name} is ready for one upload attempt.` : "Choose a local file to upload." });
  };

  const upload = async () => {
    if (!selectedFile || selectedFile.size > maxSourceBytes) return;
    const file = selectedFile;
    const { controller, request } = start("upload");
    setSelectedFile(null);
    setFileInputKey((value) => value + 1);
    setMetadata(null);
    setCopyResult(null);
    setCopyUnknown(false);
    setNotice({ tone: "info", text: "Uploading once. An uncertain response will not be retried." });
    try {
      const next = await operations.uploadSourceFile(
        { file, filename: file.name },
        { signal: controller.signal },
      );
      if (!finish(request, controller)) return;
      setMetadata(next);
      setFileId(next.id);
      setNotice({ tone: "success", text: "Source File uploaded and its durable ID was returned by Core." });
    } catch (error) {
      if (!current(request, controller) || isAbort(error)) return;
      finish(request, controller);
      setNotice(knownRejected(error)
        ? { tone: "error", text: safeFailure(error, "upload") }
        : {
            tone: "warning",
            text: "Upload outcome is unknown. Core provides no Source Files list, so Web cannot safely locate or retry this upload.",
          });
    }
  };

  const retrieve = async () => {
    if (!currentId) return;
    const { controller, request } = start("retrieve");
    setMetadata(null);
    setCopyResult(null);
    setCopyUnknown(false);
    try {
      const next = await operations.retrieveSourceFile(currentId, { signal: controller.signal });
      if (!finish(request, controller)) return;
      setMetadata(next);
      setNotice({ tone: "success", text: "Source File metadata retrieved. Content was not downloaded." });
    } catch (error) {
      if (!current(request, controller) || isAbort(error)) return;
      finish(request, controller);
      setNotice({ tone: "error", text: safeFailure(error, "retrieve") });
    }
  };

  const deleteFile = async () => {
    if (!currentId) return;
    const id = currentId;
    const { controller, request } = start("delete");
    setNotice({ tone: "info", text: "Deleting once. Web will not repeat an uncertain DELETE." });
    try {
      await operations.deleteSourceFile(id, { signal: controller.signal });
      if (!finish(request, controller)) return;
      if (metadata?.id === id) setMetadata(null);
      setCopyResult(null);
      setNotice({ tone: "success", text: "Source File deleted. Existing Workspace copies, if any, are independent." });
    } catch (error) {
      if (!current(request, controller) || isAbort(error)) return;
      if (error instanceof AgentCoreError && error.status === 404) {
        finish(request, controller);
        if (metadata?.id === id) setMetadata(null);
        setNotice({ tone: "success", text: "The Source File is absent from the current project." });
        return;
      }
      if (knownRejected(error)) {
        finish(request, controller);
        setNotice({ tone: "error", text: safeFailure(error, "delete") });
        return;
      }

      const reconcileController = new AbortController();
      abortRef.current = reconcileController;
      try {
        const next = await operations.retrieveSourceFile(id, { signal: reconcileController.signal });
        if (request !== requestRef.current || reconcileController.signal.aborted) return;
        setMetadata(next);
        setNotice({ tone: "warning", text: "DELETE was not confirmed; a single read found the Source File still present. Web did not repeat DELETE." });
      } catch (reconcileError) {
        if (request !== requestRef.current || reconcileController.signal.aborted || isAbort(reconcileError)) return;
        if (reconcileError instanceof AgentCoreError && reconcileError.status === 404) {
          if (metadata?.id === id) setMetadata(null);
          setNotice({ tone: "success", text: "The uncertain DELETE was reconciled by one read: the Source File is now absent." });
        } else {
          setNotice({ tone: "warning", text: "DELETE outcome remains unknown after one read-only reconciliation. Web did not repeat DELETE." });
        }
      } finally {
        if (request === requestRef.current && !reconcileController.signal.aborted) setOperation(null);
      }
    }
  };

  const inspectEnvironment = async () => {
    const id = environmentId.trim();
    if (!id) return;
    const { controller, request } = start("environment");
    setEnvironment(null);
    setCopyResult(null);
    setCopyUnknown(false);
    try {
      const resource = await operations.retrieveEnvironment(id, { signal: controller.signal });
      if (!finish(request, controller)) return;
      setEnvironment(resource);
      setNotice(resource.type === "openai_hosted"
        ? isWritableBasicHostedEnvironmentResource(resource, id)
          ? {
              tone: "success",
              text: "Core returned the exact non-terminal basic openai_hosted resource. Referenced copy controls are now available; status alone is not execution readiness.",
            }
          : {
              tone: "warning",
              text: "Core returned an openai_hosted resource, but it is terminal or exposes unsupported installation metadata. Write controls remain hidden.",
            }
        : {
            tone: "info",
            text: "Core returned a self_hosted Environment. It remains read-only in this Web; hosted write controls stay hidden.",
          });
    } catch (error) {
      if (!current(request, controller) || isAbort(error)) return;
      finish(request, controller);
      setNotice({ tone: "error", text: safeFailure(error, "environment") });
    }
  };

  const copyToEnvironment = async () => {
    if (!hosted || !sourceReady || !destinationValid || metadata.bytes > maxDestinationBytes || copyUnknown) return;
    const path = destinationPath;
    const source = metadata;
    const { controller, request } = start("copy");
    setCopyResult(null);
    setNotice({ tone: "info", text: "Copying once by Source File ID. The local filename or path is never used as file_id." });
    try {
      const result = await operations.createEnvironmentFile(hosted.id, {
        type: "file_id",
        file_id: source.id,
        path,
      }, { signal: controller.signal });
      if (!finish(request, controller)) return;
      setCopyResult(result);
      setNotice({ tone: "success", text: "Core confirmed the Workspace copy." });
    } catch (error) {
      if (!current(request, controller) || isAbort(error)) return;
      if (knownRejected(error)) {
        finish(request, controller);
        setNotice({ tone: "error", text: safeFailure(error, "copy") });
        return;
      }

      const reconcileController = new AbortController();
      abortRef.current = reconcileController;
      setCopyUnknown(true);
      try {
        const page = await operations.listEnvironmentFiles(hosted.id, {
          path: parentDirectory(path),
          limit: 100,
          order: "asc",
          signal: reconcileController.signal,
        });
        if (request !== requestRef.current || reconcileController.signal.aborted) return;
        const candidate = page.data.find((file) => file.path === path && file.size_bytes === source.bytes);
        setNotice({
          tone: "warning",
          text: candidate
            ? "Copy outcome is still unknown. One read found the same path and size, but that cannot prove byte identity; Web will not replay the write."
            : "Copy outcome is unknown and one read did not prove the destination. Web will not replay the write.",
        });
      } catch (reconcileError) {
        if (request !== requestRef.current || reconcileController.signal.aborted || isAbort(reconcileError)) return;
        setNotice({ tone: "warning", text: "Copy outcome remains unknown after one read-only reconciliation. Web will not replay the write." });
      } finally {
        if (request === requestRef.current && !reconcileController.signal.aborted) setOperation(null);
      }
    }
  };

  return (
    <section className="source-files-panel" aria-labelledby="source-files-heading">
      <header>
        <div>
          <FileUp size={15} strokeWidth={1.5} aria-hidden="true" />
          <div><h2 id="source-files-heading">Source Files</h2><p>Project-owned upload, metadata, Workspace copy and delete</p></div>
        </div>
        <span>{environmentFilesEnabled ? "512 MiB source · 50 MiB destination" : "512 MiB source"}</span>
      </header>

      <div className="source-files-grid">
        <div className="source-files-card">
          <h3>Upload once</h3>
          <label className="source-files-file-input">
            <span>Local file</span>
            <input key={fileInputKey} type="file" onChange={changeFile} disabled={busy} />
          </label>
          <button className="button primary" type="button" onClick={() => void upload()} disabled={busy || !selectedFile || selectedFile.size > maxSourceBytes}>
            <FileUp size={13} aria-hidden="true" />{operation === "upload" ? "Uploading…" : "Upload Source File"}
          </button>
          <p>Purpose is fixed to <code>user_data</code>. Upload has no idempotency key or list-based recovery.</p>
        </div>

        <div className="source-files-card">
          <h3>Operate by Core ID</h3>
          <label>
            <span>Source File ID</span>
            <input
              value={fileId}
              onChange={(event) => {
                setFileId(event.target.value);
                setMetadata(null);
                setCopyResult(null);
                setCopyUnknown(false);
              }}
              placeholder="file-…"
              autoComplete="off"
              spellCheck={false}
              disabled={busy}
            />
          </label>
          <div className="source-files-actions">
            <button className="button outline" type="button" onClick={() => void retrieve()} disabled={busy || !currentId}><Search size={12} />Retrieve</button>
            <button className="button outline danger" type="button" onClick={() => void deleteFile()} disabled={busy || !currentId}><Trash2 size={12} />Delete once</button>
          </div>
          <p>Uploaded Source Files cannot be downloaded directly. Copy them to a Workspace for execution.</p>
          <p>Core has no Source Files list API. Refreshing or reopening this page requires the ID again.</p>
        </div>
      </div>

      <div className={`source-files-notice source-files-notice-${notice.tone}`} role={notice.tone === "error" ? "alert" : "status"} aria-live="polite">
        {operation ? <RefreshCw className="refresh-spinning" size={13} aria-hidden="true" /> : null}<span>{notice.text}</span>
      </div>

      {metadata ? (
        <dl className="source-files-result" aria-label="Source File metadata">
          <div><dt>ID</dt><dd><code>{metadata.id}</code></dd></div>
          <div><dt>Filename</dt><dd>{metadata.filename}</dd></div>
          <div><dt>Bytes</dt><dd>{formatFileSize(metadata.bytes)}</dd></div>
          <div><dt>Status</dt><dd>{metadata.status} · stored bytes only, not scanned or indexed</dd></div>
        </dl>
      ) : null}

      {environmentFilesEnabled ? <section className="source-files-hosted" aria-labelledby="source-files-hosted-heading">
        <header>
          <div><FolderInput size={14} strokeWidth={1.5} aria-hidden="true" /><h3 id="source-files-hosted-heading">Copy to hosted Workspace</h3></div>
          <p>Write controls remain hidden until this exact Environment is retrieved as <code>openai_hosted</code>.</p>
        </header>
        <div className="source-files-hosted-check">
          <label>
            <span>Environment ID</span>
            <input
              value={environmentId}
              onChange={(event) => {
                setEnvironmentId(event.target.value);
                setEnvironment(null);
                setCopyResult(null);
                setCopyUnknown(false);
              }}
              autoComplete="off"
              spellCheck={false}
              disabled={busy}
            />
          </label>
          <button className="button outline" type="button" onClick={() => void inspectEnvironment()} disabled={busy || !environmentId.trim()}>
            <Search size={12} />{operation === "environment" ? "Checking…" : "Check Environment"}
          </button>
        </div>

        {hosted ? (
          <div className="source-files-hosted-write">
            <p className="source-files-hosted-qualified"><strong>Qualified resource:</strong> <code>{hosted.id}</code> · {hosted.status}</p>
            <label>
              <span>Destination path</span>
              <input
                value={destinationPath}
                onChange={(event) => {
                  setDestinationPath(event.target.value);
                  setCopyResult(null);
                  setCopyUnknown(false);
                }}
                aria-invalid={!destinationValid}
                placeholder="/workspace/input/notes.txt"
                disabled={busy}
              />
            </label>
            {!destinationValid ? <p className="source-files-validation">Use one canonical absolute file path beneath <code>/workspace/</code>; parent traversal and trailing slashes are rejected.</p> : null}
            {metadata && metadata.bytes > maxDestinationBytes ? <p className="source-files-validation">This Source File is retained, but it exceeds the 50 MiB destination-copy limit.</p> : null}
            <button
              className="button primary"
              type="button"
              onClick={() => void copyToEnvironment()}
              disabled={busy || !sourceReady || !destinationValid || metadata.bytes > maxDestinationBytes || copyUnknown}
            >
              <FolderInput size={13} />{operation === "copy" ? "Copying…" : copyUnknown ? "Outcome unknown — not replayed" : "Copy by Source File ID"}
            </button>
          </div>
        ) : null}

        {environment?.type === "self_hosted" ? (
          <p className="source-files-readonly">This is a <code>self_hosted</code> Environment. Web keeps its Workspace file surface read-only.</p>
        ) : null}
        {copyResult ? (
          <p className="source-files-copy-result"><strong>Copy confirmed:</strong> <code>{copyResult.path}</code> · {formatFileSize(copyResult.size_bytes)}</p>
        ) : null}
      </section> : null}
    </section>
  );
}
