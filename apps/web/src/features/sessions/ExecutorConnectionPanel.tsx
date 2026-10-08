import type { ExecutorCredentialList } from "@oac/agents-client";
import { useId } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip, RefreshButton, StatusDot } from "../../components/console-ui";
import { executorConnectionState } from "./executor-connection";
import "./executor-connection.css";

export function ExecutorConnectionPanel({ read, stale, failed, refreshing, onRefresh }: {
  read: ExecutorCredentialList | undefined;
  stale: boolean;
  failed: boolean;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const { t } = useTranslation("sessions");
  const { t: tCommon } = useTranslation("common");
  const headingId = useId();
  const state = executorConnectionState(read, stale);
  return (
    <section className="executor-connection" aria-labelledby={headingId}>
      <div className="executor-connection-heading">
        <h3 id={headingId}>{t("executor.connection.title")}</h3>
        <StatusDot tone={state === "connected" ? "ok" : state === "revoked" || state === "disconnected" ? "warning" : "neutral"} label={t(`executor.connection.status.${state}`)} />
        <HelpTip>{t("executor.connection.help")}</HelpTip>
        <RefreshButton onClick={onRefresh} refreshing={refreshing} label={`${tCommon("actions.refresh")} · ${t("executor.connection.title")}`} />
      </div>
      {stale && read ? <p className="executor-connection-warning" role="status">{t(failed ? "executor.connection.stale" : "executor.connection.refreshing")}</p> : null}
      {!read && failed ? <p className="executor-connection-warning" role="status">{t("executor.connection.failed")}</p> : null}
    </section>
  );
}
