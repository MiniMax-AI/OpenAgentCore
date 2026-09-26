import type { AgentTurn, SessionItem } from "@agents-core-web/agents-client";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { EmptyState, HelpTip, StatusDot, type Tone } from "../../components/console-ui";
import { CopyableId } from "../../components/list-ui";
import { formatDateTime, formatDuration, formatInteger, MISSING } from "../../lib/format";
import { itemsPerTurn, turnAnchorId, turnDurationSeconds } from "./session-history";

export const turnTone: Record<AgentTurn["status"], Tone> = {
  queued: "pending",
  in_progress: "pending",
  waiting: "warning",
  completed: "neutral",
  failed: "danger",
  cancelled: "neutral",
};

/** One row per Turn in the order Core ran them, with timing, Item count and usage. */
export function SessionTurnsTable({ turns, items, failure = null }: { turns: readonly AgentTurn[]; items: readonly SessionItem[]; failure?: string | null }) {
  const { t, i18n } = useTranslation("sessions");
  const locale = i18n.resolvedLanguage;
  const running = turns.some((turn) => turn.status === "in_progress" || turn.status === "waiting");
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(() => setNow(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [running]);
  const { counts, unassociated } = useMemo(() => itemsPerTurn(turns, items), [items, turns]);

  // Without Turns, a failed read says so rather than "none yet".
  if (!turns.length) return <EmptyState title={failure ?? t("turnTable.none")} />;
  return (
    <>
      <div className="table-frame">
        <table className="data-table session-turns-table" aria-label={t("turns.observed", { count: turns.length })}>
          <thead>
            <tr>
              <th scope="col">{t("turnTable.turn")}</th>
              <th scope="col">{t("turnTable.status")}</th>
              <th scope="col">{t("turnTable.started")}</th>
              <th scope="col" className="numeric">{t("turnTable.duration")}</th>
              <th scope="col" className="numeric">{t("turnTable.items")}</th>
              <th scope="col" className="numeric">{t("turnTable.input")}</th>
              <th scope="col" className="numeric">{t("turnTable.output")}</th>
              <th scope="col" className="numeric">{t("turnTable.total")}</th>
            </tr>
          </thead>
          <tbody>
            {turns.map((turn, index) => {
              const itemCount = counts.get(turn.id) ?? 0;
              return (
                <tr key={turn.id} id={turnAnchorId(turn.id)} tabIndex={-1}>
                  <th scope="row">
                    <span className="name-cell">
                      <span className="name-cell-title">{t("turnTable.number", { number: index + 1 })}</span>
                      <CopyableId id={turn.id} compact />
                    </span>
                  </th>
                  <td>
                    <span className="status-with-help">
                      <StatusDot tone={turnTone[turn.status] ?? "neutral"} label={t(`status.${turn.status}`)} />
                      {turn.error ? <HelpTip label={t("turnTable.errorLabel")}>{turn.error.message}</HelpTip> : null}
                    </span>
                  </td>
                  <td className="session-nowrap">{turn.started_at === null ? MISSING : formatDateTime(turn.started_at, locale)}</td>
                  <td className="numeric">{formatDuration(turnDurationSeconds(turn, now))}</td>
                  <td className="numeric" title={t("turns.linkedItems", { count: itemCount })}>{formatInteger(itemCount, locale)}</td>
                  <td className="numeric">{formatInteger(turn.usage?.input_tokens, locale)}</td>
                  <td className="numeric">{formatInteger(turn.usage?.output_tokens, locale)}</td>
                  <td className="numeric">{formatInteger(turn.usage?.total_tokens, locale)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {unassociated ? <p className="page-status" role="status">{t("turns.unassociated", { count: unassociated })}</p> : null}
    </>
  );
}
