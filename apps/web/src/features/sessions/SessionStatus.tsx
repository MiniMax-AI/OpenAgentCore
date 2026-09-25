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

export function SessionStatus({ session }: { session: AgentSession }) {
  const { t } = useTranslation("sessions");
  const waitingFor = useWaitingFor()(session);
  const key = statusKey(session.status);
  return (
    <span className="status-with-help">
      <StatusDot tone={sessionStatusTone[session.status] ?? "neutral"} label={t(`sessionStatus.${key}`)} />
      {session.status === "failed" && session.error ? <HelpTip label={t("log.errorLabel")}>{session.error}</HelpTip> : null}
      {session.status === "requires_action" && waitingFor.length ? <HelpTip label={t("log.waitingLabel")}>{waitingFor.join(" · ")}</HelpTip> : null}
    </span>
  );
}
