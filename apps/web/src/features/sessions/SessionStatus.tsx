import type { AgentSession } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";

import { HelpTip, StatusDot, type Tone } from "../../components/console-ui";
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
 * Failures stay visible under the status, truncated only in lists. Required
 * actions appear here in lists and in their own facts on the Session page.
 */
export function SessionStatus({ session, truncate = false }: { session: AgentSession; truncate?: boolean }) {
  const { t } = useTranslation("sessions");
  const waitingFor = useWaitingFor()(session);
  const key = statusKey(session.status);
  const reason = session.status === "failed" ? session.error : session.status === "requires_action" && truncate ? waitingFor.join(" · ") : null;
  const status = (
    <span className="status-with-help">
      <StatusDot tone={sessionStatusTone[session.status] ?? "neutral"} label={t(`sessionStatus.${key}`)} />
      {truncate && session.required_actions.some((action) => action.type === "function_call") ? <HelpTip label={t("log.waitingLabel")}>{t("detail.applicationAction")}</HelpTip> : null}
    </span>
  );
  if (!reason) return status;
  return (
    <span className={truncate ? "session-status session-status-truncated" : "session-status"}>
      {status}
      <span className="session-status-reason" title={truncate ? reason : undefined}>{reason}</span>
    </span>
  );
}
