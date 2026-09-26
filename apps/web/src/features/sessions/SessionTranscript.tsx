import type { AgentTurn, SessionItem } from "@agents-core-web/agents-client";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { StatusDot } from "../../components/console-ui";
import { formatDateTime, formatDuration, formatInteger } from "../../lib/format";
import { ThreadItems } from "./items/ItemRenderers";
import { transcriptGroups, turnAnchorId, turnDurationSeconds } from "./session-history";
import { turnTone } from "./SessionTurnsTable";

/**
 * The conversation as a chat, one block per Turn: a quiet line with the
 * Turn's status, duration, tokens and start time, then the user's message on
 * the right and the Agent's work and reply on the left.
 */
export function SessionTranscript({ turns, items, agentName }: { turns: readonly AgentTurn[]; items: readonly SessionItem[]; agentName: string }) {
  const { t, i18n } = useTranslation("sessions");
  const activityOf = (status: AgentTurn["status"]) => (status === "in_progress" ? t("history.working") : status === "queued" ? t("history.queued") : null);
  const locale = i18n.resolvedLanguage;
  const groups = useMemo(() => transcriptGroups(turns, items), [items, turns]);
  const now = Math.floor(Date.now() / 1000);
  return (
    <ol className="chat-thread" aria-label={t("history.conversation")}>
      {groups.map((group) => {
        const turn = group.turn;
        const duration = turn ? turnDurationSeconds(turn, now) : null;
        const key = turn?.id ?? "unassociated";
        return (
          <li className="chat-turn" key={key} id={turn ? turnAnchorId(turn.id) : undefined} tabIndex={turn ? -1 : undefined}>
            {turn || turns.length ? (
              <header className="chat-turn-meta">
                <span className="chat-turn-title">{group.number ? t("turnTable.number", { number: group.number }) : t("history.unassociated")}</span>
                {turn ? <StatusDot tone={turnTone[turn.status] ?? "neutral"} label={t(`status.${turn.status}`)} /> : null}
                {duration !== null ? <span>{formatDuration(duration)}</span> : null}
                {turn?.usage ? <span>{t("history.tokens", { value: formatInteger(turn.usage.total_tokens, locale) })}</span> : null}
                {turn?.started_at != null ? <time className="chat-turn-time">{formatDateTime(turn.started_at, locale)}</time> : null}
              </header>
            ) : null}
            {group.items.length || turn?.error || (turn && activityOf(turn.status)) ? (
              <div className="chat-stack">
                <ThreadItems items={group.items} agentName={agentName} error={turn?.error?.message ?? null} activity={turn ? activityOf(turn.status) : null} />
              </div>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}
