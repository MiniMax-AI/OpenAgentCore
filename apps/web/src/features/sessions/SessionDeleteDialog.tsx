import { AgentCoreError, isSessionDeletionConflict } from "@oac/agents-client";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { ConfirmDialog } from "../../components/ConfirmDialog";
import { useToast } from "../../components/Toast";
import { projectClient } from "../../lib/projects";
import { shortId } from "../../lib/format";
import { type Project } from "../../lib/admin-view";

export interface SessionDeleteTarget {
  project: Project;
  sessionId: string;
}

export type SessionDeleteOutcome =
  | { kind: "deleted" }
  | { kind: "missing" }
  /** Core refused: the Session has work in flight. */
  | { kind: "not-idle" }
  | { kind: "rejected"; reason: string }
  /** No answer from Core; the Session may or may not be gone. Never retried automatically. */
  | { kind: "uncertain" };

/** Deletes one Session through its project's scope and classifies the answer. */
export async function deleteSession(target: SessionDeleteTarget): Promise<SessionDeleteOutcome> {
  try {
    await projectClient(target.project.id).deleteSession(target.sessionId);
    return { kind: "deleted" };
  } catch (error) {
    return classifyDeleteError(error);
  }
}

export function classifyDeleteError(error: unknown): SessionDeleteOutcome {
  if (isSessionDeletionConflict(error)) return { kind: "not-idle" };
  if (error instanceof AgentCoreError && error.status === 404) return { kind: "missing" };
  if (error instanceof AgentCoreError && error.status >= 400 && error.status < 500) return { kind: "rejected", reason: error.message };
  return { kind: "uncertain" };
}

/**
 * Confirmed deletion of one Session. Core deletes only idle or failed
 * Sessions without pending actions; a refusal stays in the dialog in plain
 * words. An unconfirmed outcome is not retried: `onUncertain` lets the page
 * read the truth again.
 */
export function SessionDeleteDialog({
  target,
  onClose,
  onDeleted,
  onUncertain,
}: {
  target: SessionDeleteTarget | null;
  onClose: () => void;
  onDeleted: (target: SessionDeleteTarget) => void;
  onUncertain: () => void;
}) {
  const { t } = useTranslation("sessions");
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  useEffect(() => {
    setBusy(false);
    setProblem(null);
  }, [target]);

  const confirm = async () => {
    if (!target || busy) return;
    setBusy(true);
    setProblem(null);
    const outcome = await deleteSession(target);
    setBusy(false);
    const id = shortId(target.sessionId);
    if (outcome.kind === "deleted" || outcome.kind === "missing") {
      toast.show(t(outcome.kind === "deleted" ? "delete.deleted" : "delete.alreadyDeleted", { id }), { tone: "success" });
      onDeleted(target);
      return;
    }
    if (outcome.kind === "uncertain") onUncertain();
    setProblem(outcome.kind === "not-idle" ? t("delete.notIdle") : outcome.kind === "rejected" ? t("delete.rejected", { reason: outcome.reason }) : t("delete.uncertain"));
  };

  return (
    <ConfirmDialog
      open={target !== null}
      title={t("delete.title")}
      confirmLabel={t("delete.confirm")}
      busyLabel={t("delete.deleting")}
      busy={busy}
      error={problem}
      onConfirm={() => void confirm()}
      onClose={onClose}
    >
      {target ? (
        <>
          <p>{t("delete.prompt", { id: target.sessionId, project: target.project.name })}</p>
          <p>{t("delete.consequence")}</p>
        </>
      ) : null}
    </ConfirmDialog>
  );
}
