import type { AgentSession } from "@oac/agents-client";
import { useTranslation } from "react-i18next";

import { HelpTip, StatusDot, type Tone } from "../../components/console-ui";
import { SessionFailure } from "./session-diagnostics";
import { statusKey } from "./session-log";

export const sessionStatusTone: Record<string, Tone> = { in_progress: "pending", requires_action: "warning", failed: "danger", idle: "neutral" };

/** What a Session waiting for input waits for, in words. */
export function useWaitingFor() {
  const { t } = useTranslation("sessions");
  return (session: AgentSession): string[] => session.required_actions.map((action) => (
    action.type === "function_call" ? t("detail.functionCall", { name: action.name }) : t("detail.environmentConnection")
  ));
}

/**
 * Lists add one truncated line under the status: a failure's reason or what
 * the Session waits for. The Session page shows the status alone; its failure
 * reads under the facts and its required actions in their own fact.
 */
export function SessionStatus({ session, truncate = false, projectId }: { session: AgentSession; truncate?: boolean; projectId: string }) {
  const { t } = useTranslation("sessions");
  const waitingFor = useWaitingFor()(session);
  const key = statusKey(session.status);
  const status = (
    <span className="status-with-help">
      <StatusDot tone={sessionStatusTone[session.status] ?? "neutral"} label={t(`sessionStatus.${key}`)} />
      {truncate && session.required_actions.some((action) => action.type === "function_call") ? <HelpTip label={t("log.waitingLabel")}>{t("detail.applicationAction")}</HelpTip> : null}
    </span>
  );
  if (!truncate) return status;
  if (session.status === "failed") return <span className="session-status session-status-truncated">{status}<SessionFailure projectId={projectId} session={session} /></span>;
  const reason = session.status === "requires_action" ? waitingFor.join(" · ") : "";
  if (!reason) return status;
  return (
    <span className="session-status session-status-truncated">
      {status}
      <span className="session-status-reason" title={reason}>{reason}</span>
    </span>
  );
}
