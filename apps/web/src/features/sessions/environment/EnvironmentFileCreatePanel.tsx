import { FilePlus2 } from "lucide-react";
import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";

import type { AgentCore, EnvironmentFile } from "@agents-core-web/agents-client";

import {
  createEnvironmentFileCreateGate,
  maxInlineEnvironmentFileBytes,
  submitInlineEnvironmentFile,
  validInlineEnvironmentFilePath,
} from "./environment-file-create";
import "./EnvironmentFileCreatePanel.css";

interface SelectedFileInfo {
  name: string;
  size: number;
}

export interface EnvironmentFileCreatePanelProps {
  environmentId: string;
  workspaceDirectory: string;
  onCreateFile: AgentCore["createEnvironmentFile"];
}

function destinationPlaceholder(workspaceDirectory: string): string {
  const root = workspaceDirectory.replace(/\/+$/u, "");
  return root === "/workspace" || root.startsWith("/workspace/")
    ? `${root}/input.txt`
    : "/workspace/input.txt";
}

export function EnvironmentFileCreatePanel({
  environmentId,
  workspaceDirectory,
  onCreateFile,
}: EnvironmentFileCreatePanelProps) {
  const fileRef = useRef<File | null>(null);
  const gateRef = useRef(createEnvironmentFileCreateGate());
  const generationRef = useRef(0);
  const [selectedFile, setSelectedFile] = useState<SelectedFileInfo | null>(null);
  const [fileInputKey, setFileInputKey] = useState(0);
  const [path, setPath] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [createdFile, setCreatedFile] = useState<EnvironmentFile | null>(null);
  const pathValid = validInlineEnvironmentFilePath(path);
  const sizeValid = selectedFile === null || selectedFile.size <= maxInlineEnvironmentFileBytes;

  useEffect(() => {
    generationRef.current += 1;
    fileRef.current = null;
    setSelectedFile(null);
    setFileInputKey((value) => value + 1);
    setPath("");
    setSubmitting(false);
    setMessage(null);
    setCreatedFile(null);
  }, [environmentId, workspaceDirectory]);

  const chooseFile = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0] ?? null;
    fileRef.current = file;
    setSelectedFile(file ? { name: file.name, size: file.size } : null);
    setMessage(null);
    setCreatedFile(null);
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (gateRef.current.pending) return;

    const generation = generationRef.current;
    const draft = { file: fileRef.current, path };
    setSubmitting(true);
    setMessage(null);
    setCreatedFile(null);
    const result = await submitInlineEnvironmentFile(
      gateRef.current,
      environmentId,
      draft,
      onCreateFile,
    );
    if (generation !== generationRef.current || result.kind === "ignored") return;

    setSubmitting(false);
    if (result.kind === "success") {
      fileRef.current = null;
      setSelectedFile(null);
      setFileInputKey((value) => value + 1);
      setPath("");
      setCreatedFile(result.file);
      setMessage(null);
      return;
    }
    setMessage(result.message);
  };

  return (
    <section className="environment-file-create" aria-labelledby="environment-file-create-heading">
      <header>
        <FilePlus2 size={14} strokeWidth={1.5} aria-hidden="true" />
        <div>
          <strong id="environment-file-create-heading">Add inline Workspace file</strong>
          <small>Explicit local selection · one write attempt · 5 MiB maximum</small>
        </div>
      </header>

      <form onSubmit={(event) => void submit(event)} noValidate>
        <label>
          <span>Local file</span>
          <input
            key={fileInputKey}
            type="file"
            onChange={chooseFile}
            disabled={submitting}
          />
        </label>
        {selectedFile ? (
          <p className="environment-file-create-selection">
            <strong>{selectedFile.name}</strong> · {selectedFile.size} bytes selected
          </p>
        ) : null}
        {!sizeValid ? (
          <p className="environment-file-create-validation">This file exceeds the 5 MiB inline-content limit.</p>
        ) : null}

        <label>
          <span>Destination path</span>
          <input
            type="text"
            value={path}
            onChange={(event) => {
              setPath(event.target.value);
              setMessage(null);
              setCreatedFile(null);
            }}
            placeholder={destinationPlaceholder(workspaceDirectory)}
            aria-invalid={path.length > 0 && !pathValid}
            disabled={submitting}
            autoComplete="off"
            spellCheck={false}
          />
        </label>
        {path.length > 0 && !pathValid ? (
          <p className="environment-file-create-validation">
            Use a canonical absolute file path beneath <code>/workspace/</code>. Parent traversal, dot segments, repeated separators, backslashes, and trailing slashes are rejected.
          </p>
        ) : null}

        {message ? <p className="environment-file-create-error" role="alert">{message}</p> : null}
        {createdFile ? (
          <p className="environment-file-create-success" role="status">
            <strong>Write confirmed:</strong> <code>{createdFile.path}</code> · {createdFile.size_bytes} bytes
          </p>
        ) : null}

        <button
          className="button primary"
          type="submit"
          disabled={submitting || !selectedFile || !sizeValid || !pathValid}
        >
          <FilePlus2 size={13} aria-hidden="true" />
          {submitting ? "Writing once…" : "Write selected file"}
        </button>
      </form>

      <p className="environment-file-create-boundary">
        The selected bytes are read only for this explicit request and are not stored by Web. A confirmed file write does not start a Turn or prove hosted runtime or executor readiness.
      </p>
    </section>
  );
}
