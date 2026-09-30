import "./receipt-timing.css";

import type { AgentTurn, SessionItem } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { useContext } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../../components/console-ui";
import { CopyableId } from "../../../components/list-ui";
import { formatDateTime, formatDuration, MISSING } from "../../../lib/format";
import { DiagnosticScope, turnDiagnosticsQuery } from "../session-diagnostics";

/** Read only the selected root Turn, keeping merged call/result receipts distinct. */
export function ItemReceiptTiming({ turn, items }: { turn: AgentTurn | null; items: readonly SessionItem[] }) {
  const scope = useContext(DiagnosticScope);
  const { t } = useTranslation("diagnostics");
  return scope && turn
    ? <ReceiptRead scope={scope} turn={turn} items={items} />
    : <p>{t("receiptMissing")}</p>;
}

function ReceiptRead({ scope, turn, items }: { scope: { projectId: string; sessionId: string }; turn: AgentTurn; items: readonly SessionItem[] }) {
  const { t, i18n } = useTranslation("diagnostics");
  const query = useQuery(turnDiagnosticsQuery(scope, turn));
  if (query.isPending) return <div className="skeleton-bar" role="status" aria-label={t("checking")} />;
  if (query.isError || query.data.status !== turn.status) return <p role="status">{t("receiptUnavailable")} <button type="button" className="text-action" onClick={() => void query.refetch()}>{t("retry")}</button></p>;
  const date = (value: string | null) => value === null ? MISSING : formatDateTime(Date.parse(value) / 1000, i18n.resolvedLanguage);
  return <section className="trace-receipt-timing" aria-label={t("receiptTitle")}>
    <p className="status-with-help">{t("receiptTitle")}<HelpTip>{t("receiptHelp")}</HelpTip></p>
    {items.map((item) => {
      const timing = query.data.items.find((entry) => entry.item_id.toLowerCase() === item.id.toLowerCase());
      return <div className="trace-receipt-item" key={item.id}>
        {items.length > 1 ? <CopyableId id={item.id} compact /> : null}
        {timing ? <dl>
          <div><dt>{t("receiptStarted")}</dt><dd>{date(timing.started_at)}</dd></div>
          <div><dt>{t("receiptCompleted")}</dt><dd>{date(timing.completed_at)}</dd></div>
          <div><dt>{t("receiptDuration")}</dt><dd>{formatDuration(timing.observed_duration_ms === null ? null : timing.observed_duration_ms / 1000)}{timing.observed_duration_ms !== null && timing.observed_duration_ms < 0 ? <HelpTip>{t("clockChange")}</HelpTip> : null}</dd></div>
        </dl> : <p>{t("receiptMissing")}</p>}
      </div>;
    })}
    {query.data.items_truncated ? <p role="status">{t("truncated")}</p> : null}
  </section>;
}
