import type { AgentTurn, SessionItem } from "@agents-core-web/agents-client";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { StatusDot } from "../../components/console-ui";
import { formatDateTime, formatDuration, formatInteger } from "../../lib/format";
import { ThreadItems } from "./items/ItemRenderers";
import { transcriptGroups, turnDurationSeconds } from "./session-history";
import { turnTone } from "./SessionTurnsTable";

/**
 * The conversation as a compact transcript: one block per Turn with its
 * status, duration, tokens and start time, then who said what, left-aligned.
 */
export function SessionTranscript({ turns, items, agentName }: { turns: readonly AgentTurn[]; items: readonly SessionItem[]; agentName: string }) {
  const { t, i18n } = useTranslation("sessions");
  const locale = i18n.resolvedLanguage;
  const groups = useMemo(() => transcriptGroups(turns, items), [items, turns]);
  const now = Math.floor(Date.now() / 1000);
  return (
    <ol className="transcript" aria-label={t("history.conversation")}>
      {groups.map((group) => {
        const turn = group.turn;
        const duration = turn ? turnDurationSeconds(turn, now) : null;
        const key = turn?.id ?? "unassociated";
        return (
          <li className="transcript-turn" key={key}>
            {turn || turns.length ? (
              <header className="transcript-turn-head">
                <span className="transcript-turn-title">{group.number ? t("turnTable.number", { number: group.number }) : t("history.unassociated")}</span>
                {turn ? <StatusDot tone={turnTone[turn.status] ?? "neutral"} label={t(`status.${turn.status}`)} /> : null}
                {duration !== null ? <span>{formatDuration(duration)}</span> : null}
                {turn?.usage ? <span>{t("history.tokens", { value: formatInteger(turn.usage.total_tokens, locale) })}</span> : null}
                {turn?.started_at != null ? <time className="transcript-turn-time">{formatDateTime(turn.started_at, locale)}</time> : null}
              </header>
            ) : null}
            {group.items.length || turn?.error ? (
              <div className="transcript-rows">
                <ThreadItems items={group.items} agentName={agentName} />
                {turn?.error ? (
                  <div className="transcript-row error">
                    <span className="transcript-role">{t("history.error")}</span>
                    <div className="transcript-content">{turn.error.message}</div>
                  </div>
                ) : null}
              </div>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}
