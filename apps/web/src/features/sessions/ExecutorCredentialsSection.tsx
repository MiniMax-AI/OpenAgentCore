import { AgentCoreError, type ExecutorCredential, type IssuedExecutorCredential } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, HelpTip, Section, StatusDot } from "../../components/console-ui";
import { ErrorDialog } from "../../components/ErrorDialog";
import { CopyableId, RowActions } from "../../components/list-ui";
import { Modal } from "../../components/Modal";
import { TableSkeleton } from "../../components/Skeleton";
import { failedLast, useFailureToast, useToast } from "../../components/Toast";
import { useDeleteFlow } from "../../lib/delete-flow";
import { formatDateTime, shortId } from "../../lib/format";
import { admin, useProjects } from "../../lib/projects";
import { ExecutorInstallPanel, useExecutorInstall } from "./ExecutorInstallPanel";
import { CredentialFile } from "./executor-credential-file";
import { executorConnectionQuery } from "./executor-connection-query";
import { boundExecutorCredential, executorConnectionState } from "./executor-connection";
import { ExecutorConnectionPanel } from "./ExecutorConnectionPanel";

/** An issuance that gets no answer in this time has an unknown outcome. */
const WRITE_TIMEOUT_MS = 30_000;

/** A key ID for a new credential. Plain-HTTP consoles lack `crypto.randomUUID`, so it falls back to a v4 UUID. */
function newKeyId(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6]! & 0x0f) | 0x40;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/**
 * A write either failed before Core acted ("rejected", with Core's reason) or
 * has an unknown outcome: no answer, a timeout, a 5xx or an unreadable reply.
 */
type WriteFailure = { kind: "rejected"; message: string; code: string | null } | { kind: "uncertain" };

function writeFailure(error: unknown): WriteFailure {
  if (error instanceof AgentCoreError && error.status >= 400 && error.status < 500 && error.status !== 408) {
    return { kind: "rejected", message: error.message, code: error.code ?? null };
  }
  return { kind: "uncertain" };
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error ?? "");
}

function sameKey(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase();
}

function seconds(value: string | null): number | null {
  return value === null ? null : Math.floor(Date.parse(value) / 1000);
}

/**
 * Executor credentials of a self-hosted Session's environment: Core issues,
 * rotates and revokes them with the deployment's Core key. An issued
 * credential is shown once, lives only in this component's state and is
 * forgotten when the administrator presses Done; dismissing the dialog keeps
 * it on the page. Writes are never retried automatically: an issuance with an
 * unknown outcome keeps its key ID, and after the list is refreshed the
 * administrator rotates it (issued, secret lost) or issues it again (not
 * issued). A kept key ID that is listed is never sent again: its row's actions
 * own it. Rotating a revoked credential restores it with a new secret, which
 * is how a host whose credential was revoked reconnects: Core retains
 * the bound key ID. An archived project's credentials are listed and
 * revoked but neither issued nor rotated. Below the list, Connect a host gives
 * the command that installs the executor with one of these credentials.
 */
export function ExecutorCredentialsSection({ projectId, sessionId, environmentId }: { projectId: string; sessionId: string; environmentId: string; remoteUrl: string; workspaceDirectory?: string }) {
  const { t, i18n } = useTranslation("sessions");
  const locale = i18n.resolvedLanguage;
  const toast = useToast();
  const { byId, refresh: refreshProjects } = useProjects();
  const archived = byId.get(projectId)?.status === "archived";
  const install = useExecutorInstall(projectId, environmentId, archived);
  const query = useQuery(executorConnectionQuery(projectId, sessionId, environmentId));
  const credentials = query.data?.data ?? null;
  const connectionStale = failedLast(query) || query.isStale;
  const connectionState = executorConnectionState(query.data, connectionStale);
  const boundCredential = boundExecutorCredential(query.data);
  const { refetch } = query;
  const reread = () => { void refetch(); };
  useFailureToast(credentials && failedLast(query) ? message(query.error) : null, t("executor.refreshFailed"), "executor-credentials-read");

  // The key ID of an issuance in flight or with an unknown outcome; the next Issue sends it again unless it is listed.
  const [kept, setKept] = useState<string | null>(null);
  const forget = (keyId: string) => setKept((current) => (current !== null && sameKey(current, keyId) ? null : current));
  const [issuing, setIssuing] = useState(false);
  // The unknown-outcome dialog; the key ID stays for its closing animation.
  const [uncertain, setUncertain] = useState<{ keyId: string; open: boolean } | null>(null);
  // Why a credential is rotated: an active one on request, a revoked one to restore it, or one whose secret was lost.
  const [rotation, setRotation] = useState<{ keyId: string; reason: "active" | "revoked" | "lost" } | null>(null);
  const [rotating, setRotating] = useState(false);
  const [rotationError, setRotationError] = useState<string | null>(null);
  // The credential shown once: in its dialog, or on the page once the dialog is dismissed, until Done.
  const [shown, setShown] = useState<{ credential: IssuedExecutorCredential; open: boolean; done: boolean } | null>(null);
  const revoke = useDeleteFlow<string>(
    (keyId) => admin.revokeExecutorCredential(projectId, environmentId, keyId).then(() => forget(keyId)),
    reread,
    { uncertain: t("executor.revokeDialog.uncertain") },
  );
  const held = shown !== null && !shown.done;
  // A held credential also blocks writes: another issuance would replace it.
  const busy = issuing || rotating || revoke.busy || held;

  // Done forgets the credential once its dialog has faded out.
  const done = shown?.done ?? false;
  useEffect(() => {
    if (!done) return;
    const timer = window.setTimeout(() => setShown(null), 250);
    return () => window.clearTimeout(timer);
  }, [done]);
  // A reload or a closed tab would lose a credential that is shown only once.
  useEffect(() => {
    if (!held) return;
    const guard = (event: BeforeUnloadEvent) => { event.preventDefault(); };
    window.addEventListener("beforeunload", guard);
    return () => window.removeEventListener("beforeunload", guard);
  }, [held]);

  const openRotation = (keyId: string, reason: "active" | "revoked" | "lost") => { setRotationError(null); setRotation({ keyId, reason }); };

  // Reads the list again and finds a key ID in it; undefined when the read failed.
  const findListed = async (keyId: string): Promise<ExecutorCredential | null | undefined> => {
    const result = await refetch();
    if (!result.data || result.isError) return undefined;
    return result.data.data.find((credential) => sameKey(credential.key_id, keyId)) ?? null;
  };
  // An issued key ID whose secret never arrived is rotated for a fresh one, if it is still active.
  const recoverLost = (found: ExecutorCredential): boolean => {
    const rotatable = found.revoked_at === null && !archived;
    if (rotatable) openRotation(found.key_id, "lost");
    return rotatable;
  };

  const issue = async () => {
    if (busy) return;
    // A listed key ID is managed from its row; reusing it would recover a credential that was since rotated or revoked.
    const keyId = kept !== null && !credentials?.some((credential) => sameKey(credential.key_id, kept)) ? kept : newKeyId();
    setKept(keyId);
    setIssuing(true);
    try {
      const credential = await admin.issueExecutorCredential(projectId, environmentId, { key_id: keyId, rotate: false }, { signal: AbortSignal.timeout(WRITE_TIMEOUT_MS) });
      setKept(null);
      setShown({ credential, open: true, done: false });
      reread();
    } catch (error) {
      const failure = writeFailure(error);
      if (failure.kind === "uncertain") {
        setUncertain({ keyId, open: true });
        return;
      }
      setKept(null);
      if (failure.code === "executor_credential_exists") {
        // An earlier attempt with this key ID may have been issued after all, its secret lost.
        const found = await findListed(keyId);
        if (!found || !recoverLost(found)) {
          toast.show(t("executor.issueRejected"), { tone: "error", detail: found?.revoked_at ? t("executor.existsRevoked", { id: shortId(keyId) }) : failure.message });
        }
        return;
      }
      if (failure.code === "project_archived") {
        // Archived meanwhile: read the project again so issuing and rotating disappear.
        toast.show(t("executor.issueRejected"), { tone: "error", detail: t("executor.archived") });
        refreshProjects();
      } else {
        toast.show(t("executor.issueRejected"), { tone: "error", detail: failure.message });
      }
      reread();
    } finally {
      setIssuing(false);
    }
  };

  // After an unknown outcome: issued (rotate it for a fresh secret) or not (the next Issue reuses the key ID).
  const checkKept = async (keyId: string) => {
    const found = await findListed(keyId);
    if (found === undefined) return;
    if (found === null) {
      toast.show(t("executor.notIssued", { id: shortId(keyId) }), { tone: "info" });
      return;
    }
    setKept(null);
    recoverLost(found);
  };

  const rotate = async () => {
    if (!rotation || busy) return;
    setRotating(true);
    setRotationError(null);
    try {
      const credential = await admin.issueExecutorCredential(projectId, environmentId, { key_id: rotation.keyId, rotate: true }, { signal: AbortSignal.timeout(WRITE_TIMEOUT_MS) });
      setRotation(null);
      forget(rotation.keyId);
      setShown({ credential, open: true, done: false });
    } catch (error) {
      const failure = writeFailure(error);
      if (failure.kind === "rejected" && failure.code === "project_archived") {
        setRotationError(t("executor.archived"));
        refreshProjects();
      } else {
        setRotationError(failure.kind === "uncertain" ? t("executor.rotateDialog.uncertain") : t("executor.rotateDialog.rejected", { reason: failure.message }));
      }
    } finally {
      setRotating(false);
      reread();
    }
  };

  const issueButton = archived ? null : (
    <button className="button outline" type="button" onClick={() => void issue()} disabled={busy}>
      <Plus size={14} aria-hidden="true" />{issuing ? t("executor.issuing") : t("executor.issue")}
    </button>
  );

  let body;
  if (!credentials) {
    body = query.isError
      ? <EmptyState title={t("executor.loadFailed")} description={message(query.error)} />
      : <TableSkeleton label={t("executor.loading")} rows={2} columns={4} />;
  } else if (!credentials.length) {
    body = <EmptyState title={t("executor.empty")} />;
  } else {
    body = (
      <div className="table-frame">
        <table className="data-table executor-credentials-table" aria-label={t("executor.title")}>
          <thead>
            <tr>
              <th scope="col">{t("executor.columns.credential")}</th>
              <th scope="col">{t("executor.columns.created")}</th>
              <th scope="col">{t("executor.columns.status")}</th>
              <th scope="col"><span className="visually-hidden">{t("log.actions")}</span></th>
            </tr>
          </thead>
          <tbody>
            {credentials.map((credential) => {
              const revoked = credential.revoked_at !== null;
              const bound = boundCredential?.key_id === credential.key_id;
              const id = shortId(credential.key_id);
              return (
                <tr key={credential.key_id} className={revoked ? "executor-credential-revoked" : undefined}>
                  <th scope="row"><span className="name-cell">
                    <CopyableId id={credential.key_id} compact label={t("executor.copyKeyId")} />
                    {bound ? <span className="executor-credential-binding">
                      <span className="pill">{t("executor.connection.boundKey")}</span>
                      <HelpTip>{t(revoked ? "executor.connection.restore" : "executor.connection.bound", { id })}</HelpTip>
                    </span> : null}
                  </span></th>
                  <td className="session-nowrap">{formatDateTime(seconds(credential.created_at), locale)}</td>
                  <td>
                    <span className="executor-credential-status">
                      <StatusDot tone={revoked ? "neutral" : "ok"} label={revoked ? t("executor.status.revoked") : t("executor.status.active")} />
                      {revoked ? <span className="executor-credential-date">{formatDateTime(seconds(credential.revoked_at), locale)}</span> : null}
                    </span>
                  </td>
                  <td className="actions-cell">
                    {revoked && archived ? null : (
                      <RowActions>
                        {archived ? null : (
                          <button className="text-action" type="button" aria-label={bound ? t(revoked ? "executor.connection.rotateRestore" : "executor.connection.rotateBound") : t(revoked ? "executor.restoreLabel" : "executor.rotateLabel", { id })} disabled={busy || (bound && connectionStale)} onClick={() => openRotation(credential.key_id, revoked ? "revoked" : "active")}>
                            {bound ? t(revoked ? "executor.connection.rotateRestore" : "executor.connection.rotateBound") : t(revoked ? "executor.restore" : "executor.rotate")}
                          </button>
                        )}
                        {revoked ? null : (
                          <button className="text-action danger" type="button" aria-label={t("executor.revokeLabel", { id })} disabled={busy} onClick={() => revoke.ask(credential.key_id)}>
                            {t("executor.revoke")}
                          </button>
                        )}
                      </RowActions>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    );
  }

  // Dismissing the dialog keeps the credential on the page; only Done forgets it.
  const dismissShown = () => setShown((current) => current && { ...current, open: false });
  const finishShown = () => setShown((current) => current && { ...current, open: false, done: true });

  return (
    <Section headingId="session-executor-heading" title={t("executor.title")} help={t("executor.help")} actions={issueButton}>
      {archived ? <p className="coverage-note">{t("executor.archived")}</p> : null}
      {shown && !shown.open && !shown.done ? (
        <section className="executor-credential-pending" aria-label={t("executor.issued.title")}>
          <CredentialFile credential={shown.credential} />
          <div><button className="button outline" type="button" onClick={finishShown}>{t("executor.issued.done")}</button></div>
        </section>
      ) : null}
      <ExecutorConnectionPanel read={query.data} stale={connectionStale} failed={failedLast(query)} refreshing={query.isFetching} onRefresh={reread} />
      <ExecutorInstallPanel install={install} archived={archived} connected={connectionState === "connected"} />
      {body}
      <Modal
        open={shown?.open ?? false}
        title={t("executor.issued.title")}
        onClose={dismissShown}
        footer={<button className="button outline" type="button" onClick={finishShown}>{t("executor.issued.done")}</button>}
      >
        {shown ? <CredentialFile credential={shown.credential} /> : null}
      </Modal>
      <ErrorDialog
        open={uncertain?.open ?? false}
        title={t("executor.uncertain.title")}
        action={uncertain ? { label: t("executor.uncertain.refresh"), onClick: () => void checkKept(uncertain.keyId) } : undefined}
        onClose={() => setUncertain((current) => current && { ...current, open: false })}
      >
        <p>{t("executor.uncertain.body", { id: uncertain ? shortId(uncertain.keyId) : "" })}</p>
        <p>{t("executor.uncertain.next")}</p>
      </ErrorDialog>
      <ConfirmDialog
        open={rotation !== null}
        title={t(rotation?.reason === "revoked" ? "executor.rotateDialog.restoreTitle" : "executor.rotateDialog.title")}
        confirmLabel={t(rotation?.reason === "revoked" ? "executor.rotateDialog.restoreConfirm" : "executor.rotateDialog.confirm")}
        busyLabel={t(rotation?.reason === "revoked" ? "executor.rotateDialog.restoreBusy" : "executor.rotateDialog.busy")}
        busy={rotating}
        error={rotationError}
        onConfirm={() => void rotate()}
        onClose={() => { if (!rotating) setRotation(null); }}
      >
        {rotation ? (
          <>
            <p>{t(`executor.rotateDialog.${rotation.reason}`, { id: shortId(rotation.keyId) })}</p>
            {rotation.reason === "revoked" ? null : <p>{t("executor.rotateDialog.consequence")}</p>}
            {/* Reconnection uses the installed daemon and the replacement credential file. */}
            {rotation.reason === "lost" ? null : <p>{t(`executor.rotateDialog.${rotation.reason === "revoked" ? "reconnect" : "disconnect"}`)}</p>}
          </>
        ) : null}
      </ConfirmDialog>
      <ConfirmDialog
        open={revoke.target !== null}
        title={t("executor.revokeDialog.title")}
        confirmLabel={t("executor.revokeDialog.confirm")}
        busyLabel={t("executor.revokeDialog.busy")}
        busy={revoke.busy}
        error={revoke.error}
        onConfirm={() => void revoke.confirm()}
        onClose={revoke.cancel}
      >
        {revoke.target ? (
          <>
            <p>{t("executor.revokeDialog.prompt", { id: shortId(revoke.target) })}</p>
            <p>{t("executor.revokeDialog.consequence")}</p>
          </>
        ) : null}
      </ConfirmDialog>
    </Section>
  );
}
