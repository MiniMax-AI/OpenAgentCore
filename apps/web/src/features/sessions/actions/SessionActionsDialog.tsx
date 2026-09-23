import { Pencil, Trash2 } from "lucide-react";
import { createPortal } from "react-dom";
import { useEffect, useRef, useState, type FormEvent } from "react";

import type { AgentSession } from "@agents-core-web/agents-client";

import { SessionPlacement } from "../../sandbox/SessionPlacement";
import { Modal } from "../../../components/Modal";
import {
  SessionActionError,
  SessionMetadataConflictError,
  rebaseSessionMetadataDraft,
  validateSessionMetadata,
  valuesFromMetadata,
  valuesFromSession,
  type SessionMetadataValues,
} from "./session-actions";

type DialogMode = "detail" | "edit" | "delete";

interface SessionActionsDialogProps {
  busy: boolean;
  session: AgentSession | null;
  onClose: () => void;
  onDelete: (sessionId: string) => Promise<boolean>;
  onCancelAndDelete: (sessionId: string) => Promise<boolean>;
  onDeleted: (sessionId: string) => void;
  onRetrieve: (sessionId: string) => Promise<AgentSession | undefined>;
  onUpdate: (
    sessionId: string,
    baselineMetadata: Record<string, string>,
    draftMetadata: Record<string, string>,
  ) => Promise<AgentSession | undefined>;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "The Session request failed.";
}

function formatTimestamp(seconds: number): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" })
    .format(new Date(seconds * 1000));
}

function sessionTitle(session: AgentSession): string {
  return session.metadata.title || session.agent.name || "Untitled Session";
}

function StructuredMetadata({ metadata }: { metadata: Record<string, string> }) {
  return <pre className="session-structured-value">{JSON.stringify(metadata, null, 2)}</pre>;
}

export function SessionDetails({ session }: { session: AgentSession }) {
  return (
    <div className="session-details">
      <dl>
        <div><dt>ID</dt><dd><code>{session.id}</code></dd></div>
        <div><dt>Status</dt><dd>{session.status.replaceAll("_", " ")}</dd></div>
        <div><dt>Created</dt><dd><time dateTime={new Date(session.created_at * 1000).toISOString()}>{formatTimestamp(session.created_at)}</time></dd></div>
        <div><dt>Last active</dt><dd><time dateTime={new Date(session.last_active_at * 1000).toISOString()}>{formatTimestamp(session.last_active_at)}</time></dd></div>
        <div><dt>Agent</dt><dd>{session.agent.name || <span className="agent-null-value">Untitled Agent</span>}</dd></div>
        <SessionPlacement key={session.id} sessionId={session.id} />
        <div><dt>Model</dt><dd><code>{session.agent.model}</code></dd></div>
        <div><dt>Metadata</dt><dd><StructuredMetadata metadata={session.metadata} /></dd></div>
      </dl>
      <div className="session-metadata-warning" role="note">
        Session metadata is durable Core data. Never store credentials, access tokens, private keys, or other secrets here.
      </div>
    </div>
  );
}

export function SessionDeleteConfirmation({ session, busy = false }: { session: AgentSession; busy?: boolean }) {
  return (
    <div className="session-delete-confirmation">
      <p>Delete <strong>{sessionTitle(session)}</strong> from Agent Core?</p>
      <p className="session-delete-target">Exact Session: <code>{session.id}</code></p>
      <p>The Web removes this Session only after Core confirms success. A missing, conflicting, unavailable, or uncertain response leaves the current durable view in place and is never retried automatically.</p>
      <p>Parsar deletion follows server lifecycle semantics. It is not a promise of physical history erasure, immediate native executor shutdown, or deletion of executor Workspace files.</p>
      {busy ? (
        <p className="session-delete-busy">Cancel work and delete sends one cancellation for the current work, waits until Core reports the Session idle or failed, and then sends one deletion. Input still waiting for its Environment cannot be cancelled; wait for it to start or expire.</p>
      ) : null}
    </div>
  );
}

interface SessionMetadataFormProps {
  disabled?: boolean;
  formId: string;
  session: AgentSession;
  onSubmit: (metadata: Record<string, string>) => Promise<void> | void;
  replacement?: { revision: number; values: SessionMetadataValues } | null;
}

export function SessionMetadataForm({ disabled = false, formId, session, onSubmit, replacement = null }: SessionMetadataFormProps) {
  const [values, setValues] = useState<SessionMetadataValues>(() => valuesFromSession(session));
  const [metadataError, setMetadataError] = useState<string | null>(null);

  useEffect(() => {
    if (!replacement) return;
    setValues(replacement.values);
    setMetadataError(null);
  }, [replacement]);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const result = validateSessionMetadata(values);
    setMetadataError(result.metadataError ?? null);
    if (!result.metadata) return;
    void onSubmit(result.metadata);
  };

  return (
    <form className="form-stack" id={formId} onSubmit={submit}>
      <label className="field">
        <span>Title</span>
        <input
          autoFocus
          aria-label="Session title"
          value={values.title}
          onChange={(event) => setValues((current) => ({ ...current, title: event.target.value }))}
          placeholder={session.agent.name || "Untitled Session"}
          disabled={disabled}
        />
        <small>A blank title falls back to the durable Agent snapshot name.</small>
      </label>
      <label className="field">
        <span>Additional metadata</span>
        <textarea
          className="session-metadata-input"
          aria-label="Additional Session metadata"
          value={values.metadata}
          onChange={(event) => {
            setValues((current) => ({ ...current, metadata: event.target.value }));
            if (metadataError) setMetadataError(null);
          }}
          aria-describedby={`${formId}-metadata-help${metadataError ? ` ${formId}-metadata-error` : ""}`}
          aria-invalid={Boolean(metadataError)}
          rows={8}
          spellCheck={false}
          disabled={disabled}
        />
        <small id={`${formId}-metadata-help`}>JSON object with string values only. Edit title above.</small>
        {metadataError ? <small className="field-error" id={`${formId}-metadata-error`} role="alert">{metadataError}</small> : null}
      </label>
      <div className="session-metadata-warning" role="note">
        Never store credentials, bearer tokens, API keys, passwords, or other secrets in Session metadata.
      </div>
    </form>
  );
}

export function SessionActionsDialog({
  busy,
  session,
  onClose,
  onDelete,
  onCancelAndDelete,
  onDeleted,
  onRetrieve,
  onUpdate,
}: SessionActionsDialogProps) {
  const [mode, setMode] = useState<DialogMode>("detail");
  const [current, setCurrent] = useState<AgentSession | null>(session);
  const [detailLoading, setDetailLoading] = useState(false);
  const [pending, setPending] = useState(false);
  const [deleteRetryBlocked, setDeleteRetryBlocked] = useState(false);
  // Core refused deletion because the Session still has work or pending input.
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [formReplacement, setFormReplacement] = useState<{
    revision: number;
    values: SessionMetadataValues;
  } | null>(null);
  const requestRef = useRef(0);
  const uncertainDeleteSessionRef = useRef<string | null>(null);
  const editActionRef = useRef<HTMLButtonElement>(null);
  const deleteCancelRef = useRef<HTMLButtonElement>(null);
  const restoreDetailFocus = useRef(false);
  const formId = "edit-session-metadata";

  useEffect(() => {
    requestRef.current += 1;
    const request = requestRef.current;
    setCurrent(session);
    setMode("detail");
    setActionError(null);
    setFormReplacement(null);
    setPending(false);
    setDeleteBusy(false);
    setDeleteRetryBlocked(uncertainDeleteSessionRef.current === session?.id);
    setDetailLoading(Boolean(session));
    if (!session) return;

    void onRetrieve(session.id).then((latest) => {
      if (request !== requestRef.current || !latest) return;
      setCurrent(latest);
      if (uncertainDeleteSessionRef.current === session.id) {
        uncertainDeleteSessionRef.current = null;
        setDeleteRetryBlocked(false);
      }
    }).catch((error: unknown) => {
      if (request === requestRef.current) setActionError(errorMessage(error));
    }).finally(() => {
      if (request === requestRef.current) setDetailLoading(false);
    });

    return () => {
      requestRef.current += 1;
    };
  }, [onRetrieve, session]);

  useEffect(() => {
    if (mode !== "detail" || busy || pending || detailLoading || !restoreDetailFocus.current) return;
    const frame = window.requestAnimationFrame(() => {
      editActionRef.current?.focus();
      restoreDetailFocus.current = false;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [busy, current, detailLoading, mode, pending]);

  useEffect(() => {
    if (mode !== "delete" || pending) return;
    const frame = window.requestAnimationFrame(() => deleteCancelRef.current?.focus());
    return () => window.cancelAnimationFrame(frame);
  }, [mode, pending]);

  const close = (force = false) => {
    if (pending && !force) return;
    requestRef.current += 1;
    restoreDetailFocus.current = false;
    onClose();
  };

  const returnToDetail = () => {
    if (pending) return;
    if (!deleteRetryBlocked) setActionError(null);
    setDeleteBusy(false);
    restoreDetailFocus.current = true;
    setMode("detail");
  };

  const submitUpdate = async (draftMetadata: Record<string, string>) => {
    if (!current || pending) return;
    const request = requestRef.current + 1;
    requestRef.current = request;
    setActionError(null);
    setPending(true);
    try {
      const updated = await onUpdate(current.id, current.metadata, draftMetadata);
      if (request !== requestRef.current || !updated) return;
      setCurrent(updated);
      restoreDetailFocus.current = true;
      setMode("detail");
    } catch (error) {
      if (request === requestRef.current) {
        if (error instanceof SessionMetadataConflictError && error.latestSession) {
          const rebasedDraft = rebaseSessionMetadataDraft(
            current.metadata,
            draftMetadata,
            error.latestSession.metadata,
          );
          setCurrent(error.latestSession);
          setFormReplacement((replacement) => ({
            revision: (replacement?.revision ?? 0) + 1,
            values: valuesFromMetadata(rebasedDraft),
          }));
        }
        setActionError(errorMessage(error));
      }
    } finally {
      if (request === requestRef.current) setPending(false);
    }
  };

  const confirmDelete = async (cancelFirst = false) => {
    if (!current || pending) return;
    const request = requestRef.current + 1;
    requestRef.current = request;
    setActionError(null);
    setPending(true);
    try {
      const confirmed = await (cancelFirst ? onCancelAndDelete : onDelete)(current.id);
      if (request !== requestRef.current) return;
      if (!confirmed) {
        throw new Error("The deletion confirmation belongs to an earlier Core connection. The current Core view and draft were kept.");
      }
      onDeleted(current.id);
      close(true);
    } catch (error) {
      if (request === requestRef.current) {
        if (error instanceof SessionActionError && error.kind === "unknown_write") {
          uncertainDeleteSessionRef.current = current.id;
          setDeleteRetryBlocked(true);
        }
        // Offer cancellation only for work it can stop; pending input cannot be cancelled.
        if (error instanceof SessionActionError && error.kind === "session_busy") setDeleteBusy(true);
        if (error instanceof SessionActionError && error.kind === "session_input_pending") setDeleteBusy(false);
        setActionError(errorMessage(error));
      }
    } finally {
      if (request === requestRef.current) setPending(false);
    }
  };

  const unavailable = busy || pending || detailLoading;
  const title = mode === "edit"
    ? "Edit Session metadata"
    : mode === "delete"
      ? "Delete Session?"
      : current ? sessionTitle(current) : "Session details";
  const footer = !current ? (
    <button className="button outline" type="button" onClick={() => close()}>Close</button>
  ) : mode === "edit" ? (
    <>
      <button className="button outline" type="button" onClick={returnToDetail} disabled={pending}>Cancel</button>
      <button className="button primary" type="submit" form={formId} disabled={unavailable}>
        {pending ? "Saving…" : "Save changes"}
      </button>
    </>
  ) : mode === "delete" ? (
    <>
      <button ref={deleteCancelRef} className="button outline" type="button" onClick={returnToDetail} disabled={pending}>Cancel</button>
      {deleteBusy ? (
        <button className="button danger" type="button" onClick={() => void confirmDelete(true)} disabled={unavailable || deleteRetryBlocked}>
          {pending ? "Cancelling and deleting…" : "Cancel work and delete"}
        </button>
      ) : (
        <button className="button danger" type="button" onClick={() => void confirmDelete()} disabled={unavailable || deleteRetryBlocked}>
          {pending ? "Deleting…" : "Delete Session"}
        </button>
      )}
    </>
  ) : (
    <>
      <button className="button danger" type="button" onClick={() => { setActionError(null); setMode("delete"); }} disabled={unavailable || deleteRetryBlocked}>
        <Trash2 size={14} strokeWidth={1.5} /> Delete
      </button>
      <button className="button outline" type="button" onClick={() => close()}>Close</button>
      <button ref={editActionRef} className="button primary" type="button" onClick={() => { setActionError(null); setFormReplacement(null); setMode("edit"); }} disabled={unavailable}>
        <Pencil size={14} strokeWidth={1.5} /> Edit
      </button>
    </>
  );

  const dialog = (
    <div className="session-actions-dialog">
      <Modal open={Boolean(session)} title={title} onClose={() => close()} footer={footer}>
        {actionError ? <div className="session-action-error" role="alert">{actionError}</div> : null}
        {detailLoading ? <div className="session-detail-loading" role="status">Retrieving the latest durable Session…</div> : null}
        {current && mode === "detail" ? <SessionDetails session={current} /> : null}
        {current && mode === "edit" ? (
          <SessionMetadataForm
            formId={formId}
            session={current}
            disabled={pending}
            onSubmit={submitUpdate}
            replacement={formReplacement}
          />
        ) : null}
        {current && mode === "delete" ? <SessionDeleteConfirmation session={current} busy={deleteBusy} /> : null}
      </Modal>
    </div>
  );
  return typeof document === "undefined" ? dialog : createPortal(dialog, document.body);
}
